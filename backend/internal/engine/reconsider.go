package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"flowplatform/internal/model"
)

// Reconsiderations: after a collaborative decision has passed and follow-up
// steps advanced, participants may discover the decision relied on wrong
// materials and open a reconsideration linked to that decision. The
// original approvals, delegations and authorization scope stay untouched;
// the new scope and materials only affect this round. Each round forms
// exactly one decision (upheld / rejected / expired); votes from the
// original wait or earlier rounds never count toward a new round, and no
// business action is re-triggered while compensation runs.

// ReconsiderRequest opens a review round against a passed decision.
type ReconsiderRequest struct {
	WaitNode         string            `json:"waitNode"`
	Participants     []string          `json:"participants"`
	AllowDelegate    bool              `json:"allowDelegate"`
	GrantDelegations map[string]string `json:"grantDelegations,omitempty"`
	Materials        map[string]any    `json:"materials,omitempty"`
	Reason           string            `json:"reason,omitempty"`
	TimeoutSeconds   int64             `json:"timeoutSeconds,omitempty"`
	TimeoutPolicy    string            `json:"timeoutPolicy,omitempty"` // "uphold" | default reject
}

// OpenReconsideration starts a review round against a passed collaborative
// decision. History is never rewritten: the original decision record,
// authorizations and inputs remain exactly as they were.
func (e *Engine) OpenReconsideration(def *model.Definition, inst *model.Instance, req ReconsiderRequest) (*model.Reconsideration, error) {
	if inst.Compensating {
		return nil, fmt.Errorf("实例正在补偿中，待补偿停下后才能发起复议")
	}
	if inst.Reconsidering {
		return nil, fmt.Errorf("已有进行中的复议（%s），同一时刻只能有一轮", openReconsideration(inst).ID)
	}
	var origin *model.WaitDecision
	for i := range inst.WaitDecisions {
		d := &inst.WaitDecisions[i]
		if d.NodeID == req.WaitNode {
			origin = d // last decision recorded for this node
		}
	}
	if origin == nil {
		return nil, fmt.Errorf("节点 %s 没有已形成的协作决定，无法复议", req.WaitNode)
	}
	if origin.Decision != "approved" && origin.Decision != "timeout_approved" {
		return nil, fmt.Errorf("原决定为 %s，未通过的等待无需复议（已按失败处理）", origin.Decision)
	}
	for _, prev := range inst.Reconsiderations {
		if prev.WaitNode == req.WaitNode && prev.Decided &&
			(prev.Decision == "upheld" || prev.Decision == "expired_upheld") {
			return nil, fmt.Errorf("原决定已被复议 %s 撤销并回滚，不能对同一决定重复复议", prev.ID)
		}
	}
	if len(req.Participants) == 0 {
		return nil, fmt.Errorf("复议必须指明本轮参与方（可与原范围不同）")
	}
	now := time.Now().UTC()
	rec := &model.Reconsideration{
		ID:             fmt.Sprintf("rec-%d", len(inst.Reconsiderations)+1),
		WaitNode:       req.WaitNode,
		OriginDecision: origin.Decision,
		OriginAuthID:   origin.AuthID,
		OriginSeq:      origin.Seq,
		Participants:   append([]string(nil), req.Participants...),
		AllowDelegate:  req.AllowDelegate,
		Delegations:    map[string]string{},
		Materials:      req.Materials,
		Reason:         req.Reason,
		Received:       map[string]model.PartyInput{},
		TimeoutPolicy:  req.TimeoutPolicy,
		CreatedAt:      now,
	}
	if rec.AllowDelegate {
		for p, d := range req.GrantDelegations {
			rec.Delegations[p] = d
		}
	}
	if req.TimeoutSeconds > 0 {
		dl := now.Add(time.Duration(req.TimeoutSeconds) * time.Second)
		rec.Deadline = &dl
	}
	inst.Reconsiderations = append(inst.Reconsiderations, rec)
	inst.Reconsidering = true
	matKeys := make([]string, 0, len(rec.Materials))
	for k := range rec.Materials {
		matKeys = append(matKeys, k)
	}
	sort.Strings(matKeys)
	e.addRecord(inst, req.WaitNode, "", "reconsider_opened", model.NodeEntered,
		fmt.Sprintf("对原决定（%s，授权范围 %s）发起复议 %s：本轮参与方 %s，委托%s，新资料项 [%s]；原赞成、委托与生效范围保持不变，复议期间后续动作暂停",
			origin.Decision, origin.AuthID, rec.ID, strings.Join(rec.Participants, "、"),
			map[bool]string{true: "允许", false: "不允许"}[rec.AllowDelegate], strings.Join(matKeys, "、")))
	return rec, nil
}

