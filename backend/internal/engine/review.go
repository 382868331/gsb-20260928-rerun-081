package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"flowplatform/internal/model"
)

// Reconsideration (复议): after a collaborative wait produced a decision and
// follow-up steps advanced, participants may discover wrong materials were
// used. A review round is attached to the original decision; the original
// approvals, delegations and effective scope stay untouched in history, and
// the review's own participation scope and materials only affect this round.
// Exactly one review decision (approved / rejected / expired) can ever form.

// OpenReviewRequest starts a reconsideration round on a decided wait.
type OpenReviewRequest struct {
	Participants   []string       `json:"participants"`
	AllowDelegate  bool           `json:"allowDelegate,omitempty"`
	Materials      map[string]any `json:"materials,omitempty"` // 复议依据资料
	Reason         string         `json:"reason,omitempty"`
	TimeoutSeconds int64          `json:"timeoutSeconds,omitempty"`
}

// OpenReview attaches a new review round to the instance's decided
// collaborative wait. The original decision is never rewritten.
func (e *Engine) OpenReview(def *model.Definition, inst *model.Instance, req OpenReviewRequest) error {
	decided := decidedCollabWait(inst)
	if decided == nil {
		return fmt.Errorf("实例没有已决定的协作等待，无法发起复议")
	}
	if inst.ActiveReview() != nil {
		return fmt.Errorf("已存在进行中的复议（%s），待其形成决定后才能再次发起", inst.ActiveReview().ID)
	}
	if len(req.Participants) == 0 {
		return fmt.Errorf("复议需要指明新的参与范围 participants")
	}
	waitNode := decided.NodeID
	origSeq := 0
	for _, rec := range inst.Records {
		if rec.Action == "wait_decided" && rec.NodeID == waitNode {
			origSeq = rec.Seq
		}
	}
	now := time.Now().UTC()
	auth := model.WaitAuthorization{
		ID:            "review-auth-1",
		Participants:  append([]string(nil), req.Participants...),
		AllowDelegate: req.AllowDelegate,
		Delegations:   map[string]string{},
		Since:         now,
		Reason:        "复议发起时设定的参与范围（不影响原决定的授权记录）",
	}
	rv := &model.ReviewState{
		ID:             fmt.Sprintf("review-%d", len(inst.Reviews)+1),
		WaitNode:       waitNode,
		OrigDecision:   decided.Decision,
		OrigAuthID:     decided.DecisionAuthID,
		OrigDecideSeq:  origSeq,
		Materials:      req.Materials,
		Reason:         req.Reason,
		AuthID:         auth.ID,
		Received:       map[string]model.PartyInput{},
		Authorizations: []model.WaitAuthorization{auth},
		ConsumedKeys:   map[string]bool{},
		OpenedAt:       now,
	}
	if req.TimeoutSeconds > 0 {
		dl := now.Add(time.Duration(req.TimeoutSeconds) * time.Second)
		rv.Deadline = &dl
	}
	inst.Reviews = append(inst.Reviews, rv)
	e.addRecord(inst, waitNode, "", "review_opened", model.NodeEntered,
		fmt.Sprintf("对原决定（%s，依据授权范围 %s）发起复议 %s：新参与范围 %s；原赞成、委托与生效范围保持不变，复议资料仅影响本轮",
			rv.OrigDecision, rv.OrigAuthID, rv.ID, strings.Join(req.Participants, "、")))
	return nil
}

func currentReviewAuth(rv *model.ReviewState) model.WaitAuthorization {
	return rv.Authorizations[len(rv.Authorizations)-1]
}

// decidedCollabWait finds the most recent decided collaborative wait: the
// live one when still current, otherwise the archived history.
func decidedCollabWait(inst *model.Instance) *model.WaitState {
	if inst.Wait != nil && inst.Wait.Decided {
		return inst.Wait
	}
	if n := len(inst.WaitHistory); n > 0 && inst.WaitHistory[n-1].Decided {
		return inst.WaitHistory[n-1]
	}
	return nil
}

