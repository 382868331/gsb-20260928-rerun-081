package engine_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flowplatform/internal/engine"
	"flowplatform/internal/model"
	"flowplatform/internal/store"
)

// reviewNodes: start -> prep(task) -> sign(collab wait alice,bob) -> ship(task)
// -> confirm(simple wait) -> end. The collaborative decision at "sign" is the
// original decision reviews attach to.
func reviewNodes() ([]model.Node, []model.Edge) {
	nodes := []model.Node{
		{ID: "start", Type: model.NodeStart, Name: "开始"},
		{ID: "prep", Type: model.NodeTask, Name: "准备", CompensateTo: "prep"},
		{ID: "sign", Type: model.NodeWait, Name: "多方会签", Participants: []string{"alice", "bob"}, AllowDelegate: true},
		{ID: "ship", Type: model.NodeTask, Name: "发货", CompensateTo: "ship"},
		{ID: "confirm", Type: model.NodeWait, Name: "人工确认"},
		{ID: "end", Type: model.NodeEnd, Name: "结束"},
	}
	edges := []model.Edge{
		{ID: "e1", From: "start", To: "prep"},
		{ID: "e2", From: "prep", To: "sign"},
		{ID: "e3", From: "sign", To: "ship"},
		{ID: "e4", From: "ship", To: "confirm"},
		{ID: "e5", From: "confirm", To: "end"},
	}
	return nodes, edges
}

// startReviewed runs the instance past the collaborative decision so it waits
// at the follow-up node "confirm".
func startReviewed(t *testing.T, st *store.Store, eng *engine.Engine, data map[string]any) (*model.Definition, *model.Instance) {
	t.Helper()
	nodes, edges := reviewNodes()
	def, err := st.PublishDraft("review-flow", nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	if data == nil {
		data = map[string]any{}
	}
	inst, err := st.CreateInstance(def, data)
	if err != nil {
		t.Fatal(err)
	}
	eng.Run(def, inst)
	for _, in := range []model.ExternalInput{
		{WaitNode: "sign", Key: "k-a", From: "alice"},
		{WaitNode: "sign", Key: "k-b", From: "bob"},
	} {
		if err := eng.DeliverInput(def, inst, in); err != nil {
			t.Fatal(err)
		}
	}
	if inst.Status != model.StatusWaiting || inst.WaitNode != "confirm" {
		t.Fatalf("原决定通过后应推进到 confirm 等待, got %s/%s", inst.Status, inst.WaitNode)
	}
	if len(inst.WaitHistory) != 1 || !inst.WaitHistory[0].Decided || inst.WaitHistory[0].Decision != "approved" {
		t.Fatal("原协作等待应已决定为 approved")
	}
	return def, inst
}

func openReview(t *testing.T, eng *engine.Engine, def *model.Definition, inst *model.Instance, req engine.OpenReviewRequest) {
	t.Helper()
	if req.Participants == nil {
		req.Participants = []string{"carol", "dave"}
	}
	if err := eng.OpenReview(def, inst, req); err != nil {
		t.Fatal(err)
	}
	if inst.ActiveReview() == nil {
		t.Fatal("复议应已开启")
	}
}

func reviewIn(t *testing.T, eng *engine.Engine, def *model.Definition, inst *model.Instance, from, key string, payload map[string]any) {
	t.Helper()
	if err := eng.DeliverReviewInput(def, inst, model.ExternalInput{Key: key, From: from, Payload: payload}); err != nil {
		t.Fatal(err)
	}
}

func TestReviewApprovedCompensatesOnlyFollowUps(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := startReviewed(t, st, eng, nil)
	openReview(t, eng, def, inst, engine.OpenReviewRequest{Materials: map[string]any{"doc": "v2"}, Reason: "发现原资料有误"})

	// 迟到的旧投票（原业务等待已生效的 key）不能凑复议人数。
	reviewIn(t, eng, def, inst, "carol", "k-a", nil)
	rv := inst.ActiveReview()
	if rv == nil || len(rv.Received) != 0 {
		t.Fatal("旧投票 key 不能计入复议")
	}
	if countAction(inst.Records, "review_old_vote_rejected") != 1 {
		t.Fatal("应记录旧投票被拒事件")
	}

	// 部分新意见只记录不决定。
	reviewIn(t, eng, def, inst, "carol", "r-1", nil)
	if inst.ActiveReview() == nil || inst.ActiveReview().Decided {
		t.Fatal("部分复议意见不应形成决定")
	}
	reviewIn(t, eng, def, inst, "dave", "r-2", nil)

	rv = inst.LastReview()
	if rv == nil || !rv.Decided || rv.Decision != "approved" {
		t.Fatalf("复议应通过, got %+v", rv)
	}
	if countAction(inst.Records, "review_decided") != 1 {
		t.Fatal("复议决定只能形成一次")
	}
	// 只有原决定之后的 ship 被补偿，prep 在原决定之前，不动。
	if !inst.CompensatedNodes["ship"] {
		t.Fatal("后续动作 ship 应被补偿")
	}
	if inst.CompensatedNodes["prep"] {
		t.Fatal("原决定之前的 prep 不应被补偿")
	}
	if inst.Status != model.StatusFailed || inst.StoppedAt != "sign" {
		t.Fatalf("复议通过后实例应停在原决定节点, got %s/%s", inst.Status, inst.StoppedAt)
	}
	// 补偿结束后，原业务动作不能再被推动。
	if err := eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "confirm", Key: "late-biz"}); err != nil {
		t.Fatal(err)
	}
	if countAction(inst.Records, "late_input") == 0 || inst.ConsumedInputs["late-biz"] {
		t.Fatal("复议补偿后业务输入应作为迟到输入忽略")
	}
	// 原决定的 wait_decided 记录仍在，历史未被改写。
	if countAction(inst.Records, "wait_decided") != 1 {
		t.Fatal("原决定记录必须保留")
	}
}

