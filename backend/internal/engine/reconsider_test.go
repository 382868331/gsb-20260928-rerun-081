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

func recVote(key, from, choice string) model.ExternalInput {
	return model.ExternalInput{Key: key, From: from, Payload: map[string]any{"choice": choice}}
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
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, recVote("r-a", "alice", "uphold")); err != nil {
		t.Fatal(err)
	}
	if rec.Decided || !inst.Reconsidering {
		t.Fatal("部分新意见到达时不应形成决定")
	}
}

func TestReconsiderOneOppositionRejectsWithoutCompensation(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := decidedInstance(t, st, eng)
	rec := openRec(t, eng, def, inst, engine.ReconsiderRequest{})

	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, recVote("r-a", "alice", "uphold")); err != nil {
		t.Fatal(err)
	}
	if rec.Decided {
		t.Fatal("单方赞成不能提前决定")
	}
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, recVote("r-b", "bob", "rejected")); err != nil {
		t.Fatal(err)
	}
	if rec.Decision != "rejected" || inst.Reconsidering {
		t.Fatalf("一票反对应拒绝复议, got decided=%v decision=%s reconsidering=%v", rec.Decided, rec.Decision, inst.Reconsidering)
	}
	if countAction(inst.Records, "reconsider_decided") != 1 {
		t.Fatal("一轮只能形成一个决定")
	}
	if len(inst.CompensatedNodes) != 0 {
		t.Fatalf("拒绝复议必须保留已生效动作，不得触发补偿: %v", inst.CompensatedNodes)
	}
	if inst.WaitNode != "audit" || inst.Status != model.StatusWaiting {
		t.Fatalf("拒绝后应保留原决定并停在原后续等待点, got %s/%s", inst.Status, inst.WaitNode)
	}
	p := eng.ProgressReconsideration(def, inst, rec)
	if p.ReceivedVotes != 2 || p.UpheldVotes != 1 || p.RejectedVotes != 1 || len(p.CompensationRemaining) != 0 {
		t.Fatalf("进度应展示双方意见且没有补偿范围, got %+v", p)
	}
	if err := eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "audit", Key: "audit-1", From: "carol"}); err != nil {
		t.Fatal(err)
	}
	if inst.Status != model.StatusCompleted {
		t.Fatalf("原决定保留后原流程应继续, got %s", inst.Status)
	}
}

func TestReconsiderVoteRequiresExplicitChoice(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := decidedInstance(t, st, eng)
	rec := openRec(t, eng, def, inst, engine.ReconsiderRequest{})

	err := eng.DeliverReconsiderVote(def, inst, rec.ID, model.ExternalInput{Key: "r-a", From: "alice"})
	if err == nil {
		t.Fatal("未明确赞成/反对的意见必须被拒绝")
	}
	if len(rec.Received) != 0 || inst.ConsumedInputs["r-a"] || rec.Decided {
		t.Fatal("无效意见不得计入、消费幂等键或形成决定")
	}
}

func TestReconsiderVoteChoicesSurviveRestartAndOppositionDecides(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st, eng := newTestEngine(t, dir)
	def, inst := decidedInstance(t, st, eng)
	rec := openRec(t, eng, def, inst, engine.ReconsiderRequest{TimeoutSeconds: 1})
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, recVote("r-a", "alice", "uphold")); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveInstance(inst); err != nil {
		t.Fatal(err)
	}

	st2, eng2 := newTestEngine(t, dir)
	inst2, err := st2.GetInstance(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec2 := engine.FindReconsideration(inst2, rec.ID)
	if got := rec2.Received["alice"].Choice; got != "uphold" {
		t.Fatalf("重启后赞成选择应保留, got %q", got)
	}
	deadline := time.Now().UTC().Add(time.Hour)
	rec2.Deadline = &deadline
	if err := eng2.DeliverReconsiderVote(def, inst2, rec.ID, recVote("r-b", "bob", "rejected")); err != nil {
		t.Fatal(err)
	}
	if rec2.Decision != "rejected" || len(inst2.CompensatedNodes) != 0 {
		t.Fatalf("重启后反对应拒绝且不补偿, got decision=%s compensated=%v", rec2.Decision, inst2.CompensatedNodes)
	}
	if countAction(inst2.Records, "reconsider_decided") != 1 {
		t.Fatal("重启前后仍只能形成一个决定")
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
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, recVote("r-a", "alice", "uphold")); err != nil {
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
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, recVote("r-b", "bob", "uphold")); err != nil {
		t.Fatal(err)
	}
	if rec.Decided {
		t.Fatal("carol 委托被撤回后需本人确认，2/3 不应形成决定")
	}
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, recVote("r-c", "carol", "rejected")); err != nil {
		t.Fatal(err)
	}
	if !rec.Decided || rec.Decision != "rejected" {
		t.Fatalf("委托撤回后本人反对应形成 rejected, got %v/%s", rec.Decided, rec.Decision)
	}
	if countAction(inst.Records, "reconsider_decided") != 1 {
		t.Fatal("复议决定唯一")
	}
	if inst.CompensatedNodes["pack"] {
		t.Fatal("反对导致复议拒绝时不得补偿已生效动作")
	}
}

func TestReconsiderUpheldCompensatesOnlyFollowUps(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := decidedInstance(t, st, eng)
	rec := openRec(t, eng, def, inst, engine.ReconsiderRequest{})
	for _, in := range []model.ExternalInput{recVote("r-a", "alice", "uphold"), recVote("r-b", "bob", "uphold")} {
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
	for _, in := range []model.ExternalInput{recVote("r-a", "alice", "uphold"), recVote("r-b", "bob", "uphold")} {
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
	if err := eng.DeliverReconsiderVote(def, inst, rec.ID, recVote("r-a", "alice", "uphold")); err != nil {
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