// DeliverReviewInput records one review opinion. Review keys live in their
// own namespace: a key already consumed by the original business wait is a
// late old vote and can never count toward the review quorum.
func (e *Engine) DeliverReviewInput(def *model.Definition, inst *model.Instance, input model.ExternalInput) error {
	rv := inst.ActiveReview()
	if rv == nil {
		if last := inst.LastReview(); last != nil {
			e.addRecord(inst, last.WaitNode, "", "late_input", model.NodeEntered,
				fmt.Sprintf("迟到的复议意见 %s 已忽略：复议 %s 已有决定（%s），仅作为历史保留", input.Key, last.ID, last.Decision))
			return nil
		}
		return fmt.Errorf("当前没有进行中的复议")
	}
	// A due expiry wins the race against any simultaneous opinion/scope change.
	e.checkReviewTimeout(def, inst, time.Now().UTC())
	if rv.Decided {
		e.addRecord(inst, rv.WaitNode, "", "late_input", model.NodeEntered,
			fmt.Sprintf("迟到的复议意见 %s 已忽略：复议 %s 已有决定（%s）", input.Key, rv.ID, rv.Decision))
		return nil
	}
	if rv.ConsumedKeys[input.Key] {
		e.addRecord(inst, rv.WaitNode, "", "duplicate_input", model.NodeEntered,
			"重复的复议意见 "+input.Key+" 已忽略，复议计票未重复")
		return nil
	}
	if inst.ConsumedInputs[input.Key] {
		e.addRecord(inst, rv.WaitNode, "", "review_old_vote_rejected", model.NodeEntered,
			fmt.Sprintf("复议意见 %s 已忽略：该 key 是原业务等待已生效的旧投票，不能拿来凑复议人数", input.Key))
		return nil
	}
	auth := currentReviewAuth(rv)
	if !auth.AllowDelegate && len(auth.Delegations) > 0 {
		auth.Delegations = map[string]string{}
	}
	effective := effectiveSubmitters(auth)
	allowed := map[string]bool{}
	for _, sub := range effective {
		allowed[sub] = true
	}
	if input.From == "" {
		return fmt.Errorf("复议意见需要指明参与方 from（当前有效：%s）", strings.Join(sortedSubmitters(effective), "、"))
	}
	if !allowed[input.From] {
		e.addRecord(inst, rv.WaitNode, "", "input_rejected", model.NodeEntered,
			fmt.Sprintf("参与方 %s 不在复议 %s 的当前有效范围（%s）内，意见 %s 未生效",
				input.From, rv.ID, strings.Join(sortedSubmitters(effective), "、"), input.Key))
		return nil
	}
	if _, ok := rv.Received[input.From]; ok {
		e.addRecord(inst, rv.WaitNode, "", "duplicate_input", model.NodeEntered,
			fmt.Sprintf("参与方 %s 在本轮复议已表态，重复意见 %s 忽略", input.From, input.Key))
		return nil
	}
	rv.ConsumedKeys[input.Key] = true
	rv.Received[input.From] = model.PartyInput{
		Key: input.Key, From: input.From, Payload: input.Payload, AuthID: auth.ID, At: time.Now().UTC(),
	}
	total := len(auth.Participants)
	done := 0
	for _, sub := range effective {
		if _, ok := rv.Received[sub]; ok {
			done++
		}
	}
	e.addRecord(inst, rv.WaitNode, "", "review_input_received", model.NodeSucceeded,
		fmt.Sprintf("复议 %s：参与方 %s 已表态（%d/%d，复议范围 %s）；未集齐前复议不形成决定", rv.ID, input.From, done, total, auth.ID))
	if done < total {
		return nil
	}
	// Quorum reached: any explicit reject vote (payload vote="reject") turns
	// the review down; otherwise the review passes. One decision only.
	decision := "approved"
	for _, pi := range rv.Received {
		if v, _ := pi.Payload["vote"].(string); v == "reject" {
			decision = "rejected"
			break
		}
	}
	e.decideReview(def, inst, rv, decision, fmt.Sprintf("复议参与方全部 %d 方已表态", total))
	return nil
}

