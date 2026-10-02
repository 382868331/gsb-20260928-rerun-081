package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"flowplatform/internal/model"
)

// initCollab creates the runtime state of a collaborative wait the first
// time the instance reaches it. Re-entering after a restart is a no-op.
func (e *Engine) initCollab(inst *model.Instance, node model.Node) *model.CollabState {
	if inst.Collabs == nil {
		inst.Collabs = map[string]*model.CollabState{}
	}
	if st, ok := inst.Collabs[node.ID]; ok {
		return st
	}
	spec := node.Collab
	scope := append([]string{}, spec.Participants...)
	sort.Strings(scope)
	st := &model.CollabState{
		WaitNode: node.ID,
		Scope:    scope,
		Votes:    []model.CollabVote{},
		Grants:   []model.GrantRecord{},
	}
	if spec.DeadlineSeconds > 0 {
		dl := time.Now().UTC().Add(time.Duration(spec.DeadlineSeconds) * time.Second)
		st.Deadline = &dl
	}
	inst.Collabs[node.ID] = st
	return st
}

func collabNode(def *model.Definition, nodeID string) (model.Node, *model.CollabSpec) {
	for _, n := range def.Nodes {
		if n.ID == nodeID {
			return n, n.Collab
		}
	}
	return model.Node{}, nil
}

func collabState(inst *model.Instance, nodeID string) *model.CollabState {
	if inst.Collabs == nil {
		return nil
	}
	return inst.Collabs[nodeID]
}

func collabHasKey(st *model.CollabState, key string) bool {
	for _, v := range st.Votes {
		if v.Key == key {
			return true
		}
	}
	return false
}

// voteAuthorization resolves whether a voter may act at this moment and on
// whose behalf. Delegation is only permitted when the spec allows it and an
// un-revoked grant exists.
func voteAuthorization(st *model.CollabState, spec *model.CollabSpec, voter string) (onBehalf string, grantSeq int, ok bool) {
	if inScope(st.Scope, voter) {
		return voter, 0, true
	}
	if !spec.Delegatable {
		return "", 0, false
	}
	// Find the latest action for each (actor,party) pair; a delegate may vote
	// if the latest record is a delegation that has not since been revoked.
	latest := map[string]model.GrantAction{}
	var seqOf map[string]int
	for _, g := range st.Grants {
		key := g.Actor + "\x00" + g.Party
		latest[key] = g.Action
		if seqOf == nil {
			seqOf = map[string]int{}
		}
		seqOf[key] = g.Seq
	}
	for gk, act := range latest {
		if act != model.GrantDelegate {
			continue
		}
		parts := strings.SplitN(gk, "\x00", 2)
		actor, party := parts[0], parts[1]
		if party == voter && inScope(st.Scope, actor) {
			return actor, seqOf[gk], true
		}
	}
	return "", 0, false
}

func inScope(scope []string, party string) bool {
	for _, p := range scope {
		if p == party {
			return true
		}
	}
	return false
}

func removeParty(scope []string, party string) []string {
	out := []string{}
	for _, p := range scope {
		if p != party {
			out = append(out, p)
		}
	}
	return out
}

// countedVotes are currently valid votes, at most one per participant
// (a delegate's vote counts for the original participant).
func validTallies(st *model.CollabState) (approvals, rejections int, byParticipant map[string]model.VoteKind) {
	byParticipant = map[string]model.VoteKind{}
	for _, v := range st.Votes {
		if !v.Valid {
			continue
		}
		p := v.OnBehalfOf
		if p == "" {
			p = v.Voter
		}
		byParticipant[p] = v.Kind
	}
	for _, k := range byParticipant {
		if k == model.VoteApprove {
			approvals++
		} else {
			rejections++
		}
	}
	return
}

func approveThreshold(spec *model.CollabSpec, scopeSize int) int {
	if spec.ApproveThreshold > 0 {
		return spec.ApproveThreshold
	}
	return scopeSize
}