func TestReviewHoldsBusinessInputUntilRejected(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := startReviewed(t, st, eng, nil)
	openReview(t, eng, def, inst, engine.OpenReviewRequest{})

	// 复议等待期间，后续业务输入被挂起而不是推进。
	if err := eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "confirm", Key: "biz-1"}); err != nil {
		t.Fatal(err)
	}
	if inst.ConsumedInputs["biz-1"] || inst.WaitNode != "confirm" {
		t.Fatal("复议期间业务输入应挂起，不推进实例")
	}
	if countAction(inst.Records, "held_by_review") != 1 {
		t.Fatal("应记录挂起事件")
	}

	// 复议拒绝（dave 投 reject）：原决定继续有效，业务可继续。
	reviewIn(t, eng, def, inst, "carol", "r-1", nil)
	reviewIn(t, eng, def, inst, "dave", "r-2", map[string]any{"vote": "reject"})
	rv := inst.LastReview()
	if rv.Decision != "rejected" {
		t.Fatalf("应形成拒绝决定, got %s", rv.Decision)
	}
	if inst.Status != model.StatusWaiting || inst.WaitNode != "confirm" {
		t.Fatal("复议拒绝后实例应继续在原等待点")
	}
	if err := eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "confirm", Key: "biz-2"}); err != nil {
		t.Fatal(err)
	}
	if inst.Status != model.StatusCompleted {
		t.Fatalf("复议拒绝后业务应能推进完成, got %s", inst.Status)
	}
	// 复议结束后的迟到复议意见只作历史保留。
	reviewIn(t, eng, def, inst, "carol", "r-3", nil)
	if countAction(inst.Records, "review_decided") != 1 {
		t.Fatal("迟到的复议意见不能再形成决定")
	}
}

func TestReviewExpiryFormsOwnDecision(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := startReviewed(t, st, eng, nil)
	openReview(t, eng, def, inst, engine.OpenReviewRequest{TimeoutSeconds: 1})

	rv := inst.ActiveReview()
	past := rv.Deadline.Add(time.Second)
	eng.CheckReviewTimeout(def, inst, past)
	if !rv.Decided || rv.Decision != "expired" {
		t.Fatalf("复议到期应形成 expired 决定, got %s", rv.Decision)
	}
	if inst.Status != model.StatusWaiting || inst.WaitNode != "confirm" {
		t.Fatal("复议到期不成立，原决定继续有效，实例保持在原等待点")
	}
	// 到期后迟到的复议意见被忽略。
	reviewIn(t, eng, def, inst, "carol", "r-1", nil)
	if countAction(inst.Records, "review_decided") != 1 {
		t.Fatal("复议决定只能形成一次")
	}
	// 再次发起新一轮复议是允许的（旧轮已决定）。
	openReview(t, eng, def, inst, engine.OpenReviewRequest{Participants: []string{"erin"}})
	if inst.LastReview().ID != "review-2" {
		t.Fatalf("新复议应递增编号, got %s", inst.LastReview().ID)
	}
}

