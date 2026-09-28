package engine

import (
	"path/filepath"
	"testing"
	"time"

	"workflow/internal/model"
	"workflow/internal/store"
)

func testDef() *model.Definition {
	return &model.Definition{
		Key:       "wf",
		Version:   1,
		Name:      "测试流程",
		CreatedAt: time.Now(),
		Nodes: []model.Node{
			{ID: "s", Kind: model.KindStart, Name: "开始", Pos: model.Position{X: 0, Y: 0}},
			{ID: "c", Kind: model.KindCondition, Name: "判断", Condition: "amount >= 100", Pos: model.Position{X: 0, Y: 0}},
			{ID: "w", Kind: model.KindWait, Name: "等待审批", InputKey: "approval", Pos: model.Position{X: 0, Y: 0}},
			{ID: "t", Kind: model.KindTask, Name: "主任务", Compensate: "cp", Pos: model.Position{X: 0, Y: 0}},
			{ID: "cp", Kind: model.KindTask, Name: "补偿", Pos: model.Position{X: 0, Y: 0}},
			{ID: "e", Kind: model.KindEnd, Name: "结束", Pos: model.Position{X: 0, Y: 0}},
		},
		Edges: []model.Edge{
			{ID: "a", From: "s", To: "c"},
			{ID: "b1", From: "c", To: "w", Branch: model.BranchTrue},
			{ID: "b2", From: "c", To: "t", Branch: model.BranchFalse},
			{ID: "d", From: "w", To: "t"},
			{ID: "f", From: "t", To: "e"},
		},
	}
}

func newTestEngine(t *testing.T) (*Engine, *store.Store) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SaveDefinition(testDef()); err != nil {
		t.Fatal(err)
	}
	return New(st), st
}

func TestConditionFalseRunsToCompletion(t *testing.T) {
	eng, _ := newTestEngine(t)
	inst, err := eng.StartInstance("wf", map[string]any{"amount": float64(50)})
	if err != nil {
		t.Fatal(err)
	}
	if inst.Status != model.StatusCompleted {
		t.Fatalf("want completed, got %s", inst.Status)
	}
	edgeSeq := edgeIDs(inst)
	want := []string{"a", "b2", "f"}
	if len(edgeSeq) != len(want) {
		t.Fatalf("流转记录 = %v, want %v", edgeSeq, want)
	}
	for i := range want {
		if edgeSeq[i] != want[i] {
			t.Fatalf("流转顺序 = %v, want %v", edgeSeq, want)
		}
	}
}

