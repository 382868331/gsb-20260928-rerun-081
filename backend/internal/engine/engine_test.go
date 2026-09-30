package engine_test

import (
	"path/filepath"
	"testing"

	"flowplatform/internal/engine"
	"flowplatform/internal/model"
	"flowplatform/internal/store"
)

func sampleNodes() ([]model.Node, []model.Edge) {
	nodes := []model.Node{
		{ID: "start", Type: model.NodeStart, Name: "开始"},
		{ID: "review", Type: model.NodeDecision, Name: "金额审核", X: 200},
		{ID: "reserve", Type: model.NodeTask, Name: "扣减库存", CompensateTo: "reserve", X: 400},
		{ID: "manual", Type: model.NodeWait, Name: "等待人工确认", X: 600},
		{ID: "ship", Type: model.NodeTask, Name: "发货", CompensateTo: "ship", X: 800},
		{ID: "end", Type: model.NodeEnd, Name: "结束", X: 1000},
	}
	edges := []model.Edge{
		{ID: "e1", From: "start", To: "review"},
		{ID: "e2", From: "review", To: "reserve", Condition: `amount >= 100`},
		{ID: "e3", From: "reserve", To: "manual"},
		{ID: "e4", From: "manual", To: "ship"},
		{ID: "e5", From: "ship", To: "end"},
	}
	return nodes, edges
}

func newTestEngine(t *testing.T, dir string) (*store.Store, *engine.Engine) {
	t.Helper()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	task := func(inst *model.Instance, n model.Node) error {
		if v, _ := inst.Data["failAt"].(string); v == n.ID {
			return errSimple("fail " + n.ID)
		}
		return nil
	}
	comp := func(inst *model.Instance, n model.Node) error {
		if v, _ := inst.Data["compFailAt"].(string); v == n.ID {
			return errSimple("comp fail " + n.ID)
		}
		return nil
	}
	return st, engine.New(task, comp)
}