func openReconsideration(inst *model.Instance) *model.Reconsideration {
	for _, rec := range inst.Reconsiderations {
		if !rec.Decided {
			return rec
		}
	}
	return nil
}

// FindReconsideration returns a round by id.
func FindReconsideration(inst *model.Instance, id string) *model.Reconsideration {
	for _, rec := range inst.Reconsiderations {
		if rec.ID == id {
			return rec
		}
	}
	return nil
}

func recEffectiveSubmitters(rec *model.Reconsideration) map[string]string {
	out := map[string]string{}
	for _, p := range rec.Participants {
		if rec.AllowDelegate {
			if d, ok := rec.Delegations[p]; ok && d != "" {
				out[p] = d
				continue
			}
		}
		out[p] = p
	}
	return out
}

// recVoteOpinion extracts the explicit opinion of a reconsideration vote:
// "uphold" (support the review, overturn the original decision) or
// "reject" (oppose the review, keep the original decision). Anything else
// means the vote did not state a recognizable opinion.
func recVoteOpinion(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	v, ok := payload["opinion"].(string)
	if !ok {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "uphold", "support", "approve":
		return "uphold"
	case "reject", "oppose":
		return "reject"
	}
	return ""
}

// recTally counts, among the currently effective submitters, how many
// received votes support vs oppose the review.
func recTally(rec *model.Reconsideration) (support, oppose, done, total int) {
	effective := recEffectiveSubmitters(rec)
	total = len(rec.Participants)
	for _, sub := range effective {
		pi, ok := rec.Received[sub]
		if !ok {
			continue
		}
		done++
		if recVoteOpinion(pi.Payload) == "reject" {
			oppose++
		} else {
			support++
		}
	}
	return
}

// decideByVotes forms the round's decision from the actual opinions: the
// review passes only when every effective participant explicitly supports
// overturning the original decision; any explicit opposition rejects it.
func (e *Engine) decideReconsiderationByVotes(def *model.Definition, inst *model.Instance, rec *model.Reconsideration) {
	support, oppose, _, total := recTally(rec)
	if oppose > 0 {
		e.decideReconsideration(def, inst, rec, "rejected",
			fmt.Sprintf("本轮 %d 方意见已集齐：%d 方支持复议、%d 方反对；存在反对意见，复议不成立，原决定继续有效", total, support, oppose))
		return
	}
	e.decideReconsideration(def, inst, rec, "upheld",
		fmt.Sprintf("本轮 %d 方一致支持复议，确认原决定依据有误", total))
}