func TestWaitInputIsIdempotentAndContinues(t *testing.T) {
	eng, st := newTestEngine(t)
	inst, err := eng.StartInstance("wf", map[string]any{"amount": float64(200)})
	if err != nil {
		t.Fatal(err)
	}
	if inst.Status != model.StatusWaiting || inst.WaitingFor["w"] != "approval" {
		t.Fatalf("实例应挂起在等待节点, status=%s waiting=%v", inst.Status, inst.WaitingFor)
	}

	// 同一幂等键重复提交两次：只推进一次。
	_, err = eng.SubmitInput(inst.ID, "w", "key-1", map[string]any{"approve": true})
	if err != nil {
		t.Fatal(err)
	}
	inst2, err := eng.SubmitInput(inst.ID, "w", "key-1", map[string]any{"approve": true})
	if err != nil {
		t.Fatal(err)
	}
	if inst2.Status != model.StatusCompleted {
		t.Fatalf("重复提交后应保持 completed, got %s", inst2.Status)
	}
	valid, dup := 0, 0
	for _, in := range inst2.Inputs {
		if in.Duplicate {
			dup++
		} else {
			valid++
		}
	}
	if valid != 1 || dup != 1 {
		t.Fatalf("有效输入=%d 重复输入=%d, want 1/1", valid, dup)
	}

	// 从磁盘重新加载，状态完整。
	reloaded, err := st.GetInstance(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != model.StatusCompleted || len(reloaded.Transitions) != 4 {
		t.Fatalf("落盘状态错误: status=%s transitions=%d", reloaded.Status, len(reloaded.Transitions))
	}
}

func TestCompensationFailureStopsAtCompensationNode(t *testing.T) {
	eng, _ := newTestEngine(t)
	// 走 false 分支直接到 t；主任务失败，补偿节点也失败。
	inst, err := eng.StartInstance("wf", map[string]any{
		"amount":    float64(1),
		"failTasks": "t",
		"failComp":  "cp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if inst.Status != model.StatusCompFailed {
		t.Fatalf("want compensation_failed, got %s", inst.Status)
	}
	if len(inst.Current) != 1 || inst.Current[0] != "cp" {
		t.Fatalf("实例应真实停在补偿节点 cp, got %v", inst.Current)
	}
	last := inst.Steps[len(inst.Steps)-1]
	if last.NodeID != "cp" || !last.Comp || last.Result != "error" {
		t.Fatalf("最后一步不是失败的补偿记录: %+v", last)
	}
	var compTrans *model.TransitionRecord
	for i := range inst.Transitions {
		if inst.Transitions[i].Comp {
			compTrans = &inst.Transitions[i]
		}
	}
	if compTrans == nil || compTrans.From != "t" || compTrans.To != "cp" {
		t.Fatalf("缺少 t->cp 的补偿流转记录")
	}
}

func TestCompensationSuccessMarksFailed(t *testing.T) {
	eng, _ := newTestEngine(t)
	inst, err := eng.StartInstance("wf", map[string]any{
		"amount":    float64(1),
		"failTasks": "t",
	})
	if err != nil {
		t.Fatal(err)
	}
	if inst.Status != model.StatusFailed {
		t.Fatalf("want failed, got %s", inst.Status)
	}
	if inst.Error == "" {
		t.Fatal("应保留原始失败原因")
	}
}

func TestNewVersionDoesNotChangeRunningInstance(t *testing.T) {
	eng, st := newTestEngine(t)
	inst, err := eng.StartInstance("wf", map[string]any{"amount": float64(200)})
	if err != nil {
		t.Fatal(err)
	}

	// 发布一个节点完全不同的 v2，老实例仍持有 v1 快照。
	v2 := testDef()
	v2.Nodes = []model.Node{
		{ID: "s", Kind: model.KindStart, Name: "开始"},
		{ID: "x", Kind: model.KindTask, Name: "新任务"},
		{ID: "e", Kind: model.KindEnd, Name: "结束"},
	}
	v2.Edges = []model.Edge{
		{ID: "g1", From: "s", To: "x"},
		{ID: "g2", From: "x", To: "e"},
	}
	v2.Version = 2
	if err := st.SaveDefinition(v2); err != nil {
		t.Fatal(err)
	}
	if got := inst.Definition.Nodes[2].ID; got != "w" {
		t.Fatalf("老实例定义被污染: %s", got)
	}
	if _, err := eng.SubmitInput(inst.ID, "w", "k", "ok"); err != nil {
		t.Fatalf("老实例应仍能按 v1 继续: %v", err)
	}

	// 新实例使用 v2。
	inst2, err := eng.StartInstance("wf", nil)
	if err != nil {
		t.Fatal(err)
	}
	if inst2.Version != 2 || inst2.Status != model.StatusCompleted {
		t.Fatalf("新实例应跑 v2 并完成: v=%d status=%s", inst2.Version, inst2.Status)
	}
}

func TestResumeRestartContinuesInstance(t *testing.T) {
	eng, st := newTestEngine(t)
	inst, err := eng.StartInstance("wf", map[string]any{"amount": float64(1), "failTasks": "t"})
	if err != nil {
		t.Fatal(err)
	}
	if inst.Status != model.StatusFailed {
		t.Fatalf("前置状态错误: %s", inst.Status)
	}

	// waiting 状态跨重启应保持挂起。
	w, err := eng.StartInstance("wf", map[string]any{"amount": float64(200)})
	if err != nil {
		t.Fatal(err)
	}
	eng2 := New(st)
	insts, err := st.ListInstances()
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range insts {
		if err := eng2.Resume(in); err != nil {
			t.Fatal(err)
		}
	}
	reloaded, err := st.GetInstance(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != model.StatusWaiting || reloaded.WaitingFor["w"] != "approval" {
		t.Fatalf("重启后等待实例应继续挂起: %s %v", reloaded.Status, reloaded.WaitingFor)
	}
}

func edgeIDs(inst *model.Instance) []string {
	ids := make([]string, 0, len(inst.Transitions))
	for _, tr := range inst.Transitions {
		ids = append(ids, tr.EdgeID)
	}
	return ids
}
