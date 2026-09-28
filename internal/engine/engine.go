// Package engine 是流程执行引擎。
//
// 引擎只根据持久化的实例状态推进：每次推进先落盘执行记录，再决定下一步。
// 服务重启后对 running / compensating 的实例从其停留节点继续。
package engine

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"workflow/internal/model"
	"workflow/internal/store"
)

type Engine struct {
	store *store.Store
	now   func() time.Time
	mu    sync.Mutex // 串行化所有实例推进，避免并发重复执行
}

func New(s *store.Store) *Engine {
	return &Engine{store: s, now: time.Now}
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// StartInstance 固化当前发布的定义版本快照并开始执行。
func (e *Engine) StartInstance(defKey string, input map[string]any) (*model.Instance, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	version, err := e.store.LatestVersion(defKey)
	if err != nil {
		return nil, err
	}
	if version == 0 {
		return nil, fmt.Errorf("流程 %s 尚未发布", defKey)
	}
	def, err := e.store.GetDefinition(defKey, version)
	if err != nil {
		return nil, err
	}
	if err := Validate(def); err != nil {
		return nil, err
	}
	vars := map[string]any{}
	for k, v := range input {
		vars[k] = v
	}
	inst := &model.Instance{
		ID:         newID(),
		DefKey:     defKey,
		Version:    version,
		Definition: *def, // 版本快照：之后发布新版本不影响本实例
		Status:     model.StatusRunning,
		Variables:  vars,
		Current:    nil,
		WaitingFor: map[string]string{},
		CreatedAt:  e.now(),
	}
	start := def.Nodes[0]
	if err := e.advanceInto(inst, start.ID, false); err != nil {
		return nil, err
	}
	if err := e.store.SaveInstance(inst); err != nil {
		return nil, err
	}
	return inst, nil
}

// Resume 在服务重启后调用：仍处于 running / compensating 的实例从中断点继续。
func (e *Engine) Resume(inst *model.Instance) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if inst.Status != model.StatusRunning && inst.Status != model.StatusCompensating {
		return nil
	}
	if len(inst.Current) == 0 {
		return nil
	}
	if err := e.executeCurrent(inst, inst.Current[0]); err != nil {
		return err
	}
	return e.store.SaveInstance(inst)
}

// SubmitInput 提交外部输入。相同幂等键（或同一等待节点的第二次有效提交）
// 只记录为 duplicate，不会重复推进流程。
func (e *Engine) SubmitInput(instID, nodeID, idemKey string, payload any) (*model.Instance, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	inst, err := e.store.GetInstance(instID)
	if err != nil {
		return nil, err
	}
	if idemKey == "" {
		idemKey = newID()
	}
	rec := model.InputRecord{
		Seq:            len(inst.Inputs) + 1,
		IdempotencyKey: idemKey,
		NodeID:         nodeID,
		At:             e.now(),
		Payload:        payload,
	}
	for _, in := range inst.Inputs {
		if in.IdempotencyKey == idemKey || (in.NodeID == nodeID && !in.Duplicate) {
			rec.Duplicate = true
			inst.Inputs = append(inst.Inputs, rec)
			inst.UpdatedAt = e.now()
			return inst, e.store.SaveInstance(inst)
		}
	}
	if inst.Status != model.StatusWaiting {
		return nil, fmt.Errorf("实例当前不在等待输入")
	}
	wantKey, ok := inst.WaitingFor[nodeID]
	if !ok {
		return nil, fmt.Errorf("节点 %s 没有在等待输入", nodeID)
	}
	rec.InputKey = wantKey
	inst.Inputs = append(inst.Inputs, rec)

	// 输入写入变量：对象展开其字段，同时整体挂在所等待的键名下。
	inst.Variables[wantKey] = payload
	if m, ok := payload.(map[string]any); ok {
		for k, v := range m {
			inst.Variables[k] = v
		}
	}
	delete(inst.WaitingFor, nodeID)

	// 离开等待节点并沿默认出边继续。
	e.recordStep(inst, nodeID, model.StepLeave, false, "", "")
	next, err := e.follow(inst, nodeID, model.BranchDefault, false)
	if err != nil {
		return nil, err
	}
	inst.Status = model.StatusRunning
	if err := e.advanceInto(inst, next, false); err != nil {
		return nil, err
	}
	inst.UpdatedAt = e.now()
	return inst, e.store.SaveInstance(inst)
}

