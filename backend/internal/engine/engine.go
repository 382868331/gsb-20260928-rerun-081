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
	// onPersist, when set, is called after every state-changing compensation
	// step so a crash mid-compensation resumes without redoing anything.
	onPersist func(*model.Instance)
}

func New(task TaskHandler, compensate CompensateHandler) *Engine {
	return &Engine{task: task, compensate: compensate}
}

// WithPersist installs a callback invoked after each durable engine step.
func (e *Engine) WithPersist(fn func(*model.Instance)) *Engine {
	e.onPersist = fn
	return e
}

func (e *Engine) persist(inst *model.Instance) {
	if e.onPersist != nil {
		e.onPersist(inst)
	}
}

type defLookup interface {
	GetDefinition(id string) (*model.Definition, error)
}

// MigrationImpact describes what changes for a waiting instance if it is
// switched to a new definition version. Only the wait point and the steps
// that still lie ahead are affected; history under the old version is kept.
type MigrationImpact struct {
	InstanceID    string         `json:"instanceId"`
	WaitNode      string         `json:"waitNode"`
	WaitNodeName  string         `json:"waitNodeName"`
	FromVersion   int            `json:"fromVersion"`
	ToVersion     int            `json:"toVersion"`
	WaitNodeInNew bool           `json:"waitNodeInNew"`
	NewNodes      []model.Node   `json:"newNodes"`
	RemovedNodes  []string       `json:"removedNodes"`
	NewEdges      []model.Edge   `json:"newEdges"`
	RemovedEdges  []string       `json:"removedEdges"`
	AffectedAhead []AffectedStep `json:"affectedAhead"`
}

// AffectedStep is a node reachable after the wait point in the new graph that
// is new/removed/changed and therefore changes what the instance will do.
type AffectedStep struct {
	NodeID string `json:"nodeId"`
	Name   string `json:"name,omitempty"`
	Kind   string `json:"kind"` // added / removed / changed / edge_added / edge_changed
}

// PreviewMigration computes the impact without changing the instance. The
// wait node must still exist in the target definition (same id).
func PreviewMigration(inst *model.Instance, from, to *model.Definition) (*MigrationImpact, error) {
	if inst.Status != model.StatusWaiting || inst.WaitNode == "" {
		return nil, fmt.Errorf("只有等待人工输入的实例才能迁移版本，当前状态为 %s", inst.Status)
	}
	if inst.DefinitionID == to.ID {
		return nil, fmt.Errorf("实例已经使用该版本")
	}
	waitInNew := false
	var waitName string
	oldByID := map[string]model.Node{}
	for _, n := range from.Nodes {
		if n.ID == inst.WaitNode {
			waitName = n.Name
		}
		oldByID[n.ID] = n
	}
	newByID := map[string]model.Node{}
	for _, n := range to.Nodes {
		newByID[n.ID] = n
		if n.ID == inst.WaitNode {
			waitInNew = true
			if waitName == "" {
				waitName = n.Name
			}
		}
	}
	if !waitInNew {
		return nil, fmt.Errorf("新版本中不存在等待节点 %s，无法迁移；已运行实例继续使用旧版本", inst.WaitNode)
	}

	impact := &MigrationImpact{
		InstanceID:    inst.ID,
		WaitNode:      inst.WaitNode,
		WaitNodeName:  waitName,
		FromVersion:   from.Version,
		ToVersion:     to.Version,
		WaitNodeInNew: true,
		NewNodes:      to.Nodes,
		NewEdges:      to.Edges,
	}
	for _, n := range to.Nodes {
		old, ok := oldByID[n.ID]
		if !ok {
			impact.AffectedAhead = append(impact.AffectedAhead, AffectedStep{NodeID: n.ID, Name: n.Name, Kind: "added"})
		} else if old.Type != n.Type || old.Name != n.Name || old.CompensateTo != n.CompensateTo {
			impact.AffectedAhead = append(impact.AffectedAhead, AffectedStep{NodeID: n.ID, Name: n.Name, Kind: "changed"})
		}
	}
	for _, n := range from.Nodes {
		if _, ok := newByID[n.ID]; !ok {
			impact.RemovedNodes = append(impact.RemovedNodes, n.ID)
			impact.AffectedAhead = append(impact.AffectedAhead, AffectedStep{NodeID: n.ID, Name: n.Name, Kind: "removed"})
		}
	}
	oldEdges := map[string]model.Edge{}
	for _, ed := range from.Edges {
		oldEdges[ed.ID] = ed
	}
	newEdges := map[string]model.Edge{}
	for _, ed := range to.Edges {
		newEdges[ed.ID] = ed
		old, ok := oldEdges[ed.ID]
		if !ok {
			impact.AffectedAhead = append(impact.AffectedAhead, AffectedStep{NodeID: ed.To, Kind: "edge_added"})
		} else if old != ed {
			impact.AffectedAhead = append(impact.AffectedAhead, AffectedStep{NodeID: ed.To, Kind: "edge_changed"})
		}
	}
	for _, ed := range from.Edges {
		if _, ok := newEdges[ed.ID]; !ok {
			impact.RemovedEdges = append(impact.RemovedEdges, ed.ID)
		}
	}
	// Keep only steps that actually lie ahead of (or at) the wait point in the
	// new graph, so the user is not warned about already-finished history.
	reachable := reachableFrom(to, inst.WaitNode)
	var ahead []AffectedStep
	for _, a := range impact.AffectedAhead {
		if a.NodeID == inst.WaitNode || reachable[a.NodeID] {
			ahead = append(ahead, a)
		}
	}
	impact.AffectedAhead = ahead
	return impact, nil
}