func TestWaitIdempotentInputAndComplete(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st, eng := newTestEngine(t, dir)
	nodes, edges := sampleNodes()
	def, err := st.PublishDraft("order", nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := st.CreateInstance(def, map[string]any{"amount": float64(100)})
	if err != nil {
		t.Fatal(err)
	}
	eng.Run(def, inst)
	if inst.Status != model.StatusWaiting || inst.WaitNode != "manual" {
		t.Fatalf("应停在等待节点, got %s/%s", inst.Status, inst.WaitNode)
	}
	if err := st.SaveInstance(inst); err != nil {
		t.Fatal(err)
	}

	// Simulate process restart: reload store/engine from disk.
	st2, eng2 := newTestEngine(t, dir)
	inst2, err := st2.GetInstance(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	def2, _ := st2.GetDefinition(inst2.DefinitionID)
	input := model.ExternalInput{WaitNode: "manual", Key: "k-1", Payload: map[string]any{"approved": true}}
	if err := eng2.DeliverInput(def2, inst2, input); err != nil {
		t.Fatal(err)
	}
	if inst2.Status != model.StatusCompleted {
		t.Fatalf("应完成, got %s", inst2.Status)
	}
	if err := st2.SaveInstance(inst2); err != nil {
		t.Fatal(err)
	}

	// Re-submit the SAME key after completion: accepted but never re-runs.
	inst3, _ := st2.GetInstance(inst.ID)
	if err := eng2.DeliverInput(def2, inst3, input); err != nil {
		t.Fatalf("重复 key 应幂等忽略: %v", err)
	}
	if inst3.Status != model.StatusCompleted || inst3.CurrentNode != "end" {
		t.Fatal("重复 key 不能改变实例状态")
	}
	// While waiting, duplicate key adds only a duplicate record, no advance:
	inst4, _ := st.CreateInstance(def, map[string]any{"amount": float64(100)})
	eng2.Run(def, inst4)
	before := len(inst4.Records)
	if err := eng2.DeliverInput(def, inst4, model.ExternalInput{Key: "dup"}); err != nil {
		t.Fatal(err)
	}
	if err := eng2.DeliverInput(def, inst4, model.ExternalInput{Key: "dup"}); err != nil {
		t.Fatal(err)
	}
	if inst4.Status != model.StatusCompleted {
		t.Fatal("重复提交后首次输入仍应推进到完成")
	}
	if got := countAction(inst4.Records, "input_received"); got != 1 {
		t.Fatalf("同一 key 只能生效一次, input_received=%d", got)
	}
	if len(inst4.Records) <= before {
		t.Fatal("执行记录应增长")
	}
}

func TestNewVersionDoesNotChangeRunningInstance(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st, eng := newTestEngine(t, dir)
	nodes, edges := sampleNodes()
	v1, err := st.PublishDraft("order", nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	inst, _ := st.CreateInstance(v1, map[string]any{"amount": float64(100)})
	eng.Run(v1, inst)
	_ = st.SaveInstance(inst)

	// Publish v2 with changed graph.
	nodes = append(nodes, model.Node{ID: "extra", Type: model.NodeTask, Name: "新增步骤"})
	v2, err := st.PublishDraft("order", nodes, edges)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Version != 2 {
		t.Fatalf("新版本应为 2, got %d", v2.Version)
	}
	reloaded, _ := st.GetInstance(inst.ID)
	if reloaded.DefinitionID != v1.ID {
		t.Fatal("运行中实例必须固定旧定义")
	}
	pinned, _ := st.GetDefinition(reloaded.DefinitionID)
	if len(pinned.Nodes) != len(v1.Nodes) {
		t.Fatal("旧定义被发布操作修改")
	}
}

func TestFailedCompensationRecordsTrueStopNode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st, eng := newTestEngine(t, dir)
	nodes, edges := sampleNodes()
	def, _ := st.PublishDraft("order", nodes, edges)
	// ship fails; compensation runs back through ship? ship never succeeded,
	// so reserve compensates and is forced to fail -> stop recorded at reserve.
	inst, _ := st.CreateInstance(def, map[string]any{
		"amount":     float64(100),
		"failAt":     "ship",
		"compFailAt": "reserve",
	})
	eng.Run(def, inst)
	if err := eng.DeliverInput(def, inst, model.ExternalInput{Key: "go"}); err != nil {
		t.Fatal(err)
	}
	if inst.Status != model.StatusFailed {
		t.Fatalf("应失败, got %s", inst.Status)
	}
	if inst.StoppedAt != "reserve" {
		t.Fatalf("补偿失败时真实停点应为 reserve, got %s", inst.StoppedAt)
	}
	last := inst.Records[len(inst.Records)-1]
	if last.Status != model.NodeCompFailed || last.NodeID != "reserve" {
		t.Fatalf("最后一条记录应为 reserve 补偿失败, got %+v", last)
	}
}

func TestConditionBranchFalsePath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st, eng := newTestEngine(t, dir)
	nodes, edges := sampleNodes()
	// add small-amount default branch to end
	edges = append(edges, model.Edge{ID: "e6", From: "review", To: "end", Condition: "default", Label: "小额免审"})
	def, _ := st.PublishDraft("order", nodes, edges)
	inst, _ := st.CreateInstance(def, map[string]any{"amount": float64(1)})
	eng.Run(def, inst)
	if inst.Status != model.StatusCompleted {
		t.Fatalf("默认分支应直达结束, got %s", inst.Status)
	}
	if got := countAction(inst.Records, "branch"); got == 0 {
		t.Fatal("条件分支应记录 branch 事件")
	}
}

func TestMigrateWaitingInstanceKeepsHistoryContinuous(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st, eng := newTestEngine(t, dir)
	nodes, edges := sampleNodes()
	v1, _ := st.PublishDraft("order", nodes, edges)
	inst, _ := st.CreateInstance(v1, map[string]any{"amount": float64(100)})
	eng.Run(v1, inst)
	if inst.Status != model.StatusWaiting {
		t.Fatalf("应等待, got %s", inst.Status)
	}
	before := len(inst.Records)

	// v2 adds an extra task after ship but keeps the wait node.
	nodes2 := append(append([]model.Node{}, nodes...), model.Node{ID: "extra", Type: model.NodeTask, Name: "额外步骤"})
	var edges2 []model.Edge
	for _, e := range edges {
		if e.ID != "e5" { // replace ship->end with ship->extra->end
			edges2 = append(edges2, e)
		}
	}
	edges2 = append(edges2,
		model.Edge{ID: "e6", From: "ship", To: "extra"},
		model.Edge{ID: "e7", From: "extra", To: "end"})
	v2, err := st.PublishDraft("order", nodes2, edges2)
	if err != nil {
		t.Fatal(err)
	}

	prev := engine.PreviewMigration(inst, v1, v2)
	if !prev.CanMigrate {
		t.Fatalf("等待节点保留时应可迁移: %+v", prev.AffectedSteps)
	}
	eng.Migrate(inst, v1, v2)
	if inst.DefinitionID != v2.ID || inst.Version != 2 {
		t.Fatal("迁移后应固定到新定义")
	}
	if len(inst.Records) != before+1 || inst.Records[len(inst.Records)-1].Action != "migrate" {
		t.Fatal("迁移只能追加一条 migrate 事件，历史不得拼接")
	}
	// Input after migration follows the NEW definition and completes.
	if err := eng.DeliverInput(v2, inst, model.ExternalInput{Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	if inst.Status != model.StatusCompleted {
		t.Fatalf("迁移后应按新定义推进至完成, got %s", inst.Status)
	}
	if got := countAction(inst.Records, "succeed"); got != 3 { // reserve, ship, extra
		t.Fatalf("新旧定义历史不得拼接重放, succeed=%d", got)
	}
}

func TestMigrateBlockedWhenWaitNodeRemoved(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st, eng := newTestEngine(t, dir)
	nodes, edges := sampleNodes()
	v1, _ := st.PublishDraft("order", nodes, edges)
	inst, _ := st.CreateInstance(v1, map[string]any{"amount": float64(100)})
	eng.Run(v1, inst)

	// v2 removes the wait node entirely.
	var nodes2 []model.Node
	for _, n := range nodes {
		if n.ID != "manual" {
			nodes2 = append(nodes2, n)
		}
	}
	var edges2 []model.Edge
	for _, e := range edges {
		if e.From != "manual" && e.To != "manual" {
			edges2 = append(edges2, e)
		}
	}
	edges2 = append(edges2, model.Edge{ID: "e9", From: "reserve", To: "ship"})
	v2, err := st.PublishDraft("order", nodes2, edges2)
	if err != nil {
		t.Fatal(err)
	}
	prev := engine.PreviewMigration(inst, v1, v2)
	if prev.CanMigrate {
		t.Fatal("等待节点被删除时必须阻断迁移")
	}
	hasBlocking := false
	for _, s := range prev.AffectedSteps {
		if s.Blocking && s.NodeID == "manual" {
			hasBlocking = true
		}
	}
	if !hasBlocking {
		t.Fatal("预览必须列出被删除的等待步骤为阻断项")
	}
}

func TestResumeDoesNotRedoSucceededStep(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st, eng := newTestEngine(t, dir)
	nodes, edges := sampleNodes()
	def, _ := st.PublishDraft("order", nodes, edges)
	inst, _ := st.CreateInstance(def, map[string]any{"amount": float64(100)})
	eng.Run(def, inst)
	// Simulate crash persisted mid-flight: instance still "running" parked on
	// a node that already has a succeed record.
	inst.Status = model.StatusRunning
	inst.CurrentNode = "reserve"
	eng.Resume(def, inst)
	if got := countAction(inst.Records, "succeed"); got != 1 {
		t.Fatalf("重开继续不得重复完成同一步, succeed=%d", got)
	}
	if countAction(inst.Records, "skip_redone") != 1 {
		t.Fatal("应记录 skip_redone 事件")
	}
	if inst.Status != model.StatusWaiting {
		t.Fatalf("应继续推进到等待, got %s", inst.Status)
	}
}

func TestResumeFinishesInterruptedCompensation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st, eng := newTestEngine(t, dir)
	nodes, edges := sampleNodes()
	def, _ := st.PublishDraft("order", nodes, edges)
	inst, _ := st.CreateInstance(def, map[string]any{"amount": float64(100), "failAt": "ship"})
	eng.Run(def, inst)
	if err := eng.DeliverInput(def, inst, model.ExternalInput{Key: "go"}); err != nil {
		t.Fatal(err)
	}
	if inst.Status != model.StatusFailed {
		t.Fatalf("应失败, got %s", inst.Status)
	}
	// Simulate crash mid-compensation: strip terminal record, back to running.
	var kept []model.StepRecord
	for _, r := range inst.Records {
		if r.Action != "compensate_done" {
			kept = append(kept, r)
		}
	}
	inst.Records = kept
	inst.Status = model.StatusRunning
	compBefore := countAction(inst.Records, "compensated")
	eng.Resume(def, inst)
	if inst.Status != model.StatusFailed {
		t.Fatalf("补偿续跑后应失败, got %s", inst.Status)
	}
	if got := countAction(inst.Records, "compensated"); got != compBefore {
		t.Fatalf("同一步不得重复补偿, compensated=%d want %d", got, compBefore)
	}
	if countAction(inst.Records, "compensate_done") != 1 {
		t.Fatal("补偿应补跑到终态 compensate_done")
	}
}

func countAction(recs []model.StepRecord, action string) int {
	n := 0
	for _, r := range recs {
		if r.Action == action {
			n++
		}
	}
	return n
}

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }

func errSimple(s string) error { return &simpleErr{msg: s} }
