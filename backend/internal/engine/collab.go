package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"flowplatform/internal/model"
)

// Collaborative waits: a wait node with Participants needs one confirmation
// from every effective participant. Partial input never completes the step.
// The acceptance scope is versioned (WaitAuthorization); every change keeps
// the old record, and each decision names the scope it was made under.

// enterWait opens the wait state for a node. Collaborative nodes persist a
// WaitState so a restart keeps waiting with confirmations intact.
func (e *Engine) enterWait(inst *model.Instance, node model.Node) {
	if len(node.Participants) == 0 {
		inst.Wait = nil // a plain wait node is never collaborative
		e.addRecord(inst, node.ID, "", "wait", model.NodeEntered, node.Name)
		inst.Status = model.StatusWaiting
		inst.WaitNode = node.ID
		inst.StoppedAt = node.ID
		return
	}
	now := time.Now().UTC()
	auth := model.WaitAuthorization{
		ID:            "auth-1",
		Participants:  append([]string(nil), node.Participants...),
		AllowDelegate: node.AllowDelegate,
		Delegations:   map[string]string{},
		Since:         now,
		Reason:        "按流程定义初始化参与范围",
	}
	inst.Wait = &model.WaitState{
		NodeID:         node.ID,
		AuthID:         auth.ID,
		Received:       map[string]model.PartyInput{},
		Authorizations: []model.WaitAuthorization{auth},
	}
	if node.TimeoutSeconds > 0 {
		dl := now.Add(time.Duration(node.TimeoutSeconds) * time.Second)
		inst.Wait.Deadline = &dl
	}
	e.addRecord(inst, node.ID, "", "wait", model.NodeEntered,
		fmt.Sprintf("%s（协作等待：需 %s 全部确认；委托%s）", node.Name,
			strings.Join(node.Participants, "、"), map[bool]string{true: "允许", false: "不允许"}[node.AllowDelegate]))
	inst.Status = model.StatusWaiting
	inst.WaitNode = node.ID
	inst.StoppedAt = node.ID
}

// currentAuth returns the authorization in effect (last in history).
func currentAuth(ws *model.WaitState) model.WaitAuthorization {
	return ws.Authorizations[len(ws.Authorizations)-1]
}

// effectiveSubmitters maps each participant to the party currently allowed
// to confirm on their behalf (their delegate when one is active).
func effectiveSubmitters(auth model.WaitAuthorization) map[string]string {
	out := map[string]string{}
	for _, p := range auth.Participants {
		if d, ok := auth.Delegations[p]; ok && d != "" {
			out[p] = d
		} else {
			out[p] = p
		}
	}
	return out
}

func sortedKeys(m map[string]model.PartyInput) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// deliverCollaborative handles one input for a collaborative wait. Exactly
// one decision can ever form: once Decided, every later input is late.
func (e *Engine) deliverCollaborative(def *model.Definition, inst *model.Instance, input model.ExternalInput) error {
	ws := inst.Wait
	// A due timeout wins the race against any simultaneous input/revocation:
	// evaluate it first so only one decision is ever recorded.
	e.checkTimeout(def, inst, time.Now().UTC())
	if ws.Decided || inst.Status != model.StatusWaiting {
		e.addRecord(inst, inputContextNode(inst, input), "", "late_input", model.NodeEntered,
			fmt.Sprintf("迟到的输入 %s 已忽略：等待步骤已有决定（%s），仅作为历史保留", input.Key, ws.Decision))
		return nil
	}
	if inst.ConsumedInputs[input.Key] {
		e.addRecord(inst, inst.WaitNode, "", "duplicate_input", model.NodeEntered, "重复的输入 "+input.Key+" 已忽略，业务动作未重复执行")
		return nil
	}
	auth := currentAuth(ws)
	if !auth.AllowDelegate && len(auth.Delegations) > 0 {
		// Delegations only exist while allowed; scope changes prune them.
		auth.Delegations = map[string]string{}
	}
	effective := effectiveSubmitters(auth)
	allowed := map[string]bool{}
	for _, sub := range effective {
		allowed[sub] = true
	}
	if input.From == "" {
		return fmt.Errorf("协作等待需要指明参与方 from（当前有效：%s）", strings.Join(sortedSubmitters(effective), "、"))
	}
	if !allowed[input.From] {
		e.addRecord(inst, inst.WaitNode, "", "input_rejected", model.NodeEntered,
			fmt.Sprintf("参与方 %s 不在当前有效范围（%s）内，输入 %s 未生效", input.From, strings.Join(sortedSubmitters(effective), "、"), input.Key))
		return nil
	}
	if _, ok := ws.Received[input.From]; ok {
		e.addRecord(inst, inst.WaitNode, "", "duplicate_input", model.NodeEntered,
			fmt.Sprintf("参与方 %s 已确认过，重复输入 %s 忽略", input.From, input.Key))
		return nil
	}
	for k, v := range input.Payload {
		inst.Data[k] = v
	}
	inst.ConsumedInputs[input.Key] = true
	ws.Received[input.From] = model.PartyInput{
		Key: input.Key, From: input.From, Payload: input.Payload, AuthID: auth.ID, At: time.Now().UTC(),
	}
	total := len(auth.Participants)
	done := 0
	for _, sub := range effective {
		if _, ok := ws.Received[sub]; ok {
			done++
		}
	}
	e.addRecord(inst, inst.WaitNode, "", "input_received", model.NodeSucceeded,
		fmt.Sprintf("参与方 %s 已确认（%d/%d，授权范围 %s）；未集齐前步骤不完成", input.From, done, total, auth.ID))
	if done < total {
		return nil
	}
	e.decideWait(inst, "approved", fmt.Sprintf("全部 %d 方已确认", total))
	e.completeWait(def, inst)
	return nil
}

