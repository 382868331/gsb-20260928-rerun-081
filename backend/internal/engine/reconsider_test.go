package engine_test

import (
	"path/filepath"
	"testing"
	"time"

	"flowplatform/internal/engine"
	"flowplatform/internal/model"
	"flowplatform/internal/store"
)

// Flow: start -> prep(task) -> sign(collab wait: alice,bob) -> pack(task) -> audit(collab wait: carol) -> end
func recNodes() ([]model.Node, []model.Edge) {
	nodes := []model.Node{
		{ID: "start", Type: model.NodeStart, Name: "开始"},
		{ID: "prep", Type: model.NodeTask, Name: "准备", CompensateTo: "prep"},
		{ID: "sign", Type: model.NodeWait, Name: "多方会签",
			Participants: []string{"alice", "bob"}, AllowDelegate: true},
		{ID: "pack", Type: model.NodeTask, Name: "打包", CompensateTo: "pack"},
		{ID: "audit", Type: model.NodeWait, Name: "复核", Participants: []string{"carol"}},
		{ID: "end", Type: model.NodeEnd, Name: "结束"},
	}
	edges := []model.Edge{
		{ID: "e1", From: "start", To: "prep"},
		{ID: "e2", From: "prep", To: "sign"},
		{ID: "e3", From: "sign", To: "pack"},
		{ID: "e4", From: "pack", To: "audit"},
		{ID: "e5", From: "audit", To: "end"},
	}
	return nodes, edges
}

