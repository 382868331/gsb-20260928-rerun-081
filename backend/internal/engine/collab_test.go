package engine_test

import (
	"testing"
	"time"

	"flowplatform/internal/model"
)

func collabNodes(spec *model.CollabSpec) ([]model.Node, []model.Edge) {
	nodes := []model.Node{
		{ID: "start", Type: model.NodeStart, Name: "开始"},
		{ID: "review", Type: model.NodeWait, Name: "会签", Collab: spec},
		{ID: "done", Type: model.NodeTask, Name: "执行"},
		{ID: "end", Type: model.NodeEnd, Name: "结束"},
	}
	edges := []model.Edge{
		{ID: "a", From: "start", To: "review"},
		{ID: "b", From: "review", To: "done"},
		{ID: "c", From: "done", To: "end"},
	}
	return nodes, edges
}

func vote(key, voter string, kind model.VoteKind) model.ExternalInput {
	return model.ExternalInput{WaitNode: "review", Key: key, Kind: kind, Voter: voter, Payload: map[string]any{}}
}

func TestCollabPartialVotesDoNotComplete(t *testing.T) {
	dir := t.TempDir() + "/data"
	st, eng := newTestEngine(t, dir)
	spec := &model.CollabSpec{Participants: []string{"alice", "bob", "carol"}}
	nodes, edges := collabNodes(spec)
	def, _ := st.PublishDraft("p", nodes, edges)
	inst, _ := st.CreateInstance(def, map[string]any{})
	eng.Run(def, inst)
	if inst.Status != model.StatusWaiting {
		t.Fatalf("应等待, got %s", inst.Status)
	}
	if err := eng.DeliverInput(def, inst, vote("k1", "alice", model.VoteApprove)); err != nil {
		t.Fatal(err)
	}
	if inst.Status != model.StatusWaiting {
		t.Fatalf("只有一方赞成时步骤不得提前完成, got %s", inst.Status)
	}
	if err := eng.DeliverInput(def, inst, vote("k2", "bob", model.VoteReject)); err != nil {
		t.Fatal(err)
	}
	if inst.Status != model.StatusWaiting {
		t.Fatal("两方意见相反仍属部分输入，必须继续等待")
	}
	if err := eng.DeliverInput(def, inst, vote("k3", "carol", model.VoteApprove)); err != nil {
		t.Fatal(err)
	}
	// All parties voted; unanimous threshold not met -> rejected decision, but
	// the wait still resolves and the flow leaves the wait node.
	if inst.Status != model.StatusCompleted {
		t.Fatalf("全员投票后应形成明确决定并离开等待点, got %s", inst.Status)
	}
	if got := inst.Collabs["review"].Decision.Outcome; got != "rejected" {
		t.Fatalf("2 赞成 1 反对、阈值 3 应为 rejected, got %s", got)
	}
}

func TestCollabUnanimousRejectsAndDecisionScope(t *testing.T) {
	dir := t.TempDir() + "/data"
	st, eng := newTestEngine(t, dir)
	spec := &model.CollabSpec{Participants: []string{"alice", "bob"}}
	nodes, edges := collabNodes(spec)
	def, _ := st.PublishDraft("p", nodes, edges)
	inst, _ := st.CreateInstance(def, map[string]any{})
	eng.Run(def, inst)
	_ = eng.DeliverInput(def, inst, vote("a", "alice", model.VoteApprove))
	if inst.Status != model.StatusWaiting {
		t.Fatal("一票赞成不能结束")
	}
	_ = eng.DeliverInput(def, inst, vote("b", "bob", model.VoteReject))
	if inst.Status != model.StatusCompleted {
		t.Fatalf("全员投完后应离开等待点, got %s", inst.Status)
	}
	cs := inst.Collabs["review"]
	if cs == nil || cs.Decision == nil {
		t.Fatal("应保存决定")
	}
	if cs.Decision.Outcome != "rejected" || cs.Decision.Threshold != 2 {
		t.Fatalf("一致同意阈值下 1 赞成 1 反对应为 rejected, got %+v", cs.Decision)
	}
	if len(cs.Decision.ScopeSnapshot) != 2 {
		t.Fatalf("决定应追溯当时有效参与范围, got %v", cs.Decision.ScopeSnapshot)
	}
	if got := inst.Data["collab_decision"]; got != "rejected" {
		t.Fatalf("下游数据应为 rejected, got %v", got)
	}
}

