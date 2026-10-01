package engine_test

import (
	"path/filepath"
	"testing"
	"time"

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

func TestCompensationRestartDoesNotRedoCompletedCompensations(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st, eng := newTestEngine(t, dir)
	nodes, edges := sampleNodes()
	def, _ := st.PublishDraft("order", nodes, edges)
	// ship fails; reserve compensates successfully.
	inst, _ := st.CreateInstance(def, map[string]any{
		"amount": float64(100),
		"failAt": "ship",
	})
	eng.Run(def, inst)
	if err := eng.DeliverInput(def, inst, model.ExternalInput{Key: "go"}); err != nil {
		t.Fatal(err)
	}
	if inst.Status != model.StatusFailed {
		t.Fatalf("应失败, got %s", inst.Status)
	}
	if got := countAction(inst.Records, "compensated"); got != 1 {
		t.Fatalf("reserve 应只补偿一次, got %d", got)
	}
	if inst.Compensating {
		t.Fatal("补偿完成后应退出补偿阶段")
	}

	// Simulate a crash mid-compensation: journal says reserve already done.
	crashed, _ := st.CreateInstance(def, map[string]any{"amount": float64(100), "failAt": "ship"})
	eng.Run(def, crashed)
	_ = eng.DeliverInput(def, crashed, model.ExternalInput{Key: "go2"})
	// Rewind to just after reserve compensation was journaled:
	var cut int
	for i, rec := range crashed.Records {
		if rec.Action == "compensated" {
			cut = i + 1
		}
	}
	crashed.Records = crashed.Records[:cut]
	crashed.Status = model.StatusRunning
	crashed.Compensating = true
	crashed.FailedNode = "ship"
	if err := st.SaveInstance(crashed); err != nil {
		t.Fatal(err)
	}

	st2, eng2 := newTestEngine(t, dir)
	restarted, _ := st2.GetInstance(crashed.ID)
	def2, _ := st2.GetDefinition(restarted.DefinitionID)
	eng2.Resume(def2, restarted)
	if restarted.Status != model.StatusFailed || restarted.StoppedAt != "ship" {
		t.Fatalf("续跑补偿后应失败停在 ship, got %s/%s", restarted.Status, restarted.StoppedAt)
	}
	if got := countAction(restarted.Records, "compensated"); got != 1 {
		t.Fatalf("重启不得重复完成 reserve 补偿, compensated=%d", got)
	}
}

func TestWaitingInstanceMigrationKeepsOldHistory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st, eng := newTestEngine(t, dir)
	nodes, edges := sampleNodes()
	v1, _ := st.PublishDraft("order", nodes, edges)
	inst, _ := st.CreateInstance(v1, map[string]any{"amount": float64(100)})
	eng.Run(v1, inst)
	_ = st.SaveInstance(inst)
	if inst.Status != model.StatusWaiting || inst.WaitNode != "manual" {
		t.Fatalf("应等待在 manual, got %s/%s", inst.Status, inst.WaitNode)
	}
	oldRecords := len(inst.Records)

	// v2 adds a new task between manual and ship.
	newEdges := []model.Edge{
		{ID: "e1", From: "start", To: "review"},
		{ID: "e2", From: "review", To: "reserve", Condition: `amount >= 100`},
		{ID: "e3", From: "reserve", To: "manual"},
		{ID: "e7", From: "manual", To: "pack"},
		{ID: "e4", From: "pack", To: "ship"},
		{ID: "e5", From: "ship", To: "end"},
	}
	newNodes := append(append([]model.Node{}, nodes...), model.Node{ID: "pack", Type: model.NodeTask, Name: "打包", X: 700, Y: 200})
	v2, err := st.PublishDraft("order", newNodes, newEdges)
	if err != nil {
		t.Fatal(err)
	}

	from, _ := st.GetDefinition(inst.DefinitionID)
	impact, err := engine.PreviewMigration(inst, from, v2)
	if err != nil {
		t.Fatal(err)
	}
	foundPack := false
	for _, a := range impact.AffectedAhead {
		if a.NodeID == "pack" {
			foundPack = true
		}
	}
	if !foundPack {
		fatalf(t, "迁移影响应包含等待点之后的新步骤 pack: %+v", impact.AffectedAhead)
	}

	if _, err := eng.MigrateWaiting(inst, from, v2); err != nil {
		t.Fatal(err)
	}
	if inst.DefinitionID != v2.ID || inst.Version != 2 {
		t.Fatal("迁移后应固定新版本")
	}
	// All pre-migration events stay identical; history is not spliced with v2.
	for i := 0; i < oldRecords; i++ {
		if inst.Records[i].Action == "definition_migrated" {
			t.Fatal("迁移记录不得插入旧历史之前")
		}
	}
	// Continue under v2: the new pack task must run exactly once.
	if err := eng.DeliverInput(v2, inst, model.ExternalInput{Key: "go"}); err != nil {
		t.Fatal(err)
	}
	if inst.Status != model.StatusCompleted {
		t.Fatalf("迁移后输入应按新版本完成, got %s", inst.Status)
	}
	var packSucceed int
	for _, rec := range inst.Records {
		if rec.NodeID == "pack" && rec.Action == "succeed" {
			packSucceed++
		}
	}
	if packSucceed != 1 {
		t.Fatalf("新步骤 pack 应且仅应执行一次, got %d", packSucceed)
	}
}