// decidedInstance runs to the sign wait, completes it, and stops at audit.
func decidedInstance(t *testing.T, st *store.Store, eng *engine.Engine) (*model.Definition, *model.Instance) {
	t.Helper()
	nodes, edges := recNodes()
	def, err := st.PublishDraft("rec", nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := st.CreateInstance(def, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	eng.Run(def, inst)
	for _, in := range []model.ExternalInput{
		{WaitNode: "sign", Key: "orig-a", From: "alice"},
		{WaitNode: "sign", Key: "orig-b", From: "bob"},
	} {
		if err := eng.DeliverInput(def, inst, in); err != nil {
			t.Fatal(err)
		}
	}
	if inst.WaitNode != "audit" || inst.Status != model.StatusWaiting {
		t.Fatalf("应推进到 audit 等待, got %s/%s", inst.Status, inst.WaitNode)
	}
	if len(inst.WaitDecisions) != 1 || inst.WaitDecisions[0].NodeID != "sign" {
		t.Fatal("原决定应已登记到 WaitDecisions")
	}
	return def, inst
}

func openRec(t *testing.T, eng *engine.Engine, def *model.Definition, inst *model.Instance, req engine.ReconsiderRequest) *model.Reconsideration {
	t.Helper()
	if req.WaitNode == "" {
		req.WaitNode = "sign"
	}
	if req.Participants == nil {
		req.Participants = []string{"alice", "bob"}
	}
	rec, err := eng.OpenReconsideration(def, inst, req)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func upholdVote(key, from string) model.ExternalInput {
	return model.ExternalInput{Key: key, From: from, Payload: map[string]any{"opinion": "uphold"}}
}

func rejectVote(key, from string) model.ExternalInput {
	return model.ExternalInput{Key: key, From: from, Payload: map[string]any{"opinion": "reject"}}
}

func TestReconsiderOpenKeepsOriginalHistory(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := decidedInstance(t, st, eng)
	authBefore := len(inst.Wait.Authorizations)

	rec := openRec(t, eng, def, inst, engine.ReconsiderRequest{
		Participants: []string{"dave", "erin"},
		Materials:    map[string]any{"contract": "v2"},
		Reason:       "发现采用错误资料",
	})
	if !inst.Reconsidering {
		t.Fatal("复议开启后应处于复议暂停中")
	}
	// Original authorizations/decision untouched.
	if len(inst.Wait.Authorizations) != authBefore {
		t.Fatal("原授权记录不得被复议改写")
	}
	if inst.WaitDecisions[0].Decision != "approved" || inst.WaitDecisions[0].AuthID != "auth-1" {
		t.Fatal("原决定与生效范围保持不变")
	}
	if rec.OriginAuthID != "auth-1" || rec.OriginDecision != "approved" {
		t.Fatal("复议应链接到原决定与其授权范围")
	}
	// Progress: pack executed (compensate-only), audit/end pausable.
	p := eng.ProgressReconsideration(def, inst, rec)
	if len(p.ExecutedAfterDecision) != 1 || p.ExecutedAfterDecision[0] != "pack" {
		t.Fatalf("原决定后已执行应为 [pack], got %v", p.ExecutedAfterDecision)
	}
	if len(p.PausableAhead) != 1 || p.PausableAhead[0] != "end" {
		t.Fatalf("尚可暂停的后续应为 [end], got %v", p.PausableAhead)
	}
	// Materials never merge into instance data.
	if _, ok := inst.Data["contract"]; ok {
		t.Fatal("复议新资料不得写入实例数据")
	}
}

func TestReconsiderHoldsFollowUpInputs(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := decidedInstance(t, st, eng)
	openRec(t, eng, def, inst, engine.ReconsiderRequest{})

	if err := eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "audit", Key: "audit-1", From: "carol"}); err != nil {
		t.Fatal(err)
	}
	if inst.WaitNode != "audit" || inst.Wait == nil || len(inst.Wait.Received) != 0 {
		t.Fatal("复议期间后续动作的输入应被暂扣，不推进")
	}
	if countAction(inst.Records, "input_held") != 1 {
		t.Fatal("应记录 input_held 事件")
	}
}

func TestReconsiderOldVotesCannotCount(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := decidedInstance(t, st, eng)
	rec := openRec(t, eng, def, inst, engine.ReconsiderRequest{})

	// Replay the original wait's keys: duplicate, never counted.
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, model.ExternalInput{Key: "orig-a", From: "alice"}); err != nil {
		t.Fatal(err)
	}
	if len(rec.Received) != 0 || countAction(inst.Records, "duplicate_input") != 1 {
		t.Fatal("旧投票不能凑新一轮人数")
	}
	// Out-of-scope party rejected.
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, model.ExternalInput{Key: "r-x", From: "mallory"}); err != nil {
		t.Fatal(err)
	}
	if countAction(inst.Records, "input_rejected") != 1 {
		t.Fatal("范围外意见应被拒绝")
	}
	// Partial vote does not decide.
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, upholdVote("r-a", "alice")); err != nil {
		t.Fatal(err)
	}
	if rec.Decided || !inst.Reconsidering {
		t.Fatal("部分新意见到达时不应形成决定")
	}
}

func TestReconsiderVoteRequiresExplicitOpinion(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := decidedInstance(t, st, eng)
	rec := openRec(t, eng, def, inst, engine.ReconsiderRequest{})

	// A vote without a recognizable opinion must not count as approval.
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, model.ExternalInput{Key: "r-a", From: "alice"}); err == nil {
		t.Fatal("未明确表态的意见不应被接受")
	}
	if len(rec.Received) != 0 || inst.ConsumedInputs["r-a"] {
		t.Fatal("未表态的意见不得计入，也不得消费幂等键")
	}
	if rec.Decided {
		t.Fatal("未表态不得形成决定")
	}
}