func TestCollabDuplicateVoterAndKeyIgnored(t *testing.T) {
	dir := t.TempDir() + "/data"
	st, eng := newTestEngine(t, dir)
	spec := &model.CollabSpec{Participants: []string{"alice", "bob"}}
	nodes, edges := collabNodes(spec)
	def, _ := st.PublishDraft("p", nodes, edges)
	inst, _ := st.CreateInstance(def, map[string]any{})
	eng.Run(def, inst)
	if err := eng.DeliverInput(def, inst, vote("a1", "alice", model.VoteApprove)); err != nil {
		t.Fatal(err)
	}
	// Same participant changes mind: rejected as duplicate vote.
	if err := eng.DeliverInput(def, inst, vote("a2", "alice", model.VoteReject)); err != nil {
		t.Fatal(err)
	}
	// Same key again: idempotent duplicate.
	if err := eng.DeliverInput(def, inst, vote("a1", "alice", model.VoteApprove)); err != nil {
		t.Fatal(err)
	}
	cs := inst.Collabs["review"]
	valid := 0
	for _, v := range cs.Votes {
		if v.Valid {
			valid++
		}
	}
	if valid != 1 {
		t.Fatalf("同一参与方只能有一票有效, got %d / %d", valid, len(cs.Votes))
	}
	if inst.Status != model.StatusWaiting {
		t.Fatal("重复票不能让步骤完成")
	}
}

func TestCollabDelegationAndRevocationKeepsHistory(t *testing.T) {
	dir := t.TempDir() + "/data"
	st, eng := newTestEngine(t, dir)
	spec := &model.CollabSpec{Participants: []string{"alice", "bob"}, Delegatable: true}
	nodes, edges := collabNodes(spec)
	def, _ := st.PublishDraft("p", nodes, edges)
	inst, _ := st.CreateInstance(def, map[string]any{})
	eng.Run(def, inst)
	req := model.GrantRequest{WaitNode: "review", Actor: "alice", Party: "dave"}
	if err := eng.GrantDelegate(def, inst, req); err != nil {
		t.Fatal(err)
	}
	// A delegate not in scope and without delegation must be rejected.
	if err := eng.DeliverInput(def, inst, vote("x", "erin", model.VoteApprove)); err == nil {
		t.Fatal("未授权参与方不应能投票")
	}
	// Dave votes on behalf of alice.
	if err := eng.DeliverInput(def, inst, vote("d1", "dave", model.VoteApprove)); err != nil {
		t.Fatal(err)
	}
	cs := inst.Collabs["review"]
	v0 := cs.Votes[0]
	if v0.OnBehalfOf != "alice" || !v0.Valid || v0.ViaGrantID == "" {
		t.Fatalf("委托票应记在 alice 名下并保留授权链路, got %+v", v0)
	}
	// Revoke but keep prior inputs: dave's vote still counts; grant history kept.
	keep := true
	rev := model.GrantRequest{WaitNode: "review", Actor: "alice", Party: "dave", KeepPriorInputs: &keep}
	if err := eng.RevokeDelegate(def, inst, rev); err != nil {
		t.Fatal(err)
	}
	if len(cs.Grants) != 2 {
		t.Fatalf("新旧授权记录都应保留, got %d", len(cs.Grants))
	}
	if !cs.Votes[0].Valid {
		t.Fatal("声明此前输入有效时，委托票应继续计数")
	}
	// After revocation dave can no longer vote for alice.
	if err := eng.DeliverInput(def, inst, vote("d2", "dave", model.VoteApprove)); err == nil {
		t.Fatal("撤回委托后受托人不应再能投票")
	}
}