// advanceInto 沿边进入节点并执行，直到结束、失败或挂起。
func (e *Engine) advanceInto(inst *model.Instance, nodeID string, comp bool) error {
	for {
		node := findNode(&inst.Definition, nodeID)
		if node == nil {
			return fmt.Errorf("节点 %s 不存在于定义版本 v%d", nodeID, inst.Version)
		}
		inst.Current = []string{nodeID}
		e.recordStep(inst, nodeID, model.StepEnter, comp, "", "")

		switch node.Kind {
		case model.KindStart, model.KindEnd:
			e.recordStep(inst, nodeID, model.StepLeave, comp, "", "")
			if node.Kind == model.KindEnd {
				inst.Current = nil
				inst.Status = model.StatusCompleted
				return nil
			}
			next, err := e.follow(inst, nodeID, model.BranchDefault, comp)
			if err != nil {
				return err
			}
			nodeID = next
		case model.KindCondition:
			val, err := Eval(node.Condition, inst.Variables)
			if err != nil {
				return fmt.Errorf("节点 %s 条件求值失败: %w", nodeID, err)
			}
			branch := model.BranchTrue
			if !truthy(val) {
				branch = model.BranchFalse
			}
			e.recordStep(inst, nodeID, model.StepLeave, comp, "", strings.ToUpper(string(branch)))
			next, err := e.follow(inst, nodeID, branch, comp)
			if err != nil {
				return err
			}
			nodeID = next
		case model.KindWait:
			inst.Status = model.StatusWaiting
			inst.WaitingFor[nodeID] = node.InputKey
			inst.UpdatedAt = e.now()
			return e.store.SaveInstance(inst)
		case model.KindTask:
			return e.executeCurrent(inst, nodeID)
		default:
			return fmt.Errorf("未知节点类型 %q", node.Kind)
		}
	}
}

// executeCurrent 执行当前停留的 task 节点，处理失败补偿。
// 重启恢复时直接调用本方法，不会重复写入 enter 记录。
func (e *Engine) executeCurrent(inst *model.Instance, nodeID string) error {
	node := findNode(&inst.Definition, nodeID)
	if node == nil {
		return fmt.Errorf("节点 %s 不存在", nodeID)
	}
	compMode := inst.Status == model.StatusCompensating

	if !compMode {
		if failReason, fail := taskFails(inst.Variables, nodeID, "failTasks"); fail {
			e.recordStep(inst, nodeID, model.StepLeave, false, "error", failReason)
			return e.handleTaskFailure(inst, node, failReason)
		}
		e.recordStep(inst, nodeID, model.StepLeave, false, "ok", "")
		next, err := e.follow(inst, nodeID, model.BranchDefault, false)
		if err != nil {
			return err
		}
		return e.advanceInto(inst, next, false)
	}

	// 补偿节点执行：补偿也可能失败，此时实例真实停在该补偿步骤。
	if reason, fail := taskFails(inst.Variables, nodeID, "failComp"); fail {
		e.recordStep(inst, nodeID, model.StepLeave, true, "error", reason)
		inst.Current = []string{nodeID}
		inst.Status = model.StatusCompFailed
		inst.Error = fmt.Sprintf("补偿节点 %s 执行失败: %s", node.Name, reason)
		inst.UpdatedAt = e.now()
		return e.store.SaveInstance(inst)
	}
	e.recordStep(inst, nodeID, model.StepLeave, true, "ok", "")
	inst.Current = nil
	inst.Status = model.StatusFailed
	// inst.Error 保留原始任务失败原因，便于追查补偿来源。
	inst.UpdatedAt = e.now()
	return e.store.SaveInstance(inst)
}

