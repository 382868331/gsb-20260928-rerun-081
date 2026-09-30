package engine

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"flowplatform/internal/model"
)

// TaskHandler performs automatic work for a task node.
// Returning an error triggers compensation of succeeded tasks on the path.
type TaskHandler func(inst *model.Instance, node model.Node) error

// CompensateHandler undoes a task. It may itself fail.
type CompensateHandler func(inst *model.Instance, node model.Node) error

type Engine struct {
	task       TaskHandler
	compensate CompensateHandler
}

func New(task TaskHandler, compensate CompensateHandler) *Engine {
	return &Engine{task: task, compensate: compensate}
}

type defLookup interface {
	GetDefinition(id string) (*model.Definition, error)
}

func (e *Engine) addRecord(inst *model.Instance, nodeID, edgeID, action string, status model.NodeStatus, detail string) {
	inst.Records = append(inst.Records, model.StepRecord{
		Seq:        len(inst.Records) + 1,
		NodeID:     nodeID,
		EdgeID:     edgeID,
		Action:     action,
		Status:     status,
		Detail:     detail,
		OccurredAt: time.Now().UTC(),
	})
}

// Run executes forward until the instance completes, waits, or fails.
// (with compensation attempted). Safe to call again after restart.
func (e *Engine) Run(def *model.Definition, inst *model.Instance) {
	nodeMap := map[string]model.Node{}
	for _, n := range def.Nodes {
		nodeMap[n.ID] = n
	}
	// Crash mid-compensation: a fail was recorded but the compensation
	// chain never reached a terminal record. Finish compensating first
	// instead of moving forward or redoing any step.
	if inst.Status == model.StatusRunning && compensationPending(inst) {
		failed, ok := nodeMap[lastFailedNode(inst)]
		if !ok {
			failed = model.Node{ID: inst.StoppedAt, Name: inst.StoppedAt}
		}
		e.compensateBackward(def, inst, failed)
		return
	}
	for inst.Status == model.StatusRunning {
		node, ok := nodeMap[inst.CurrentNode]
		if !ok {
			inst.Status = model.StatusFailed
			inst.StoppedAt = inst.CurrentNode
			return
		}
		switch node.Type {
		case model.NodeStart, model.NodeEnd:
			e.addRecord(inst, node.ID, "", string(node.Type), model.NodeEntered, node.Name)
			if node.Type == model.NodeEnd {
				e.addRecord(inst, node.ID, "", "complete", model.NodeSucceeded, "流程结束")
				inst.Status = model.StatusCompleted
				inst.StoppedAt = node.ID
				inst.CurrentNode = node.ID
				return
			}
			e.advance(def, inst, node, "")
		case model.NodeTask:
			e.addRecord(inst, node.ID, "", "enter", model.NodeEntered, node.Name)
			if alreadySucceeded(inst, node.ID) {
				// Restarted after a crash between executing the task and
				// persisting: never complete the same step twice.
				e.addRecord(inst, node.ID, "", "skip_redone", model.NodeSucceeded, "节点已生效过，跳过重放")
				e.advance(def, inst, node, "")
				continue
			}
			if err := e.task(inst, node); err != nil {
				e.addRecord(inst, node.ID, "", "fail", model.NodeFailed, err.Error())
				e.compensateBackward(def, inst, node)
				return
			}
			e.addRecord(inst, node.ID, "", "succeed", model.NodeSucceeded, node.Name)
			e.advance(def, inst, node, "")
		case model.NodeDecision:
			e.addRecord(inst, node.ID, "", "enter", model.NodeEntered, node.Name)
			edge, err := pickEdge(def.Edges, node.ID, inst.Data)
			if err != nil {
				e.addRecord(inst, node.ID, "", "fail", model.NodeFailed, err.Error())
				e.compensateBackward(def, inst, node)
				return
			}
			e.addRecord(inst, node.ID, edge.ID, "branch", model.NodeSucceeded, edgeLabel(edge))
			inst.CurrentNode = edge.To
		case model.NodeWait:
			e.addRecord(inst, node.ID, "", "wait", model.NodeEntered, node.Name)
			inst.Status = model.StatusWaiting
			inst.WaitNode = node.ID
			inst.StoppedAt = node.ID
			return
		}
	}
}

// compensationPending reports whether records show a failure whose
// compensation chain never reached a terminal record (crash mid-compensation).
func compensationPending(inst *model.Instance) bool {
	lastFail := -1
	terminal := false
	for i, rec := range inst.Records {
		switch rec.Action {
		case "fail":
			lastFail = i
			terminal = false
		case "compensate_done", "compensation_fail":
			if i > lastFail {
				terminal = true
			}
		}
	}
	return lastFail >= 0 && !terminal
}