// decideReview forms the single terminal decision of a review round.
func (e *Engine) decideReview(def *model.Definition, inst *model.Instance, rv *model.ReviewState, decision, why string) {
	now := time.Now().UTC()
	rv.Decided = true
	rv.Decision = decision
	rv.DecisionAuthID = rv.AuthID
	rv.DecidedAt = &now
	auth := currentReviewAuth(rv)
	e.addRecord(inst, rv.WaitNode, "", "review_decided", model.NodeSucceeded,
		fmt.Sprintf("复议决定：%s（%s；依据复议范围 %s：参与方 %s；复议资料仅用于本轮，不改写原决定历史）",
			decision, why, auth.ID, strings.Join(auth.Participants, "、")))
	if decision == "approved" {
		// 复议通过：推翻原决定的后续效果。已执行的后续动作按流程补偿，
		// 尚未执行的动作不再推进；原决定记录本身保持原样。
		e.runReviewCompensation(def, inst, rv)
	}
}

// runReviewCompensation rolls back exactly the business actions taken after
// the original decision (record seq > OrigDecideSeq). Anything before the
// original decision stays untouched. Compensation failures keep the true
// stopping point and the unfinished scope; the review ending never implies
// everything was restored.
func (e *Engine) runReviewCompensation(def *model.Definition, inst *model.Instance, rv *model.ReviewState) {
	if inst.Status == model.StatusWaiting {
		// A later wait is still pending: it is one of the follow-up actions
		// that can no longer proceed under the overturned decision.
		e.addRecord(inst, inst.WaitNode, "", "wait_cancelled_by_review", model.NodeEntered,
			fmt.Sprintf("复议 %s 通过，等待节点挂起的后续动作取消，不再推进", rv.ID))
		inst.WaitNode = ""
		inst.Wait = nil
	}
	inst.Compensating = true
	inst.FailedNode = rv.WaitNode
	inst.CompSinceSeq = rv.OrigDecideSeq
	if inst.CompensatedNodes == nil {
		inst.CompensatedNodes = map[string]bool{}
	}
	e.continueCompensation(def, inst, model.Node{ID: rv.WaitNode})
}

// CheckReviewTimeout applies review expiry. Acts at most once per round.
func (e *Engine) CheckReviewTimeout(def *model.Definition, inst *model.Instance, now time.Time) {
	e.checkReviewTimeout(def, inst, now)
}

func (e *Engine) checkReviewTimeout(def *model.Definition, inst *model.Instance, now time.Time) {
	rv := inst.ActiveReview()
	if rv == nil || rv.Decided {
		return
	}
	if rv.Deadline == nil || now.Before(*rv.Deadline) {
		return
	}
	e.decideReview(def, inst, rv, "expired", "复议到期未集齐表态，复议不成立，原决定继续有效")
}