func (e *Engine) handleTaskFailure(inst *model.Instance, node *model.Node, reason string) error {
	if node.Compensate == "" {
		inst.Current = []string{node.ID}
		inst.Status = model.StatusFailed
		inst.Error = fmt.Sprintf("任务 %s 执行失败: %s", node.Name, reason)
		inst.UpdatedAt = e.now()
		return e.store.SaveInstance(inst)
	}
	compNode := findNode(&inst.Definition, node.Compensate)
	if compNode == nil {
		return fmt.Errorf("节点 %s 配置的补偿节点 %s 不存在", node.ID, node.Compensate)
	}
	inst.Status = model.StatusCompensating
	inst.Error = fmt.Sprintf("任务 %s 执行失败: %s", node.Name, reason)
	next, err := e.followComp(inst, node.ID, compNode.ID)
	if err != nil {
		return err
	}
	return e.advanceInto(inst, next, true)
}

// follow 沿普通边流转并写一条流转记录（页面箭头的数据来源）。
func (e *Engine) follow(inst *model.Instance, from string, branch model.EdgeBranch, comp bool) (string, error) {
	for _, edge := range inst.Definition.Edges {
		if edge.From == from && edge.Branch == branch {
			inst.Transitions = append(inst.Transitions, model.TransitionRecord{
				Seq:    len(inst.Transitions) + 1,
				EdgeID: edge.ID,
				From:   edge.From,
				To:     edge.To,
				Comp:   comp,
				At:     e.now(),
			})
			return edge.To, nil
		}
	}
	return "", fmt.Errorf("节点 %s 缺少分支 %q 的出边", from, branch)
}

// followComp 记录失败任务到补偿节点的流转。该边不必画在定义图上，
// 但同样产生一条 comp=true 的流转记录，页面据此画出补偿箭头。
func (e *Engine) followComp(inst *model.Instance, from, to string) (string, error) {
	id := "comp:" + from + "->" + to
	inst.Transitions = append(inst.Transitions, model.TransitionRecord{
		Seq:    len(inst.Transitions) + 1,
		EdgeID: id,
		From:   from,
		To:     to,
		Comp:   true,
		At:     e.now(),
	})
	return to, nil
}

func (e *Engine) recordStep(inst *model.Instance, nodeID string, ev model.StepEvent, comp bool, result, msg string) {
	node := findNode(&inst.Definition, nodeID)
	var kind model.NodeKind
	if node != nil {
		kind = node.Kind
	}
	inst.Steps = append(inst.Steps, model.StepRecord{
		Seq:     len(inst.Steps) + 1,
		NodeID:  nodeID,
		Kind:    kind,
		Event:   ev,
		Comp:    comp,
		Result:  result,
		Message: msg,
		At:      e.now(),
	})
}

func findNode(d *model.Definition, id string) *model.Node {
	for i := range d.Nodes {
		if d.Nodes[i].ID == id {
			return &d.Nodes[i]
		}
	}
	return nil
}

// truthy 定义条件分支的真假：nil/false/0/"" 为假，其余为真。
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t != ""
	default:
		return true
	}
}

// taskFails 通过变量约定模拟任务失败：
// failTasks / failComp 可以是字符串数组或逗号分隔字符串，列出要失败的节点 ID。
func taskFails(vars map[string]any, nodeID, varKey string) (string, bool) {
	rawn, ok := vars[varKey]
	if !ok {
		return "", false
	}
	switch v := rawn.(type) {
	case []any:
		for _, item := range v {
			if fmt.Sprint(item) == nodeID {
				return varKey + " 注入失败", true
			}
		}
	case string:
		for _, item := range strings.Split(v, ",") {
			if strings.TrimSpace(item) == nodeID {
				return varKey + " 注入失败", true
			}
		}
	}
	return "", false
}