func TestReconsiderMixedVotesReject(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := decidedInstance(t, st, eng)
	rec := openRec(t, eng, def, inst, engine.ReconsiderRequest{})

	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, upholdVote("r-a", "alice")); err != nil {
		t.Fatal(err)
	}
	if rec.Decided {
		t.Fatal("一票赞成不应形成决定")
	}
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, rejectVote("r-b", "bob")); err != nil {
		t.Fatal(err)
	}
	if !rec.Decided || rec.Decision != "rejected" {
		t.Fatalf("一赞成一反对应复议不成立, got %v/%s", rec.Decided, rec.Decision)
	}
	if countAction(inst.Records, "reconsider_decided") != 1 {
		t.Fatal("每轮只形成一个决定")
	}
	// Rejected: original decision and executed actions stand, no compensation.
	if inst.Compensating || len(inst.CompensatedNodes) != 0 {
		t.Fatal("复议被拒绝不得触发补偿")
	}
	if inst.WaitDecisions[0].Decision != "approved" {
		t.Fatal("原决定应保持不变")
	}
	if inst.WaitNode != "audit" || inst.Status != model.StatusWaiting {
		t.Fatalf("已生效的后续动作应保留, got %s/%s", inst.Status, inst.WaitNode)
	}
	// The flow can still continue from where it paused.
	if err := eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "audit", Key: "audit-1", From: "carol"}); err != nil {
		t.Fatal(err)
	}
	if inst.Status != model.StatusCompleted {
		t.Fatalf("复议不成立后原流程应可继续到完成, got %s", inst.Status)
	}
}

func TestReconsiderDelegationRevokedMidRound(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := decidedInstance(t, st, eng)
	rec := openRec(t, eng, def, inst, engine.ReconsiderRequest{
		Participants:     []string{"alice", "bob", "carol"},
		AllowDelegate:    true,
		GrantDelegations: map[string]string{"carol": "alice"},
	})
	// alice confirms for herself and as carol's delegate: 2/3, no decision.
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, upholdVote("r-a", "alice")); err != nil {
		t.Fatal(err)
	}
	if rec.Decided {
		t.Fatal("2/3 不应形成决定")
	}
	// Revoke carol's delegation mid-round: her own vote is now required.
	if err := eng.UpdateReconsiderScope(def, inst, rec.ID, engine.ReconsiderScopeRequest{
		RevokeDelegations: []string{"carol"}, Reason: "委托被撤回",
	}); err != nil {
		t.Fatal(err)
	}
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, upholdVote("r-b", "bob")); err != nil {
		t.Fatal(err)
	}
	if rec.Decided {
		t.Fatal("carol 委托被撤回后需本人确认，2/3 不应形成决定")
	}
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, upholdVote("r-c", "carol")); err != nil {
		t.Fatal(err)
	}
	if !rec.Decided || rec.Decision != "upheld" {
		t.Fatalf("全部确认后应形成 upheld, got %v/%s", rec.Decided, rec.Decision)
	}
	if countAction(inst.Records, "reconsider_decided") != 1 {
		t.Fatal("复议决定唯一")
	}
}

func TestReconsiderScopeChangeTalliesOpinions(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := decidedInstance(t, st, eng)
	rec := openRec(t, eng, def, inst, engine.ReconsiderRequest{
		Participants: []string{"alice", "bob", "carol"},
	})
	// One support, one explicit opposition, one pending.
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, upholdVote("r-a", "alice")); err != nil {
		t.Fatal(err)
	}
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, rejectVote("r-b", "bob")); err != nil {
		t.Fatal(err)
	}
	if rec.Decided {
		t.Fatal("意见未集齐不应形成决定")
	}
	// Shrink the scope to the two who already voted: the opposition must
	// turn the round down, not be read as approval.
	if err := eng.UpdateReconsiderScope(def, inst, rec.ID, engine.ReconsiderScopeRequest{
		Participants: []string{"alice", "bob"}, Reason: "carol 退出本轮",
	}); err != nil {
		t.Fatal(err)
	}
	if !rec.Decided || rec.Decision != "rejected" {
		t.Fatalf("范围变更后应按实际意见形成 rejected, got %v/%s", rec.Decided, rec.Decision)
	}
	if inst.Compensating || len(inst.CompensatedNodes) != 0 {
		t.Fatal("复议不成立不得触发补偿")
	}
}