// UpdateReviewScope changes the participation scope of a live review round.
// Old review authorizations are kept; the original wait's authorizations are
// never touched.
func (e *Engine) UpdateReviewScope(def *model.Definition, inst *model.Instance, req ScopeRequest) error {
	rv := inst.ActiveReview()
	if rv == nil {
		return fmt.Errorf("当前没有进行中的复议，无法修改复议范围")
	}
	e.checkReviewTimeout(def, inst, time.Now().UTC())
	if rv.Decided {
		return fmt.Errorf("复议 %s 已有决定（%s），参与范围不可再修改", rv.ID, rv.Decision)
	}
	prev := currentReviewAuth(rv)
	next := model.WaitAuthorization{
		ID:              fmt.Sprintf("review-auth-%d", len(rv.Authorizations)+1),
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
			return fmt.Errorf("当前复议范围不允许委托，请先开启 allowDelegate")
		}
		next.Delegations[p] = d
	}
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
	for from := range rv.Received {
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
		rv.Received = map[string]model.PartyInput{}
	} else {
		for _, from := range dropped {
			delete(rv.Received, from)
		}
	}
	sort.Strings(kept)
	sort.Strings(dropped)
	rv.Authorizations = append(rv.Authorizations, next)
	rv.AuthID = next.ID

	validity := "此前复议表态全部失效，仅作为历史保留"
	if req.KeepPriorInputs {
		if len(dropped) == 0 {
			validity = "此前复议表态仍然有效"
		} else {
			validity = fmt.Sprintf("此前复议表态中 %s 仍有效；%s 因不在新范围而失效（仅作历史保留）",
				strings.Join(kept, "、"), strings.Join(dropped, "、"))
		}
	}
	e.addRecord(inst, rv.WaitNode, "", "review_scope_changed", model.NodeEntered,
		fmt.Sprintf("复议 %s 参与范围变更为 %s（参与方 %s；委托%s）；%s；原复议授权记录 %s 保留，原决定的授权历史不受影响",
			rv.ID, next.ID, strings.Join(next.Participants, "、"),
			map[bool]string{true: "允许", false: "不允许"}[next.AllowDelegate], validity, prev.ID))
	return nil
}

// ReviewProgress describes, while a review is pending (and after), how far
// the original decision had progressed: which follow-up actions already
// executed and can only be compensated, which are still pausable, and which
// compensations remain unfinished.
type ReviewProgress struct {
	ReviewID            string   `json:"reviewId"`
	WaitNode            string   `json:"waitNode"`
	ExecutedAfter       []string `json:"executedAfter"`       // succeeded task nodes after the original decision
	Compensated         []string `json:"compensated"`         // of those, already compensated
	PendingCompensation []string `json:"pendingCompensation"` // of those, still not undone (unfinished scope)
	PausableAhead       []string `json:"pausableAhead"`       // not-yet-executed nodes ahead of the current position
}

// Progress computes the review progress view from real records only.
func (e *Engine) ReviewProgress(def *model.Definition, inst *model.Instance, rv *model.ReviewState) *ReviewProgress {
	p := &ReviewProgress{ReviewID: rv.ID, WaitNode: rv.WaitNode}
	nodeType := map[string]model.NodeType{}
	for _, n := range def.Nodes {
		nodeType[n.ID] = n.Type
	}
	seen := map[string]bool{}
	for _, rec := range inst.Records {
		if rec.Seq <= rv.OrigDecideSeq || rec.Action != "succeed" || nodeType[rec.NodeID] != model.NodeTask {
			continue
		}
		if !seen[rec.NodeID] {
			seen[rec.NodeID] = true
			p.ExecutedAfter = append(p.ExecutedAfter, rec.NodeID)
		}
	}
	for _, id := range p.ExecutedAfter {
		if inst.CompensatedNodes[id] {
			p.Compensated = append(p.Compensated, id)
		} else {
			p.PendingCompensation = append(p.PendingCompensation, id)
		}
	}
	// Pausable: nodes reachable from the current position that never executed.
	executed := map[string]bool{}
	for _, rec := range inst.Records {
		if rec.Action == "succeed" || rec.Action == "enter" || rec.Action == "wait" {
			executed[rec.NodeID] = true
		}
	}
	from := inst.CurrentNode
	if inst.WaitNode != "" {
		from = inst.WaitNode
	}
	if from != "" && (inst.Status == model.StatusWaiting || inst.Status == model.StatusRunning) {
		for id := range reachableFrom(def, from) {
			if !executed[id] && nodeType[id] != "" {
				p.PausableAhead = append(p.PausableAhead, id)
			}
		}
		sort.Strings(p.PausableAhead)
	}
	return p
}