// lastFailedNode returns the node id of the most recent fail record.
func lastFailedNode(inst *model.Instance) string {
	for i := len(inst.Records) - 1; i >= 0; i-- {
		if inst.Records[i].Action == "fail" {
			return inst.Records[i].NodeID
		}
	}
	return inst.StoppedAt
}

// alreadySucceeded reports whether the node has a succeed record that was
// not later compensated — used to avoid completing the same step twice.
func alreadySucceeded(inst *model.Instance, nodeID string) bool {
	succeeded := false
	for _, rec := range inst.Records {
		if rec.NodeID != nodeID {
			continue
		}
		switch rec.Action {
		case "succeed":
			succeeded = true
		case "compensated":
			succeeded = false
		}
	}
	return succeeded
}

func edgeLabel(edge model.Edge) string {
	if edge.Label != "" {
		return edge.Label
	}
	return edge.Condition
}

func (e *Engine) advance(def *model.Definition, inst *model.Instance, node model.Node, via string) {
	edges := outgoing(def.Edges, node.ID)
	if len(edges) == 0 {
		inst.Status = model.StatusFailed
		inst.StoppedAt = node.ID
		e.addRecord(inst, node.ID, "", "fail", model.NodeFailed, "节点没有后继边")
		return
	}
	edge := edges[0]
	e.addRecord(inst, edge.To, edge.ID, "traverse", model.NodeEntered, edgeLabel(edge))
	inst.CurrentNode = edge.To
}

// DeliverInput resumes a waiting instance. Duplicate key -> no-op advance.
func (e *Engine) DeliverInput(def *model.Definition, inst *model.Instance, input model.ExternalInput) error {
	if input.Key == "" {
		return fmt.Errorf("缺少幂等键 key")
	}
	// A consumed key is accepted as a no-op forever: retried/duplicate
	// submissions never advance the instance a second time.
	if inst.ConsumedInputs[input.Key] {
		e.addRecord(inst, inputContextNode(inst, input), "", "duplicate_input", model.NodeEntered, "重复的输入 "+input.Key+" 已忽略，业务动作未重复执行")
		return nil
	}
	if inst.Status != model.StatusWaiting || inst.WaitNode == "" {
		// Late input for a wait that is already gone (failed / completed) must not
		// rerun any business action. Record it as a visible event and stay put.
		e.addRecord(inst, inputContextNode(inst, input), "", "late_input", model.NodeEntered,
			fmt.Sprintf("迟到的输入 %s 已忽略：实例当前状态为 %s，停在 %s", input.Key, inst.Status, effectiveStop(inst)))
		return nil
	}
	if input.WaitNode != "" && input.WaitNode != inst.WaitNode {
		return fmt.Errorf("输入目标节点 %s 与等待节点 %s 不一致", input.WaitNode, inst.WaitNode)
	}
	for k, v := range input.Payload {
		inst.Data[k] = v
	}
	inst.ConsumedInputs[input.Key] = true
	waitNode := inst.WaitNode
	e.addRecord(inst, waitNode, "", "input_received", model.NodeSucceeded, "接收输入 "+input.Key)
	inst.WaitNode = ""
	inst.Status = model.StatusRunning
	// The wait node itself is now complete; move along its outgoing edge once.
	out := outgoing(def.Edges, waitNode)
	if len(out) == 0 {
		inst.Status = model.StatusFailed
		inst.StoppedAt = waitNode
		e.addRecord(inst, waitNode, "", "fail", model.NodeFailed, "等待节点没有后继边")
		return nil
	}
	edge := out[0]
	e.addRecord(inst, edge.To, edge.ID, "traverse", model.NodeEntered, edgeLabel(edge))
	inst.CurrentNode = edge.To
	e.Run(def, inst)
	return nil
}

// inputContextNode picks the most meaningful node for an input that does not
// advance the instance: the wait node when still waiting, otherwise the node
// supplied by the caller or the recorded stopping/current node.
func inputContextNode(inst *model.Instance, input model.ExternalInput) string {
	if inst.WaitNode != "" {
		return inst.WaitNode
	}
	if input.WaitNode != "" {
		return input.WaitNode
	}
	return effectiveStop(inst)
}

func effectiveStop(inst *model.Instance) string {
	if inst.StoppedAt != "" {
		return inst.StoppedAt
	}
	return inst.CurrentNode
}

// Resume continues an instance that was persisted in "running" state, for
// example after a process restart. Waiting / completed / failed instances
// are left untouched.
func (e *Engine) Resume(def *model.Definition, inst *model.Instance) {
	if inst.Status != model.StatusRunning {
		return
	}
	e.Run(def, inst)
}