func reachableFrom(def *model.Definition, from string) map[string]bool {
	out := map[string][]string{}
	for _, ed := range def.Edges {
		out[ed.From] = append(out[ed.From], ed.To)
	}
	seen := map[string]bool{}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range out[cur] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return seen
}

// MigrateWaiting switches a waiting instance to a new definition version
// after the user has reviewed the impact. Existing records remain exactly as
// they were under the old version; only future steps use the new graph.
func (e *Engine) MigrateWaiting(inst *model.Instance, from, to *model.Definition) (*MigrationImpact, error) {
	impact, err := PreviewMigration(inst, from, to)
	if err != nil {
		return nil, err
	}
	inst.DefinitionID = to.ID
	inst.Version = to.Version
	inst.DefinitionHistory = append(inst.DefinitionHistory, model.DefinitionRef{
		DefinitionID: to.ID,
		Name:         to.Name,
		Version:      to.Version,
		Since:        time.Now().UTC(),
	})
	e.addRecord(inst, inst.WaitNode, "", "definition_migrated", model.NodeEntered,
		fmt.Sprintf("实例从 %s v%d 迁移到 v%d；旧版本 %d 条历史记录原样保留，后续步骤按新版本执行", from.Name, from.Version, to.Version, len(inst.Records)))
	return impact, nil
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

// Run executes forward until the instance completes, waits, or fails
// (with compensation attempted). Safe to call again after restart.
func (e *Engine) Run(def *model.Definition, inst *model.Instance) {
	nodeMap := map[string]model.Node{}
	for _, n := range def.Nodes {
		nodeMap[n.ID] = n
	}
	// Crash recovery: a forward Run must never re-execute a task that already
	// produced a succeed record. Reconcile the current node from the last real
	// movement event recorded on disk.
	if inst.Status == model.StatusRunning {
		reconcileCurrentNode(def, inst)
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
			// Idempotent after a crash between task success and persistence of
			// the advance: if this node already succeeded, never run it twice.
			if nodeSucceeded(inst, node.ID) {
				e.advance(def, inst, node, "")
			} else {
				if err := e.task(inst, node); err != nil {
					e.addRecord(inst, node.ID, "", "fail", model.NodeFailed, err.Error())
					e.startCompensation(def, inst, node)
					return
				}
				e.addRecord(inst, node.ID, "", "succeed", model.NodeSucceeded, node.Name)
				e.advance(def, inst, node, "")
			}
		case model.NodeDecision:
			e.addRecord(inst, node.ID, "", "enter", model.NodeEntered, node.Name)
			edge, err := pickEdge(def.Edges, node.ID, inst.Data)
			if err != nil {
				e.addRecord(inst, node.ID, "", "fail", model.NodeFailed, err.Error())
				e.startCompensation(def, inst, node)
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
	if inst.Compensating {
		// A crash happened while compensating. The journal tells us exactly
		// which compensations already completed; never invoke them again.
		failed := defNodeOrMissing(def, inst.FailedNode)
		e.continueCompensation(def, inst, failed)
		return
	}
	e.Run(def, inst)
}

// startCompensation opens the compensation journal and starts undoing
// succeeded tasks in reverse execution order.
func (e *Engine) startCompensation(def *model.Definition, inst *model.Instance, failed model.Node) {
	inst.Compensating = true
	inst.FailedNode = failed.ID
	if inst.CompensatedNodes == nil {
		inst.CompensatedNodes = map[string]bool{}
	}
	e.continueCompensation(def, inst, failed)
}

// continueCompensation walks succeeded task nodes back along the recorded
// path and invokes their compensation, skipping any node already marked in
// the journal. A failing compensation stops everything and records the true
// stopping node. Safe to re-enter after a crash/restart.
func (e *Engine) continueCompensation(def *model.Definition, inst *model.Instance, failed model.Node) {
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
	// Reverse order; the failed node itself never succeeded so is not listed.
	for i := len(succeeded) - 1; i >= 0; i-- {
		node := nodeMap[succeeded[i]]
		if inst.CompensatedNodes[node.ID] {
			// Completed before the crash (or on a previous resume): never twice.
			continue
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
			inst.Compensating = false
			return
		}
		e.addRecord(inst, node.ID, "", "compensated", model.NodeCompensated, node.Name)
		// Journal each success immediately so a later crash cannot redo it.
		inst.CompensatedNodes[node.ID] = true
		e.persist(inst)
	}
	if failed.ID != "" {
		e.addRecord(inst, failed.ID, "", "compensate_done", model.NodeFailed, "失败后补偿全部完成")
	}
	inst.Status = model.StatusFailed
	inst.StoppedAt = failed.ID
	inst.Compensating = false
}

func defNodeOrMissing(def *model.Definition, id string) model.Node {
	for _, n := range def.Nodes {
		if n.ID == id {
			return n
		}
	}
	return model.Node{ID: id}
}

// nodeSucceeded reports whether a task node already produced a succeed event
// in the current execution segment, i.e. since the last external input (or
// from the start). Events from before a wait/input boundary belong to an
// already-finished segment and must not suppress execution after migration
// to a definition that happens to reuse a node id.
func nodeSucceeded(inst *model.Instance, nodeID string) bool {
	start := 0
	for i, rec := range inst.Records {
		if rec.Action == "input_received" {
			start = i + 1
		}
	}
	for i := start; i < len(inst.Records); i++ {
		rec := inst.Records[i]
		if rec.NodeID == nodeID && rec.Action == "succeed" {
			return true
		}
	}
	return false
}

// reconcileCurrentNode rebuilds CurrentNode from the last recorded movement so
// a crash between advancing and saving does not replay an executed node.
func reconcileCurrentNode(def *model.Definition, inst *model.Instance) {
	current := inst.CurrentNode
	for _, rec := range inst.Records {
		if rec.Action == "traverse" && rec.NodeID != "" {
			current = rec.NodeID
		} else if rec.Action == "branch" && rec.EdgeID != "" {
			for _, edge := range def.Edges {
				if edge.ID == rec.EdgeID {
					current = edge.To
				}
			}
		}
	}
	inst.CurrentNode = current
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
