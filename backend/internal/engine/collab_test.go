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

func collabNodes() ([]model.Node, []model.Edge) {
	nodes := []model.Node{
		{ID: "start", Type: model.NodeStart, Name: "开始"},
		{ID: "prep", Type: model.NodeTask, Name: "准备", CompensateTo: "prep"},
		{ID: "sign", Type: model.NodeWait, Name: "多方会签",
			Participants:  []string{"alice", "bob", "carol"},
			AllowDelegate: true, TimeoutSeconds: 3600, TimeoutPolicy: "reject"},
		{ID: "end", Type: model.NodeEnd, Name: "结束"},
	}
	edges := []model.Edge{
		{ID: "e1", From: "start", To: "prep"},
		{ID: "e2", From: "prep", To: "sign"},
		{ID: "e3", From: "sign", To: "end"},
	}
	return nodes, edges
}

func startCollab(t *testing.T, st *store.Store, eng *engine.Engine) (*model.Definition, *model.Instance) {
	t.Helper()
	nodes, edges := collabNodes()
	def, err := st.PublishDraft("collab", nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := st.CreateInstance(def, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	eng.Run(def, inst)
	if inst.Status != model.StatusWaiting || inst.WaitNode != "sign" {
		t.Fatalf("应停在协作等待, got %s/%s", inst.Status, inst.WaitNode)
	}
	if inst.Wait == nil || len(inst.Wait.Authorizations) != 1 {
		t.Fatal("协作等待状态应已初始化且含初始授权记录")
	}
	return def, inst
}

func TestCollabPartialInputDoesNotComplete(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := startCollab(t, st, eng)

	in := func(from, key string) {
		if err := eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "sign", Key: key, From: from}); err != nil {
			t.Fatal(err)
		}
	}
	in("alice", "k-a")
	if inst.Status != model.StatusWaiting {
		t.Fatalf("部分输入不能完成步骤, got %s", inst.Status)
	}
	in("bob", "k-b")
	if inst.Status != model.StatusWaiting {
		t.Fatalf("2/3 输入不能完成步骤, got %s", inst.Status)
	}
	in("carol", "k-c")
	if inst.Status != model.StatusCompleted {
		t.Fatalf("全部确认后应完成, got %s", inst.Status)
	}
	if n := countAction(inst.Records, "wait_decided"); n != 1 {
		t.Fatalf("只能形成一个决定, got %d", n)
	}
	dec := inst.Wait.DecisionAuthID
	if dec != "auth-1" {
		t.Fatalf("决定应能追到当时授权范围 auth-1, got %s", dec)
	}
}

func TestCollabRejectsUnknownAndDuplicateParties(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := startCollab(t, st, eng)

	// Unknown party: recorded, not effective.
	if err := eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "sign", Key: "k-x", From: "mallory"}); err != nil {
		t.Fatal(err)
	}
	if countAction(inst.Records, "input_rejected") != 1 || inst.Status != model.StatusWaiting {
		t.Fatal("范围外参与方应被拒绝且步骤仍等待")
	}
	// Duplicate from same party: only first counts.
	in := model.ExternalInput{WaitNode: "sign", Key: "k-a1", From: "alice"}
	if err := eng.DeliverInput(def, inst, in); err != nil {
		t.Fatal(err)
	}
	if err := eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "sign", Key: "k-a2", From: "alice"}); err != nil {
		t.Fatal(err)
	}
	if n := countAction(inst.Records, "input_received"); n != 1 {
		t.Fatalf("同一参与方重复确认只生效一次, input_received=%d", n)
	}
	if countAction(inst.Records, "duplicate_input") != 1 {
		t.Fatal("重复确认应记录 duplicate_input")
	}
}

func TestCollabScopeChangeKeepsBothAuthorizations(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := startCollab(t, st, eng)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "sign", Key: "k-a", From: "alice"}))

	// Delegate bob -> dave, then revoke it; both scopes stay in history.
	must(eng.UpdateWaitScope(def, inst, engine.ScopeRequest{
		GrantDelegations: map[string]string{"bob": "dave"},
		KeepPriorInputs:  true,
		Reason:           "bob 休假委托 dave",
	}))
	must(eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "sign", Key: "k-d", From: "dave"}))
	must(eng.UpdateWaitScope(def, inst, engine.ScopeRequest{
		RevokeDelegations: []string{"bob"},
		KeepPriorInputs:   true,
		Reason:            "bob 返岗，撤回委托",
	}))
	if len(inst.Wait.Authorizations) != 3 {
		t.Fatalf("新旧授权记录都应保留, got %d", len(inst.Wait.Authorizations))
	}
	// After revocation dave is out of scope; bob confirms himself.
	if err := eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "sign", Key: "k-d2", From: "dave"}); err != nil {
		t.Fatal(err)
	}
	if countAction(inst.Records, "input_rejected") != 1 {
		t.Fatal("撤回委托后 dave 的输入应被拒绝")
	}
	must(eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "sign", Key: "k-b", From: "bob"}))
	must(eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "sign", Key: "k-c", From: "carol"}))
	if inst.Status != model.StatusCompleted {
		t.Fatalf("应完成, got %s", inst.Status)
	}
	// The decision names the scope in effect at decision time.
	if inst.Wait.DecisionAuthID != "auth-3" {
		t.Fatalf("决定应追到 auth-3, got %s", inst.Wait.DecisionAuthID)
	}
}