func TestCollabRevokeInvalidatesPriorInputWhenDeclared(t *testing.T) {
	dir := t.TempDir() + "/data"
	st, eng := newTestEngine(t, dir)
	spec := &model.CollabSpec{Participants: []string{"alice", "bob"}, Delegatable: true}
	nodes, edges := collabNodes(spec)
	def, _ := st.PublishDraft("p", nodes, edges)
	inst, _ := st.CreateInstance(def, map[string]any{})
	eng.Run(def, inst)
	_ = eng.GrantDelegate(def, inst, model.GrantRequest{WaitNode: "review", Actor: "alice", Party: "dave"})
	_ = eng.DeliverInput(def, inst, vote("d1", "dave", model.VoteApprove))
	keep := false
	_ = eng.RevokeDelegate(def, inst, model.GrantRequest{WaitNode: "review", Actor: "alice", Party: "dave", KeepPriorInputs: &keep})
	cs := inst.Collabs["review"]
	if cs.Votes[0].Valid || cs.Votes[0].InvalidReason == "" {
		t.Fatalf("声明失效的此前输入应保留但标记无效, got %+v", cs.Votes[0])
	}
	if inst.Status != model.StatusWaiting {
		t.Fatal("失效后没有任何有效票，应继续等待")
	}
	// alice can vote again herself after her delegated vote was invalidated.
	if err := eng.DeliverInput(def, inst, vote("a2", "alice", model.VoteApprove)); err != nil {
		t.Fatal(err)
	}
	if inst.Status != model.StatusWaiting {
		t.Fatal("alice 重新投票后仍需 bob 意见，不能提前完成")
	}
}

func TestCollabMajorityTimeout(t *testing.T) {
	dir := t.TempDir() + "/data"
	st, eng := newTestEngine(t, dir)
	spec := &model.CollabSpec{
		Participants:    []string{"alice", "bob", "carol"},
		DeadlineSeconds: 1,
		TimeoutPolicy:   model.TimeoutMajority,
	}
	nodes, edges := collabNodes(spec)
	def, _ := st.PublishDraft("p", nodes, edges)
	inst, _ := st.CreateInstance(def, map[string]any{})
	eng.Run(def, inst)
	_ = eng.DeliverInput(def, inst, vote("a", "alice", model.VoteApprove))
	_ = eng.DeliverInput(def, inst, vote("b", "bob", model.VoteReject))
	time.Sleep(1100 * time.Millisecond)
	eng.Resume(def, inst)
	cs := inst.Collabs["review"]
	if cs.Decision == nil {
		t.Fatal("到期必须形成决定")
	}
	if cs.Decision.Outcome != "timeout_rejected" {
		t.Fatalf("平票按拒绝处理, got %s", cs.Decision.Outcome)
	}
	if got := cs.Decision.ScopeSnapshot; len(got) != 3 {
		t.Fatalf("决定须追溯到期时有效的完整参与范围, got %v", got)
	}
}

func TestCollabThresholdApprovesEarly(t *testing.T) {
	dir := t.TempDir() + "/data"
	st, eng := newTestEngine(t, dir)
	spec := &model.CollabSpec{Participants: []string{"alice", "bob", "carol"}, ApproveThreshold: 2}
	nodes, edges := collabNodes(spec)
	def, _ := st.PublishDraft("p", nodes, edges)
	inst, _ := st.CreateInstance(def, map[string]any{})
	eng.Run(def, inst)
	_ = eng.DeliverInput(def, inst, vote("a", "alice", model.VoteApprove))
	if inst.Status != model.StatusWaiting {
		t.Fatal("一票未达阈值，不得完成")
	}
	_ = eng.DeliverInput(def, inst, vote("b", "bob", model.VoteApprove))
	if inst.Status != model.StatusCompleted || inst.Collabs["review"].Decision.Outcome != "approved" {
		t.Fatalf("两票达到阈值 2 应通过并完成, got %s", inst.Status)
	}
	// Late vote after decision is only retained as history.
	if err := eng.DeliverInput(def, inst, vote("c", "carol", model.VoteReject)); err != nil {
		t.Fatal(err)
	}
	cs := inst.Collabs["review"]
	last := cs.Votes[len(cs.Votes)-1]
	if last.Valid || last.InvalidReason == "" {
		t.Fatalf("决定后的迟到输入只作历史保留且无效, got %+v", last)
	}
}

func TestCollabScopeChangePriorInputPolicy(t *testing.T) {
	dir := t.TempDir() + "/data"
	st, eng := newTestEngine(t, dir)
	spec := &model.CollabSpec{Participants: []string{"alice", "bob"}, Delegatable: true}
	nodes, edges := collabNodes(spec)
	def, _ := st.PublishDraft("p", nodes, edges)
	inst, _ := st.CreateInstance(def, map[string]any{})
	eng.Run(def, inst)
	_ = eng.DeliverInput(def, inst, vote("a", "alice", model.VoteApprove))
	_ = eng.DeliverInput(def, inst, vote("b", "bob", model.VoteApprove))
	if inst.Status != model.StatusCompleted {
		t.Fatal("两人都赞成后应完成")
	}
	// Scope changes after a decision are refused.
	if err := eng.ChangeScope(def, inst, true, model.GrantRequest{WaitNode: "review", Party: "zoe"}); err == nil {
		t.Fatal("决定形成后不应允许变更接收范围")
	}
}