func TestCannotMigrateWhenWaitNodeMissingInNewVersion(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st, eng := newTestEngine(t, dir)
	nodes, edges := sampleNodes()
	v1, _ := st.PublishDraft("order", nodes, edges)
	inst, _ := st.CreateInstance(v1, map[string]any{"amount": float64(100)})
	eng.Run(v1, inst)

	var v2Nodes []model.Node
	for _, n := range nodes {
		if n.ID != "manual" {
			v2Nodes = append(v2Nodes, n)
		}
	}
	var v2Edges []model.Edge
	for _, ed := range edges {
		if ed.From != "manual" && ed.To != "manual" {
			v2Edges = append(v2Edges, ed)
		}
	}
	v2Edges = append(v2Edges, model.Edge{ID: "ex", From: "reserve", To: "ship"})
	v2, _ := st.PublishDraft("order", v2Nodes, v2Edges)
	if _, err := eng.MigrateWaiting(inst, v1, v2); err == nil {
		t.Fatal("等待节点在新版本缺失时必须拒绝迁移")
	}
	if inst.DefinitionID != v1.ID {
		t.Fatal("被拒绝的迁移不得改变实例固定版本")
	}
}

func TestResumeAfterCrashBetweenTaskSuccessAndAdvance(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st, _ := newTestEngine(t, dir)
	nodes := []model.Node{
		{ID: "s", Type: model.NodeStart, Name: "开始"},
		{ID: "t1", Type: model.NodeTask, Name: "任务1"},
		{ID: "t2", Type: model.NodeTask, Name: "任务2"},
		{ID: "e", Type: model.NodeEnd, Name: "结束"},
	}
	edges := []model.Edge{
		{ID: "a", From: "s", To: "t1"},
		{ID: "b", From: "t1", To: "t2"},
		{ID: "c", From: "t2", To: "e"},
	}
	def, _ := st.PublishDraft("order", nodes, edges)

	ran := map[string]bool{}
	eng := engine.New(
		func(inst *model.Instance, n model.Node) error {
			if ran[n.ID] {
				t.Errorf("任务 %s 在重启恢复后被重复执行", n.ID)
			}
			ran[n.ID] = true
			return nil
		},
		func(inst *model.Instance, n model.Node) error { return nil },
	)
	inst, _ := st.CreateInstance(def, map[string]any{})
	// Fabricate the on-disk state of a crash right after t1's succeed record
	// was appended but before its advance persisted: CurrentNode still t1.
	inst.CurrentNode = "t1"
	inst.Status = model.StatusRunning
	now := func() time.Time { return time.Now().UTC() }
	inst.Records = append(inst.Records,
		model.StepRecord{Seq: 1, NodeID: "s", Action: "start", Status: model.NodeEntered, OccurredAt: now()},
		model.StepRecord{Seq: 2, NodeID: "t1", EdgeID: "a", Action: "traverse", Status: model.NodeEntered, OccurredAt: now()},
		model.StepRecord{Seq: 3, NodeID: "t1", Action: "enter", Status: model.NodeEntered, OccurredAt: now()},
		model.StepRecord{Seq: 4, NodeID: "t1", Action: "succeed", Status: model.NodeSucceeded, OccurredAt: now()},
	)
	if err := st.SaveInstance(inst); err != nil {
		t.Fatal(err)
	}

	st2, _ := newTestEngine(t, dir)
	restarted, _ := st2.GetInstance(inst.ID)
	eng.Resume(def, restarted)
	if restarted.Status != model.StatusCompleted || restarted.StoppedAt != "e" {
		t.Fatalf("恢复后应完成, got %s/%s", restarted.Status, restarted.StoppedAt)
	}
	for _, id := range []string{"t1", "t2"} {
		if n := countNodeAction(restarted.Records, id, "succeed"); n != 1 {
			t.Fatalf("任务 %s 应且仅应成功一次, got %d", id, n)
		}
	}
}

func countNodeAction(recs []model.StepRecord, nodeID, action string) int {
	n := 0
	for _, r := range recs {
		if r.NodeID == nodeID && r.Action == action {
			n++
		}
	}
	return n
}

func fatalf(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Fatalf(format, args...)
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