// compensateBackward walks succeeded task nodes back along the recorded path
// and invokes their compensation. A failing compensation stops everything
// and records the true stopping node.
func (e *Engine) compensateBackward(def *model.Definition, inst *model.Instance, failed model.Node) {
	nodeMap := map[string]model.Node{}
	for _, n := range def.Nodes {
		nodeMap[n.ID] = n
	}
	// Succeeded task nodes in execution order (from records).
	var succeeded []string
	seen := map[string]bool{}
	for _, rec := range inst.Records {
		if rec.Action == "succeed" {
			n := nodeMap[rec.NodeID]
			if n.Type == model.NodeTask && !seen[n.ID] {
				succeeded = append(succeeded, n.ID)
				seen[n.ID] = true
			}
		}
	}
	// The failed node itself may have a compensator too; include tasks reversed.
	for i := len(succeeded) - 1; i >= 0; i-- {
		node := nodeMap[succeeded[i]]
		if alreadyCompensated(inst, node.ID) {
			continue // crash-safe: never compensate the same step twice
		}
		targetID := node.CompensateTo
		if targetID == "" {
			targetID = node.ID
		}
		target := nodeMap[targetID]
		e.addRecord(inst, node.ID, "", "compensate", model.NodeCompensating, "补偿 "+node.Name)
		if err := e.compensate(inst, target); err != nil {
			e.addRecord(inst, node.ID, "", "compensation_fail", model.NodeCompFailed, err.Error())
			inst.Status = model.StatusFailed
			inst.StoppedAt = node.ID
			return
		}
		e.addRecord(inst, node.ID, "", "compensated", model.NodeCompensated, node.Name)
	}
	e.addRecord(inst, failed.ID, "", "compensate_done", model.NodeFailed, "失败后补偿全部完成")
	inst.Status = model.StatusFailed
	inst.StoppedAt = failed.ID
}

func alreadyCompensated(inst *model.Instance, nodeID string) bool {
	for _, rec := range inst.Records {
		if rec.NodeID == nodeID && rec.Action == "compensated" {
			return true
		}
	}
	return false
}

func outgoing(edges []model.Edge, from string) []model.Edge {
	var out []model.Edge
	for _, e := range edges {
		if e.From == from {
			out = append(out, e)
		}
	}
	return out
}

func pickEdge(edges []model.Edge, from string, data map[string]any) (model.Edge, error) {
	out := outgoing(edges, from)
	var defaultEdge model.Edge
	for _, edge := range out {
		if strings.TrimSpace(edge.Condition) == "" || edge.Condition == "default" {
			defaultEdge = edge
			continue
		}
		ok, err := evalCond(edge.Condition, data)
		if err != nil {
			return model.Edge{}, fmt.Errorf("条件 %q 解析失败: %w", edge.Condition, err)
		}
		if ok {
			return edge, nil
		}
	}
	if defaultEdge.ID != "" {
		return defaultEdge, nil
	}
	return model.Edge{}, fmt.Errorf("节点 %s 没有命中的分支", from)
}

// evalCond supports a small safe expression grammar:
//
//	key == "str" | key == 123 | key != ... | key > >= < <= number | true/false
//
// and boolean keys. No arbitrary code execution.
func evalCond(expr string, data map[string]any) (bool, error) {
	expr = strings.TrimSpace(expr)
	for _, op := range []string{">=", "<=", "==", "!=", ">", "<"} {
		if i := strings.Index(expr, op); i >= 0 {
			key := strings.TrimSpace(expr[:i])
			raw := strings.TrimSpace(expr[i+len(op):])
			val := data[key]
			return compare(val, raw, op)
		}
	}
	switch expr {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		b, ok := data[expr].(bool)
		if !ok {
			return false, fmt.Errorf("未知条件 %q", expr)
		}
		return b, nil
	}
}

func compare(val any, raw, op string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if n, err := strconv.ParseFloat(raw, 64); err == nil {
		f, ok := toFloat(val)
		if !ok {
			return false, fmt.Errorf("值不是数字")
		}
		switch op {
		case "==":
			return f == n, nil
		case "!=":
			return f != n, nil
		case ">":
			return f > n, nil
		case ">=":
			return f >= n, nil
		case "<":
			return f < n, nil
		case "<=":
			return f <= n, nil
		}
	}
	// string literal
	s := raw
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	}
	got := fmt.Sprintf("%v", val)
	switch op {
	case "==":
		return got == s, nil
	case "!=":
		return got != s, nil
	default:
		return false, fmt.Errorf("操作符 %s 不能用于字符串", op)
	}
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}