func TestReconsiderUpheldCompensatesOnlyFollowUps(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := decidedInstance(t, st, eng)
	rec := openRec(t, eng, def, inst, engine.ReconsiderRequest{})
	for _, in := range []model.ExternalInput{upholdVote("r-a", "alice"), upholdVote("r-b", "bob")} {
		if err := eng.DeliverReconsiderVote(def, inst, rec.ID, in); err != nil {
			t.Fatal(err)
		}
	}
	if rec.Decision != "upheld" {
		t.Fatalf("got %s", rec.Decision)
	}
	// pack (after decision) compensated; prep (before decision) untouched.
	if !inst.CompensatedNodes["pack"] {
		t.Fatal("原决定之后的 pack 应被补偿")
	}
	if inst.CompensatedNodes["prep"] {
		t.Fatal("原决定之前的 prep 不得被补偿")
	}
	if inst.Status != model.StatusFailed || inst.StoppedAt != "sign" {
		t.Fatalf("复议通过后应回到原决定点, got %s/%s", inst.Status, inst.StoppedAt)
	}
	// During/after compensation, business inputs cannot re-trigger actions.
	before := len(inst.Records)
	if err := eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "audit", Key: "audit-9", From: "carol"}); err != nil {
		t.Fatal(err)
	}
	if countAction(inst.Records, "input_received") != 2 { // only orig-a/orig-b era? none at audit
		t.Fatal("补偿后不得再推动原业务动作")
	}
	if got := len(inst.Records) - before; got != 1 {
		t.Fatalf("迟到输入只记录一条事件, got %d", got)
	}
}

func TestReconsiderCompensationFailureKeepsRemaining(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := decidedInstance(t, st, eng)
	inst.Data["compFailAt"] = "pack"
	rec := openRec(t, eng, def, inst, engine.ReconsiderRequest{})
	for _, in := range []model.ExternalInput{upholdVote("r-a", "alice"), upholdVote("r-b", "bob")} {
		if err := eng.DeliverReconsiderVote(def, inst, rec.ID, in); err != nil {
			t.Fatal(err)
		}
	}
	if inst.CompensatedNodes["pack"] {
		t.Fatal("补偿失败不得标记完成")
	}
	if countAction(inst.Records, "reconsider_compensation_incomplete") != 1 {
		t.Fatal("补偿失败必须保留未完成范围")
	}
	p := eng.ProgressReconsideration(def, inst, rec)
	if len(p.CompensationRemaining) != 1 || p.CompensationRemaining[0] != "pack" {
		t.Fatalf("未完成范围应为 [pack], got %v", p.CompensationRemaining)
	}
	if inst.Reconsidering {
		t.Fatal("复议已结束，但不得显示全部恢复")
	}
}

func TestReconsiderExpiryUniqueDecision(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := decidedInstance(t, st, eng)
	rec := openRec(t, eng, def, inst, engine.ReconsiderRequest{TimeoutSeconds: 1})
	// Move deadline into the past.
	past := time.Now().UTC().Add(-time.Second)
	rec.Deadline = &past
	eng.CheckReconsiderTimeout(def, inst, time.Now().UTC())
	if !rec.Decided || rec.Decision != "expired_rejected" {
		t.Fatalf("默认到期策略应复议不成立, got %s", rec.Decision)
	}
	if inst.Reconsidering {
		t.Fatal("到期决定后复议应结束")
	}
	// Second evaluation: no new decision.
	eng.CheckReconsiderTimeout(def, inst, time.Now().UTC())
	if countAction(inst.Records, "reconsider_decided") != 1 {
		t.Fatal("到期决定唯一，重复判定不得再形成决定")
	}
	// Late vote after expiry: recorded, not counted.
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, model.ExternalInput{Key: "r-late", From: "alice"}); err != nil {
		t.Fatal(err)
	}
	if len(rec.Received) != 0 || countAction(inst.Records, "late_input") != 1 {
		t.Fatal("复议结束后的迟到意见仅作历史保留")
	}
	// Original wait resumes: held inputs accepted again.
	if err := eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "audit", Key: "audit-1", From: "carol"}); err != nil {
		t.Fatal(err)
	}
	if inst.Status != model.StatusCompleted {
		t.Fatalf("复议不成立后原流程应可继续到完成, got %s", inst.Status)
	}
}