// DeliverReconsiderVote records one party's vote on an open round. Votes
// and materials of this round never merge into instance data, so history
// cannot be rewritten to look as if the original decision never passed.
func (e *Engine) DeliverReconsiderVote(def *model.Definition, inst *model.Instance, recID string, input model.ExternalInput) error {
	rec := FindReconsideration(inst, recID)
	if rec == nil {
		return fmt.Errorf("复议 %s 不存在", recID)
	}
	// A due expiry wins the race against any simultaneous vote.
	e.CheckReconsiderTimeout(def, inst, time.Now().UTC())
	if rec.Decided {
		e.addRecord(inst, rec.WaitNode, "", "late_input", model.NodeEntered,
			fmt.Sprintf("迟到的复议意见 %s 已忽略：复议 %s 已有决定（%s），仅作为历史保留", input.Key, rec.ID, rec.Decision))
		return nil
	}
	if input.Key == "" {
		return fmt.Errorf("缺少幂等键 key")
	}
	if inst.ConsumedInputs[input.Key] {
		// Keys already used by the original wait or earlier rounds can never
		// be replayed to make up numbers for this round.
		e.addRecord(inst, rec.WaitNode, "", "duplicate_input", model.NodeEntered,
			"重复的输入 "+input.Key+" 已忽略（旧投票不能凑新一轮人数）")
		return nil
	}
	effective := recEffectiveSubmitters(rec)
	allowed := map[string]bool{}
	for _, sub := range effective {
		allowed[sub] = true
	}
	if input.From == "" || !allowed[input.From] {
		e.addRecord(inst, rec.WaitNode, "", "input_rejected", model.NodeEntered,
			fmt.Sprintf("参与方 %s 不在复议 %s 的本轮范围内，意见 %s 未生效", input.From, rec.ID, input.Key))
		return nil
	}
	if _, ok := rec.Received[input.From]; ok {
		e.addRecord(inst, rec.WaitNode, "", "duplicate_input", model.NodeEntered,
			fmt.Sprintf("参与方 %s 在本轮已表达过意见，重复输入 %s 忽略", input.From, input.Key))
		return nil
	}
	opinion := recVoteOpinion(input.Payload)
	if opinion == "" {
		// An opinion-less vote must never be read as approval.
		return fmt.Errorf("复议意见必须明确表态：payload.opinion 取 \"uphold\"（支持复议、撤销原决定）或 \"reject\"（反对复议、维持原决定）")
	}
	inst.ConsumedInputs[input.Key] = true
	rec.Received[input.From] = model.PartyInput{
		Key: input.Key, From: input.From, Payload: input.Payload, AuthID: rec.ID, At: time.Now().UTC(),
	}
	support, oppose, done, total := recTally(rec)
	opinionLabel := map[string]string{"uphold": "支持复议", "reject": "反对复议"}[opinion]
	e.addRecord(inst, rec.WaitNode, "", "reconsider_vote", model.NodeEntered,
		fmt.Sprintf("复议 %s：%s 明确表态【%s】（已收到 %d/%d：支持 %d、反对 %d）；未集齐前不形成决定",
			rec.ID, input.From, opinionLabel, done, total, support, oppose))
	if done < total {
		return nil
	}
	e.decideReconsiderationByVotes(def, inst, rec)
	return nil
}

// decideReconsideration forms the single terminal decision of a round and
// applies its outcome. Called at most once per round.
func (e *Engine) decideReconsideration(def *model.Definition, inst *model.Instance, rec *model.Reconsideration, decision, why string) {
	if rec.Decided {
		return
	}
	now := time.Now().UTC()
	rec.Decided = true
	rec.Decision = decision
	rec.DecidedAt = &now
	inst.Reconsidering = false
	e.addRecord(inst, rec.WaitNode, "", "reconsider_decided", model.NodeSucceeded,
		fmt.Sprintf("复议 %s 决定：%s（%s；针对原决定 %s / 授权范围 %s）", rec.ID, decision, why, rec.OriginDecision, rec.OriginAuthID))
	switch decision {
	case "upheld", "expired_upheld":
		e.applyUpheld(def, inst, rec)
	default: // rejected / expired_rejected: original decision stands
		retained := uncompensatedAfter(inst, rec.OriginSeq)
		kept := "无"
		if len(retained) > 0 {
			kept = strings.Join(retained, "、")
		}
		e.addRecord(inst, rec.WaitNode, "", "reconsider_closed", model.NodeEntered,
			fmt.Sprintf("复议不成立，原决定继续有效；已生效动作（%s）全部保留、不是补偿待办；被暂停的后续动作恢复可推进状态", kept))
	}
}

// applyUpheld rolls back the follow-up actions executed after the original
// decision, scoped so anything done before it is never touched. Already
// executed steps can only be compensated per process; compensation failure
// keeps the remaining scope visible instead of pretending a full restore.
func (e *Engine) applyUpheld(def *model.Definition, inst *model.Instance, rec *model.Reconsideration) {
	// A later wait point paused by the review is abandoned: the flow rolls
	// back to the original decision point.
	inst.WaitNode = ""
	inst.Wait = nil
	inst.CompensateAfterSeq = rec.OriginSeq
	remaining := uncompensatedAfter(inst, rec.OriginSeq)
	if len(remaining) == 0 {
		e.addRecord(inst, rec.WaitNode, "", "reconsider_compensate_none", model.NodeEntered,
			"复议通过：原决定之后的后续动作均已补偿或从未执行，无需再补偿")
		if inst.Status != model.StatusFailed {
			inst.Status = model.StatusFailed
			inst.StoppedAt = rec.WaitNode
		}
		return
	}
	e.addRecord(inst, rec.WaitNode, "", "reconsider_compensate_start", model.NodeCompensating,
		fmt.Sprintf("复议通过：按流程补偿原决定之后已执行的 %d 个动作（%s）；补偿期间原业务动作不再推进",
			len(remaining), strings.Join(remaining, "、")))
	inst.Compensating = true
	inst.FailedNode = rec.WaitNode
	if inst.CompensatedNodes == nil {
		inst.CompensatedNodes = map[string]bool{}
	}
	e.continueCompensation(def, inst, model.Node{ID: rec.WaitNode})
	if left := uncompensatedAfter(inst, rec.OriginSeq); len(left) > 0 {
		e.addRecord(inst, rec.WaitNode, "", "reconsider_compensation_incomplete", model.NodeCompFailed,
			fmt.Sprintf("补偿未完成范围保留：%s；复议结束不代表全部恢复，重启/重新打开后可继续核对", strings.Join(left, "、")))
	}
}