func TestReviewScopeRevokeDelegation(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := startReviewed(t, st, eng, nil)
	openReview(t, eng, def, inst, engine.OpenReviewRequest{AllowDelegate: true, Participants: []string{"carol", "dave", "erin"}})

	// 委托 carol -> frank（frank 本身不是参与方），frank 代 carol 表态。
	if err := eng.UpdateReviewScope(def, inst, engine.ScopeRequest{
		GrantDelegations: map[string]string{"carol": "frank"}, KeepPriorInputs: true, Reason: "carol 出差"}); err != nil {
		t.Fatal(err)
	}
	reviewIn(t, eng, def, inst, "frank", "r-1", nil)
	rv := inst.ActiveReview()
	if len(rv.Received) != 1 {
		t.Fatal("受托方表态应计入复议")
	}
	// 撤回委托且保留此前表态：frank 不再是有效提交人，其表态失效；carol 需亲自表态。
	if err := eng.UpdateReviewScope(def, inst, engine.ScopeRequest{
		RevokeDelegations: []string{"carol"}, KeepPriorInputs: true, Reason: "carol 回来了"}); err != nil {
		t.Fatal(err)
	}
	if len(rv.Received) != 0 {
		t.Fatal("撤回委托后，受托方表态应失效（仅作历史保留）")
	}
	if len(rv.Authorizations) != 3 {
		t.Fatalf("复议授权历史应全部保留, got %d", len(rv.Authorizations))
	}
	// 原决定的授权历史不受影响。
	if len(inst.WaitHistory) != 1 || len(inst.WaitHistory[0].Authorizations) != 1 {
		t.Fatal("复议范围变更不能改写原决定的授权记录")
	}
	reviewIn(t, eng, def, inst, "carol", "r-2", nil)
	reviewIn(t, eng, def, inst, "dave", "r-3", nil)
	reviewIn(t, eng, def, inst, "erin", "r-4", nil)
	if !inst.LastReview().Decided {
		t.Fatal("集齐后复议应形成决定")
	}
}

func TestReviewCompensationFailureKeepsUnfinishedScope(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := startReviewed(t, st, eng, map[string]any{"compFailAt": "ship"})
	openReview(t, eng, def, inst, engine.OpenReviewRequest{})
	reviewIn(t, eng, def, inst, "carol", "r-1", nil)
	reviewIn(t, eng, def, inst, "dave", "r-2", nil)

	rv := inst.LastReview()
	if !rv.Decided || rv.Decision != "approved" {
		t.Fatal("复议应通过")
	}
	if inst.Status != model.StatusFailed || inst.StoppedAt != "ship" {
		t.Fatalf("补偿失败应停在真实停点 ship, got %s/%s", inst.Status, inst.StoppedAt)
	}
	// 复议结束不等于全部恢复：未完成范围必须保留可见。
	prog := eng.ReviewProgress(def, inst, rv)
	if len(prog.PendingCompensation) != 1 || prog.PendingCompensation[0] != "ship" {
		t.Fatalf("补偿失败应保留未完成范围, got %+v", prog.PendingCompensation)
	}
	if len(prog.Compensated) != 0 {
		t.Fatal("失败补偿不能记为已完成")
	}
	// 重新打开（从磁盘恢复）后仍能沿原决定、复议与动作解释状态。
	if err := st.SaveInstance(inst); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.GetInstance(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	lrv := loaded.LastReview()
	if lrv == nil || lrv.Decision != "approved" || lrv.OrigDecision != "approved" || lrv.OrigAuthID == "" {
		t.Fatal("恢复后应能追溯原决定与复议依据")
	}
	if !strings.Contains(lastDetail(loaded.Records, "review_decided"), "复议范围") {
		t.Fatal("复议决定记录应含依据范围")
	}
}

func lastDetail(recs []model.StepRecord, action string) string {
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Action == action {
			return recs[i].Detail
		}
	}
	return ""
}