func TestReconsiderExpiryUpheldPolicy(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := decidedInstance(t, st, eng)
	rec := openRec(t, eng, def, inst, engine.ReconsiderRequest{TimeoutSeconds: 1, TimeoutPolicy: "uphold"})
	past := time.Now().UTC().Add(-time.Second)
	rec.Deadline = &past
	eng.CheckReconsiderTimeout(def, inst, time.Now().UTC())
	if rec.Decision != "expired_upheld" {
		t.Fatalf("got %s", rec.Decision)
	}
	if !inst.CompensatedNodes["pack"] || inst.CompensatedNodes["prep"] {
		t.Fatal("到期复议通过同样只补偿原决定之后的动作")
	}
}

func TestReconsiderRejectedResumesFlow(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := decidedInstance(t, st, eng)
	rec := openRec(t, eng, def, inst, engine.ReconsiderRequest{Participants: []string{"alice"}})
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, upholdVote("r-a", "alice")); err != nil {
		t.Fatal(err)
	}
	if rec.Decision != "upheld" {
		t.Fatalf("got %s", rec.Decision)
	}
	// The original decision was overturned: no second round against it.
	if _, err := eng.OpenReconsideration(def, inst, engine.ReconsiderRequest{WaitNode: "sign", Participants: []string{"alice"}}); err == nil {
		t.Fatal("原决定已被撤销，不应允许对同一决定再次复议")
	}
}

func TestReconsiderRejectedAllowsNewRound(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := decidedInstance(t, st, eng)
	rec := openRec(t, eng, def, inst, engine.ReconsiderRequest{TimeoutSeconds: 1})
	past := time.Now().UTC().Add(-time.Second)
	rec.Deadline = &past
	eng.CheckReconsiderTimeout(def, inst, time.Now().UTC())
	if rec.Decision != "expired_rejected" {
		t.Fatalf("got %s", rec.Decision)
	}
	// A rejected round does not block a fresh round with new materials.
	rec2 := openRec(t, eng, def, inst, engine.ReconsiderRequest{
		Participants: []string{"alice"}, Materials: map[string]any{"contract": "v3"},
	})
	if rec2.ID == rec.ID || len(inst.Reconsiderations) != 2 {
		t.Fatal("新一轮复议应独立编号并保留旧轮记录")
	}
	// Old round's keys stay consumed: they cannot make up numbers again.
	if err := eng.DeliverReconsiderVote(def, inst, rec2.ID, model.ExternalInput{Key: "orig-a", From: "alice"}); err != nil {
		t.Fatal(err)
	}
	if len(rec2.Received) != 0 {
		t.Fatal("旧投票不能计入新一轮")
	}
}