// uncompensatedAfter lists succeeded task nodes newer than seq that have
// not been compensated yet, in execution order.
func uncompensatedAfter(inst *model.Instance, seq int) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range inst.Records {
		if r.Action == "succeed" && r.Seq > seq && !seen[r.NodeID] {
			seen[r.NodeID] = true
			if !inst.CompensatedNodes[r.NodeID] {
				out = append(out, r.NodeID)
			}
		}
	}
	return out
}

// ReconsiderScopeRequest changes the participation scope of an open round.
// Votes already cast in this round stay recorded; those falling outside the
// new scope stop counting toward the decision but remain as history.
type ReconsiderScopeRequest struct {
	Participants      []string          `json:"participants,omitempty"`
	GrantDelegations  map[string]string `json:"grantDelegations,omitempty"`
	RevokeDelegations []string          `json:"revokeDelegations,omitempty"`
	Reason            string            `json:"reason,omitempty"`
}

// UpdateReconsiderScope applies a scope change to an open round. It never
// touches the original wait's authorizations.
func (e *Engine) UpdateReconsiderScope(def *model.Definition, inst *model.Instance, recID string, req ReconsiderScopeRequest) error {
	rec := FindReconsideration(inst, recID)
	if rec == nil {
		return fmt.Errorf("复议 %s 不存在", recID)
	}
	e.CheckReconsiderTimeout(def, inst, time.Now().UTC())
	if rec.Decided {
		return fmt.Errorf("复议 %s 已有决定（%s），范围不可再修改", recID, rec.Decision)
	}
	if req.Participants != nil {
		if len(req.Participants) == 0 {
			return fmt.Errorf("参与方不能为空")
		}
		rec.Participants = append([]string(nil), req.Participants...)
	}
	inScope := map[string]bool{}
	for _, p := range rec.Participants {
		inScope[p] = true
	}
	for _, p := range req.RevokeDelegations {
		delete(rec.Delegations, p)
	}
	for p, d := range req.GrantDelegations {
		if !rec.AllowDelegate {
			return fmt.Errorf("本轮复议不允许委托")
		}
		if !inScope[p] {
			return fmt.Errorf("参与方 %s 不在本轮范围内，无法授予委托", p)
		}
		rec.Delegations[p] = d
	}
	for p := range rec.Delegations {
		if !inScope[p] {
			delete(rec.Delegations, p)
		}
	}
	effective := recEffectiveSubmitters(rec)
	counting, dropped := []string{}, []string{}
	for from := range rec.Received {
		ok := false
		for _, sub := range effective {
			if sub == from {
				ok = true
			}
		}
		if ok {
			counting = append(counting, from)
		} else {
			dropped = append(dropped, from)
		}
	}
	sort.Strings(counting)
	sort.Strings(dropped)
	e.addRecord(inst, rec.WaitNode, "", "reconsider_scope_changed", model.NodeEntered,
		fmt.Sprintf("复议 %s 范围变更（参与方 %s；委托 %v）；仍计入的意见：[%s]；不再计入（仅作历史保留）：[%s]；原决定的授权范围不受影响",
			rec.ID, strings.Join(rec.Participants, "、"), rec.Delegations,
			strings.Join(counting, "、"), strings.Join(dropped, "、")))
	// The new scope may already be fully confirmed.
	_, _, done, total := recTally(rec)
	if done >= total && total > 0 {
		e.decideReconsiderationByVotes(def, inst, rec)
	}
	return nil
}