// addGrant appends an authorization record. Both old and new records are
// retained forever; nothing in the grant history is rewritten.
func addGrant(st *model.CollabState, action model.GrantAction, actor, party string, keepPrior bool, note string) model.GrantRecord {
	g := model.GrantRecord{
		Seq:             len(st.Grants) + 1,
		Action:          action,
		Actor:           actor,
		Party:           party,
		KeepPriorInputs: keepPrior,
		Note:            note,
		At:              time.Now().UTC(),
	}
	st.Grants = append(st.Grants, g)
	return g
}

// invalidatePriorVotes marks existing valid votes invalid after a revocation
// or scope-removal whose keepPriorInputs flag is false. Votes are kept as
// history; they simply no longer count.
func invalidatePriorVotes(st *model.CollabState, affected []string, reason string) {
	for i := range st.Votes {
		v := &st.Votes[i]
		if !v.Valid {
			continue
		}
		p := v.OnBehalfOf
		if p == "" {
			p = v.Voter
		}
		if len(affected) == 0 || inScope(affected, p) || inScope(affected, v.Voter) {
			v.Valid = false
			v.InvalidReason = reason
		}
	}
}

func scopeSnapshot(scope []string) []string {
	out := append([]string{}, scope...)
	sort.Strings(out)
	return out
}

// CheckTimeouts finalizes any pending collaborative wait whose deadline has
// passed. Called on startup/recovery and before every collab operation.
func (e *Engine) CheckTimeouts(def *model.Definition, inst *model.Instance) {
	if inst.Collabs == nil {
		return
	}
	for nodeID, st := range inst.Collabs {
		if st.Decided || st.Deadline == nil {
			continue
		}
		if time.Now().UTC().Before(*st.Deadline) {
			continue
		}
		node, spec := collabNode(def, nodeID)
		if spec == nil {
			continue
		}
		e.finalizeByTimeout(inst, node, spec, st)
	}
}

func (e *Engine) finalizeByTimeout(inst *model.Instance, node model.Node, spec *model.CollabSpec, st *model.CollabState) {
	approvals, rejections, _ := validTallies(st)
	threshold := approveThreshold(spec, len(st.Scope))
	policy := spec.TimeoutPolicy
	if policy == "" {
		policy = model.TimeoutReject
	}
	approved := false
	outcome := "timeout_rejected"
	reason := fmt.Sprintf("到期（%s），按策略 %s 统计当时有效范围内的意见", st.Deadline.Format(time.RFC3339), policy)
	switch policy {
	case model.TimeoutApprove:
		approved = true
		outcome = "timeout_approved"
	case model.TimeoutMajority:
		if approvals > rejections {
			approved = true
			outcome = "timeout_approved"
		} else {
			reason += "；赞成未超过反对（含平票），按拒绝处理"
		}
	default: // reject policy: only approvals meeting threshold pass
		if approvals >= threshold {
			approved = true
			outcome = "timeout_approved"
		} else {
			reason += fmt.Sprintf("；赞成 %d 未达通过所需 %d", approvals, threshold)
		}
	}
	e.commitDecision(inst, node, spec, st, approved, outcome, reason, approvals, rejections, threshold)
}

// commitDecision records the one definitive outcome and leaves the wait.
func (e *Engine) commitDecision(inst *model.Instance, node model.Node, spec *model.CollabSpec, st *model.CollabState, approved bool, outcome, reason string, approvals, rejections, threshold int) {
	st.Decision = &model.CollabDecision{
		Outcome:       outcome,
		Reason:        reason,
		Approvals:     approvals,
		Rejections:    rejections,
		Threshold:     threshold,
		ScopeSnapshot: scopeSnapshot(st.Scope),
		At:            time.Now().UTC(),
	}
	st.Decided = true
	result := "rejected"
	if approved {
		result = "approved"
	}
	// Expose the decision to downstream conditions/data.
	inst.Data[node.ID+"_decision"] = result
	inst.Data["collab_decision"] = result
	inst.Data["collab_approved"] = approved
	e.addRecord(inst, node.ID, "", "collab_decided", model.NodeSucceeded,
		fmt.Sprintf("协作等待形成唯一决定：%s（赞成 %d / 反对 %d，需 %d；%s）；有效参与范围：%s",
			outcome, approvals, rejections, threshold, reason, strings.Join(st.Decision.ScopeSnapshot, ", ")))
}