func TestCollabScopeChangeCanInvalidatePriorInputs(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := startCollab(t, st, eng)
	if err := eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "sign", Key: "k-a", From: "alice"}); err != nil {
		t.Fatal(err)
	}
	// Shrink scope and declare prior inputs invalid.
	if err := eng.UpdateWaitScope(def, inst, engine.ScopeRequest{
		Participants:    []string{"alice", "carol"},
		KeepPriorInputs: false,
		Reason:          "bob 退出流程",
	}); err != nil {
		t.Fatal(err)
	}
	if len(inst.Wait.Received) != 0 {
		t.Fatal("声明失效后此前输入不应再计入")
	}
	if !strings.Contains(inst.Records[len(inst.Records)-1].Detail, "失效") {
		t.Fatal("范围变更记录应说明此前输入是否仍有效")
	}
	if len(inst.Wait.Authorizations) != 2 {
		t.Fatal("旧授权记录应保留")
	}
	// alice must confirm again under the new scope.
	for _, in := range []model.ExternalInput{
		{WaitNode: "sign", Key: "k-a2", From: "alice"},
		{WaitNode: "sign", Key: "k-c", From: "carol"},
	} {
		if err := eng.DeliverInput(def, inst, in); err != nil {
			t.Fatal(err)
		}
	}
	if inst.Status != model.StatusCompleted {
		t.Fatalf("新范围集齐后应完成, got %s", inst.Status)
	}
}

func TestCollabTimeoutSingleDecisionAndLateInput(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	def, inst := startCollab(t, st, eng)
	if err := eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "sign", Key: "k-a", From: "alice"}); err != nil {
		t.Fatal(err)
	}
	// Timeout and a simultaneous input: the timeout decision is the only one.
	past := inst.Wait.Deadline.Add(time.Second)
	eng.CheckTimeout(def, inst, past)
	if inst.Wait.Decision != "timeout_rejected" {
		t.Fatalf("应按到期策略拒绝, got %s", inst.Wait.Decision)
	}
	if inst.Status != model.StatusFailed {
		t.Fatalf("超时拒绝应进入补偿并失败, got %s", inst.Status)
	}
	if countAction(inst.Records, "wait_decided") != 1 {
		t.Fatal("只能形成一个决定")
	}
	// Late input during/after compensation: history only, nothing re-runs.
	before := countAction(inst.Records, "compensated")
	if err := eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "sign", Key: "k-b", From: "bob"}); err != nil {
		t.Fatal(err)
	}
	if countAction(inst.Records, "late_input") != 1 {
		t.Fatal("补偿期间的迟到输入应仅作历史记录")
	}
	if countAction(inst.Records, "compensated") != before {
		t.Fatal("迟到输入不能重做补偿")
	}
	// A second timeout evaluation cannot create another decision.
	eng.CheckTimeout(def, inst, past.Add(time.Hour))
	if countAction(inst.Records, "wait_decided") != 1 {
		t.Fatal("决定只能形成一次")
	}
}

func TestCollabTimeoutApprovePolicyAdvances(t *testing.T) {
	st, eng := newTestEngine(t, filepath.Join(t.TempDir(), "data"))
	nodes, edges := collabNodes()
	for i := range nodes {
		if nodes[i].ID == "sign" {
			nodes[i].TimeoutPolicy = "approve"
		}
	}
	def, err := st.PublishDraft("collab-approve", nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := st.CreateInstance(def, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	eng.Run(def, inst)
	eng.CheckTimeout(def, inst, inst.Wait.Deadline.Add(time.Second))
	if inst.Wait.Decision != "timeout_approved" || inst.Status != model.StatusCompleted {
		t.Fatalf("到期策略 approve 应放行, got %s/%s", inst.Wait.Decision, inst.Status)
	}
}

func TestCollabWaitSurvivesRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st, eng := newTestEngine(t, dir)
	def, inst := startCollab(t, st, eng)
	if err := eng.DeliverInput(def, inst, model.ExternalInput{WaitNode: "sign", Key: "k-a", From: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveInstance(inst); err != nil {
		t.Fatal(err)
	}

	// Restart: reload everything from disk; waiting continues, the existing
	// confirmation is not lost or re-executed.
	st2, eng2 := newTestEngine(t, dir)
	inst2, err := st2.GetInstance(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	def2, _ := st2.GetDefinition(inst2.DefinitionID)
	if inst2.Wait == nil || len(inst2.Wait.Received) != 1 {
		t.Fatal("重启后已有确认应保留")
	}
	if err := eng2.DeliverInput(def2, inst2, model.ExternalInput{WaitNode: "sign", Key: "k-b", From: "bob"}); err != nil {
		t.Fatal(err)
	}
	if err := eng2.DeliverInput(def2, inst2, model.ExternalInput{WaitNode: "sign", Key: "k-c", From: "carol"}); err != nil {
		t.Fatal(err)
	}
	if inst2.Status != model.StatusCompleted {
		t.Fatalf("重启后继续等待并集齐应完成, got %s", inst2.Status)
	}
	if n := countNodeAction(inst2.Records, "prep", "succeed"); n != 1 {
		t.Fatalf("已完成任务不能重复执行, succeed=%d", n)
	}
	if inst2.Wait.DecisionAuthID != "auth-1" {
		t.Fatal("决定应追到当时有效参与范围")
	}
}