// CheckReconsiderTimeout applies the round's expiry policy. The decision
// forms at most once; calling again afterwards is a no-op.
func (e *Engine) CheckReconsiderTimeout(def *model.Definition, inst *model.Instance, now time.Time) {
	rec := openReconsideration(inst)
	if rec == nil || rec.Deadline == nil || now.Before(*rec.Deadline) {
		return
	}
	if rec.TimeoutPolicy == "uphold" {
		e.decideReconsideration(def, inst, rec, "expired_upheld", "复议到期未集齐意见，按策略视为复议通过")
		return
	}
	e.decideReconsideration(def, inst, rec, "expired_rejected", "复议到期未集齐意见，按策略视为复议不成立")
}

// ReconsiderProgress explains, while a review is open (and after it), how
// far the original decision had advanced: which follow-up actions already
// executed and can only be compensated, and which are still pausable.
type ReconsiderProgress struct {
	ReconsiderationID string `json:"reconsiderationId"`
	OriginWaitNode    string `json:"originWaitNode"`
	OriginDecision    string `json:"originDecision"`
	OriginAuthID      string `json:"originAuthId"`
	InstanceStatus    string `json:"instanceStatus"`
	Open              bool   `json:"open"`
	Decision          string `json:"decision,omitempty"`
	// ExecutedAfterDecision: follow-up actions already executed; compensate-only.
	ExecutedAfterDecision []string `json:"executedAfterDecision"`
	// PausableAhead: not executed yet; paused while the round is open.
	PausableAhead         []string `json:"pausableAhead"`
	Compensated           []string `json:"compensated,omitempty"`
	CompensationRemaining []string `json:"compensationRemaining,omitempty"`
	// RetainedActions lists follow-up actions that stay in effect because
	// the round did not overturn the original decision (rejected /
	// expired_rejected), or actions currently effective while the round is
	// still open. They are never compensation to-dos.
	RetainedActions []string `json:"retainedActions,omitempty"`
}

// ProgressReconsideration computes the progress view for one round.
func (e *Engine) ProgressReconsideration(def *model.Definition, inst *model.Instance, rec *model.Reconsideration) *ReconsiderProgress {
	p := &ReconsiderProgress{
		ReconsiderationID: rec.ID,
		OriginWaitNode:    rec.WaitNode,
		OriginDecision:    rec.OriginDecision,
		OriginAuthID:      rec.OriginAuthID,
		InstanceStatus:    string(inst.Status),
		Open:              !rec.Decided,
		Decision:          rec.Decision,
	}
	executed := map[string]bool{}
	var executedOrder []string
	for _, r := range inst.Records {
		if r.Action == "succeed" && r.Seq > rec.OriginSeq && !executed[r.NodeID] {
			executed[r.NodeID] = true
			executedOrder = append(executedOrder, r.NodeID)
		}
	}
	p.ExecutedAfterDecision = executedOrder
	switch {
	case !rec.Decided:
		// Round open: the executed follow-ups are still in effect; they can
		// only be compensated if the round later passes.
		p.RetainedActions = append(p.RetainedActions, executedOrder...)
	case rec.Decision == "upheld" || rec.Decision == "expired_upheld":
		// Overturned: uncompensated follow-ups are the only to-dos.
		for _, id := range executedOrder {
			if inst.CompensatedNodes[id] {
				p.Compensated = append(p.Compensated, id)
			} else {
				p.CompensationRemaining = append(p.CompensationRemaining, id)
			}
		}
	default:
		// rejected / expired_rejected: original decision stands, so every
		// executed follow-up remains in effect; nothing waits for
		// compensation and nothing is paused any longer.
		p.RetainedActions = append(p.RetainedActions, executedOrder...)
	}
	if !rec.Decided {
		// Pausable: nodes reachable from the current stop that never executed.
		from := inst.WaitNode
		if from == "" {
			from = effectiveStop(inst)
		}
		for id := range reachableFrom(def, from) {
			if !executed[id] && id != rec.WaitNode {
				p.PausableAhead = append(p.PausableAhead, id)
			}
		}
		sort.Strings(p.PausableAhead)
	}
	return p
}