func sortedSubmitters(effective map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range effective {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// decideWait records the single terminal decision of a collaborative wait,
// traceable to the authorization scope in effect right now.
func (e *Engine) decideWait(inst *model.Instance, decision, why string) {
	ws := inst.Wait
	ws.Decided = true
	ws.Decision = decision
	ws.DecisionAuthID = ws.AuthID
	auth := currentAuth(ws)
	e.addRecord(inst, inst.WaitNode, "", "wait_decided", model.NodeSucceeded,
		fmt.Sprintf("协作等待决定：%s（%s；依据授权范围 %s：参与方 %s）", decision, why, auth.ID, strings.Join(auth.Participants, "、")))
}

// completeWait leaves the wait node along its outgoing edge and continues.
func (e *Engine) completeWait(def *model.Definition, inst *model.Instance) {
	waitNode := inst.WaitNode
	if inst.Wait != nil && inst.Wait.Decided {
		// Archive the decided collaborative wait so reviews can trace the
		// original decision and its authorization history forever.
		inst.WaitHistory = append(inst.WaitHistory, inst.Wait)
	}
	inst.WaitNode = ""
	inst.Status = model.StatusRunning
	out := outgoing(def.Edges, waitNode)
	if len(out) == 0 {
		inst.Status = model.StatusFailed
		inst.StoppedAt = waitNode
		e.addRecord(inst, waitNode, "", "fail", model.NodeFailed, "等待节点没有后继边")
		return
	}
	edge := out[0]
	e.addRecord(inst, edge.To, edge.ID, "traverse", model.NodeEntered, edgeLabel(edge))
	inst.CurrentNode = edge.To
	e.Run(def, inst)
}

// CheckTimeout applies the timeout policy when the deadline has passed.
// Safe to call any time; it acts at most once (a decided wait never changes).
func (e *Engine) CheckTimeout(def *model.Definition, inst *model.Instance, now time.Time) {
	e.checkTimeout(def, inst, now)
}

func (e *Engine) checkTimeout(def *model.Definition, inst *model.Instance, now time.Time) {
	if inst.Status != model.StatusWaiting || inst.Wait == nil || inst.Wait.Decided {
		return
	}
	ws := inst.Wait
	if ws.Deadline == nil || now.Before(*ws.Deadline) {
		return
	}
	node := defNodeOrMissing(def, inst.WaitNode)
	if node.TimeoutPolicy == "approve" {
		e.decideWait(inst, "timeout_approved", "到期未集齐确认，按定义到期策略视为通过")
		e.completeWait(def, inst)
		return
	}
	e.decideWait(inst, "timeout_rejected", "到期未集齐确认，按定义到期策略视为拒绝")
	e.addRecord(inst, inst.WaitNode, "", "fail", model.NodeFailed, "协作等待超时未通过")
	e.startCompensation(def, inst, node)
}

// ScopeRequest modifies the acceptance scope of a live collaborative
// wait. Old authorizations are always retained in history.
type ScopeRequest struct {
	Participants      []string          `json:"participants,omitempty"` // nil keeps current list
	AllowDelegate     *bool             `json:"allowDelegate,omitempty"`
	GrantDelegations  map[string]string `json:"grantDelegations,omitempty"`
	RevokeDelegations []string          `json:"revokeDelegations,omitempty"` // participant ids
	KeepPriorInputs   bool              `json:"keepPriorInputs"`
	Reason            string            `json:"reason,omitempty"`
}

// UpdateWaitScope applies a scope change as a new authorization version.
func (e *Engine) UpdateWaitScope(def *model.Definition, inst *model.Instance, req ScopeRequest) error {
	if inst.Status != model.StatusWaiting || inst.Wait == nil {
		return fmt.Errorf("实例当前不在协作等待中，无法修改接收范围")
	}
	// Timeout due at the same moment wins: only one decision may form.
	e.checkTimeout(def, inst, time.Now().UTC())
	ws := inst.Wait
	if ws.Decided {
		return fmt.Errorf("等待步骤已有决定（%s），接收范围不可再修改", ws.Decision)
	}
	prev := currentAuth(ws)
	next := model.WaitAuthorization{
		ID:              fmt.Sprintf("auth-%d", len(ws.Authorizations)+1),
		Participants:    append([]string(nil), prev.Participants...),
		AllowDelegate:   prev.AllowDelegate,
		Delegations:     map[string]string{},
		Since:           time.Now().UTC(),
		Reason:          req.Reason,
		PriorInputsKept: req.KeepPriorInputs,
	}
	for k, v := range prev.Delegations {
		next.Delegations[k] = v
	}
	if req.Participants != nil {
		next.Participants = append([]string(nil), req.Participants...)
	}
	if req.AllowDelegate != nil {
		next.AllowDelegate = *req.AllowDelegate
	}
	if !next.AllowDelegate {
		next.Delegations = map[string]string{}
	}
	for _, p := range req.RevokeDelegations {
		delete(next.Delegations, p)
	}
	for p, d := range req.GrantDelegations {
		if !next.AllowDelegate {
			return fmt.Errorf("当前范围不允许委托，请先开启 allowDelegate")
		}
		next.Delegations[p] = d
	}
	// Prune delegations of participants no longer in scope.
	inScope := map[string]bool{}
	for _, p := range next.Participants {
		inScope[p] = true
	}
	for p := range next.Delegations {
		if !inScope[p] {
			delete(next.Delegations, p)
		}
	}

	kept, dropped := []string{}, []string{}
	newEffective := map[string]bool{}
	for _, s := range effectiveSubmitters(next) {
		newEffective[s] = true
	}
	for from := range ws.Received {
		switch {
		case !req.KeepPriorInputs:
			dropped = append(dropped, from)
		case newEffective[from]:
			kept = append(kept, from)
		default:
			dropped = append(dropped, from)
		}
	}
	if !req.KeepPriorInputs {
		ws.Received = map[string]model.PartyInput{}
	} else {
		for _, from := range dropped {
			delete(ws.Received, from)
		}
	}
	sort.Strings(kept)
	sort.Strings(dropped)
	ws.Authorizations = append(ws.Authorizations, next)
	ws.AuthID = next.ID

	validity := "此前输入全部失效，仅作为历史保留"
	if req.KeepPriorInputs {
		if len(dropped) == 0 {
			validity = "此前输入仍然有效"
		} else {
			validity = fmt.Sprintf("此前输入中 %s 仍有效；%s 因不在新范围而失效（仅作历史保留）",
				strings.Join(kept, "、"), strings.Join(dropped, "、"))
		}
	}
	e.addRecord(inst, inst.WaitNode, "", "wait_scope_changed", model.NodeEntered,
		fmt.Sprintf("接收范围变更为 %s（参与方 %s；委托%s）；%s；原授权记录 %s 保留",
			next.ID, strings.Join(next.Participants, "、"),
			map[bool]string{true: "允许", false: "不允许"}[next.AllowDelegate], validity, prev.ID))
	return nil
}