// assertOutcomeConsistency verifies, including after a simulated restart,
// that the unique decision, retained actions and compensation to-dos all
// agree with the round's outcome, and that replaying old inputs changes
// nothing.
func assertOutcomeConsistency(t *testing.T, dir string, instID, recID, wantDecision string, wantRetained, wantRemaining []string) {
	t.Helper()
	st2, eng2 := newTestEngine(t, dir)
	inst, err := st2.GetInstance(instID)
	if err != nil {
		t.Fatal(err)
	}
	def, err := st2.GetDefinition(inst.DefinitionID)
	if err != nil {
		t.Fatal(err)
	}
	rec := engine.FindReconsideration(inst, recID)
	if rec == nil || !rec.Decided || rec.Decision != wantDecision {
		got := "missing"
		if rec != nil {
			got = rec.Decision
		}
		t.Fatalf("重启后唯一决定应为 %s, got %s", wantDecision, got)
	}
	if countAction(inst.Records, "reconsider_decided") != 1 {
		t.Fatal("重启后决定仍必须唯一")
	}
	p := eng2.ProgressReconsideration(def, inst, rec)
	if len(p.RetainedActions) != len(wantRetained) {
		t.Fatalf("继续有效的动作应为 %v, got %v", wantRetained, p.RetainedActions)
	}
	for i, id := range wantRetained {
		if i >= len(p.RetainedActions) || p.RetainedActions[i] != id {
			t.Fatalf("继续有效的动作应为 %v, got %v", wantRetained, p.RetainedActions)
		}
	}
	if len(p.CompensationRemaining) != len(wantRemaining) {
		t.Fatalf("待补偿应为 %v, got %v", wantRemaining, p.CompensationRemaining)
	}
	for i, id := range wantRemaining {
		if i >= len(p.CompensationRemaining) || p.CompensationRemaining[i] != id {
			t.Fatalf("待补偿应为 %v, got %v", wantRemaining, p.CompensationRemaining)
		}
	}
	// Replaying old vote keys and old business inputs after restart must not
	// alter the formed conclusion.
	for _, in := range []model.ExternalInput{
		{Key: "r-a", From: "alice", Payload: map[string]any{"opinion": "uphold"}},
		{Key: "r-b", From: "bob", Payload: map[string]any{"opinion": "reject"}},
		{Key: "orig-a", From: "alice"},
	} {
		if err := eng2.DeliverReconsiderVote(def, inst, recID, in); err != nil {
			t.Fatal(err)
		}
	}
	if err := eng2.DeliverInput(def, inst, model.ExternalInput{WaitNode: "audit", Key: "audit-1", From: "carol"}); err != nil {
		t.Fatal(err)
	}
	rec2 := engine.FindReconsideration(inst, recID)
	if !rec2.Decided || rec2.Decision != wantDecision || countAction(inst.Records, "reconsider_decided") != 1 {
		t.Fatal("重复旧输入不得改变已经形成的结论")
	}
}

func TestReconsiderThreeOutcomesStayConsistentAcrossRestart(t *testing.T) {
	cases := []struct {
		name      string
		compFail  bool
		setup     func(eng *engine.Engine, def *model.Definition, inst *model.Instance, recID string)
		decision  string
		retained  []string
		remaining []string
	}{
		{
			name:     "一票反对",
			decision: "rejected",
			retained: []string{"pack"},
			setup: func(eng *engine.Engine, def *model.Definition, inst *model.Instance, recID string) {
				if err := eng.DeliverReconsiderVote(def, inst, recID, upholdVote("r-a", "alice")); err != nil {
					t.Fatal(err)
				}
				if err := eng.DeliverReconsiderVote(def, inst, recID, rejectVote("r-b", "bob")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:      "一致赞成且补偿失败",
			compFail:  true,
			decision:  "upheld",
			remaining: []string{"pack"},
			setup: func(eng *engine.Engine, def *model.Definition, inst *model.Instance, recID string) {
				for _, in := range []model.ExternalInput{upholdVote("r-a", "alice"), upholdVote("r-b", "bob")} {
					if err := eng.DeliverReconsiderVote(def, inst, recID, in); err != nil {
						t.Fatal(err)
					}
				}
			},
		},
		{
			name:     "到期拒绝",
			decision: "expired_rejected",
			retained: []string{"pack"},
			setup: func(eng *engine.Engine, def *model.Definition, inst *model.Instance, recID string) {
				rec := engine.FindReconsideration(inst, recID)
				past := time.Now().UTC().Add(-time.Second)
				rec.Deadline = &past
				eng.CheckReconsiderTimeout(def, inst, time.Now().UTC())
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "data")
			st, eng := newTestEngine(t, dir)
			def, inst := decidedInstance(t, st, eng)
			if tc.compFail {
				inst.Data["compFailAt"] = "pack"
			}
			req := engine.ReconsiderRequest{TimeoutSeconds: 1}
			rec := openRec(t, eng, def, inst, req)
			tc.setup(eng, def, inst, rec.ID)
			if !rec.Decided || rec.Decision != tc.decision {
				t.Fatalf("应形成 %s, got %s", tc.decision, rec.Decision)
			}
			if err := st.SaveInstance(inst); err != nil {
				t.Fatal(err)
			}
			assertOutcomeConsistency(t, dir, inst.ID, rec.ID, tc.decision, tc.retained, tc.remaining)
		})
	}
}