func TestCollabTimeoutOneDecisionAndLateInputHistory(t *testing.T) {
	dir := t.TempDir() + "/data"
	st, eng := newTestEngine(t, dir)
	spec := &model.CollabSpec{
		Participants:    []string{"alice", "bob"},
		DeadlineSeconds: 1,
		TimeoutPolicy:   model.TimeoutReject,
	}
	nodes, edges := collabNodes(spec)
	def, _ := st.PublishDraft("p", nodes, edges)
	inst, _ := st.CreateInstance(def, map[string]any{})
	eng.Run(def, inst)
	_ = eng.DeliverInput(def, inst, vote("a", "alice", model.VoteApprove))
	if inst.Status != model.StatusWaiting {
		t.Fatal("到期前一票不能完成")
	}
	// Simulate deadline passing while the process was stopped, then resume.
	time.Sleep(1100 * time.Millisecond)
	eng.Resume(def, inst)
	if inst.Status != model.StatusCompleted {
		t.Fatalf("到期后应形成唯一决定并离开等待点, got %s", inst.Status)
	}
	cs := inst.Collabs["review"]
	if cs.Decision == nil || cs.Decision.Outcome != "timeout_rejected" {
		t.Fatalf("reject 策略下 1 赞成未达阈值 2，应为 timeout_rejected: %+v", cs.Decision)
	}
	// Inputs arriving after the timeout decision are retained only as history
	// and cannot create a second decision or re-run tasks.
	before := len(inst.Records)
	if err := eng.DeliverInput(def, inst, vote("late", "bob", model.VoteApprove)); err != nil {
		t.Fatal(err)
	}
	if got := inst.Collabs["review"].Decision.Outcome; got != "timeout_rejected" {
		t.Fatalf("迟到输入不能改变既有决定, got %s", got)
	}
	var lateRecords int
	for i := before; i < len(inst.Records); i++ {
		if inst.Records[i].Action == "late_input" {
			lateRecords++
		}
	}
	if lateRecords != 1 {
		t.Fatalf("迟到输入应仅留下一条 late_input 历史, got %d", lateRecords)
	}
}

func TestCollabRestartResumesWaitingWithoutRepeatingVotes(t *testing.T) {
	dir := t.TempDir() + "/data"
	st, eng := newTestEngine(t, dir)
	spec := &model.CollabSpec{Participants: []string{"alice", "bob", "carol"}}
	nodes, edges := collabNodes(spec)
	def, _ := st.PublishDraft("p", nodes, edges)
	inst, _ := st.CreateInstance(def, map[string]any{})
	eng.Run(def, inst)
	_ = eng.DeliverInput(def, inst, vote("a", "alice", model.VoteApprove))
	if err := st.SaveInstance(inst); err != nil {
		t.Fatal(err)
	}
	// Restart: waiting instance must remain waiting; existing votes persist.
	st2, eng2 := newTestEngine(t, dir)
	r, _ := st2.GetInstance(inst.ID)
	def2, _ := st2.GetDefinition(r.DefinitionID)
	eng2.Resume(def2, r)
	if r.Status != model.StatusWaiting {
		t.Fatalf("保存重开后未到期的协作等待应继续等待, got %s", r.Status)
	}
	cs := r.Collabs["review"]
	if cs == nil || len(cs.Votes) != 1 || !cs.Votes[0].Valid {
		t.Fatalf("已有确认必须保留且不重复执行, got %+v", cs)
	}
	_ = eng2.DeliverInput(def2, r, vote("b", "bob", model.VoteApprove))
	if r.Status != model.StatusWaiting {
		t.Fatal("两票赞成（阈值3）仍需等待")
	}
	_ = eng2.DeliverInput(def2, r, vote("c", "carol", model.VoteApprove))
	if r.Status != model.StatusCompleted {
		t.Fatalf("三方赞成后应完成, got %s", r.Status)
	}
}
