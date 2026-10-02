package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"flowplatform/internal/api"
	"flowplatform/internal/engine"
	"flowplatform/internal/model"
	"flowplatform/internal/store"
)

func setup(t *testing.T, dir string) (http.Handler, *store.Store) {
	t.Helper()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	task := func(inst *model.Instance, n model.Node) error {
		if v, _ := inst.Data["failAt"].(string); v == n.ID {
			return errf("task failed")
		}
		return nil
	}
	comp := func(inst *model.Instance, n model.Node) error {
		if v, _ := inst.Data["compFailAt"].(string); v == n.ID {
			return errf("comp failed")
		}
		return nil
	}
	return api.NewHandler(st, engine.New(task, comp)), st
}

type e struct{ s string }

func (x e) Error() string { return x.s }
func errf(s string) error { return e{s} }

func do(t *testing.T, h http.Handler, method, path string, body any, into any) int {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if into != nil && w.Code < 300 {
		if err := json.Unmarshal(w.Body.Bytes(), into); err != nil {
			t.Fatalf("decode %s: %v body=%s", path, err, w.Body.String())
		}
	}
	return w.Code
}

func TestHTTPEndToEndAcrossRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	h, _ := setup(t, dir)

	nodes := []model.Node{
		{ID: "s", Type: model.NodeStart, Name: "开始"},
		{ID: "d", Type: model.NodeDecision, Name: "判断"},
		{ID: "t1", Type: model.NodeTask, Name: "任务1", CompensateTo: "t1"},
		{ID: "w", Type: model.NodeWait, Name: "人工"},
		{ID: "t2", Type: model.NodeTask, Name: "任务2", CompensateTo: "t2"},
		{ID: "e", Type: model.NodeEnd, Name: "结束"},
	}
	edges := []model.Edge{
		{ID: "a", From: "s", To: "d"},
		{ID: "b", From: "d", To: "t1", Condition: "amount >= 100"},
		{ID: "c", From: "t1", To: "w"},
		{ID: "dd", From: "w", To: "t2"},
		{ID: "ee", From: "t2", To: "e"},
		{ID: "f", From: "d", To: "e", Condition: "default"},
	}
	var def model.Definition
	if code := do(t, h, "POST", "/api/definitions", map[string]any{"name": "p", "nodes": nodes, "edges": edges}, &def); code != 201 {
		t.Fatalf("publish code=%d", code)
	}

	var inst model.Instance
	body := map[string]any{"definitionId": def.ID, "data": map[string]any{"amount": 200}}
	if code := do(t, h, "POST", "/api/instances", body, &inst); code != 201 {
		t.Fatalf("start code=%d", code)
	}
	if inst.Status != model.StatusWaiting || inst.WaitNode != "w" {
		t.Fatalf("应等待, got %s/%s", inst.Status, inst.WaitNode)
	}
	if inst.DefinitionID != def.ID {
		t.Fatal("实例必须固定定义版本")
	}

	// Simulate restart: brand new store + engine reading the same directory.
	h2, _ := setup(t, dir)
	input := map[string]any{"waitNode": "w", "key": "k1", "payload": map[string]any{"approved": true}}
	var advanced model.Instance
	if code := do(t, h2, "POST", "/api/instances/"+inst.ID+"/inputs", input, &advanced); code != 200 {
		t.Fatalf("input code=%d", code)
	}
	if advanced.Status != model.StatusCompleted {
		t.Fatalf("重启后输入应推进至完成, got %s", advanced.Status)
	}
	progressed := progressEvents(advanced.Records)

	// Duplicate key after completion: state unchanged.
	var dup model.Instance
	do(t, h2, "POST", "/api/instances/"+inst.ID+"/inputs", input, &dup)
	if dup.Status != model.StatusCompleted || progressEvents(dup.Records) != progressed {
		t.Fatalf("重复 key 不得再推进: status=%s progress=%d vs %d", dup.Status, progressEvents(dup.Records), progressed)
	}
}

func progressEvents(recs []model.StepRecord) int {
	n := 0
	for _, r := range recs {
		if r.Action == "traverse" || r.Action == "branch" || r.Action == "input_received" {
			n++
		}
	}
	return n
}

func TestHTTPFailedCompensationStopPoint(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data2")
	h, _ := setup(t, dir)
	nodes := []model.Node{
		{ID: "s", Type: model.NodeStart, Name: "开始"},
		{ID: "t1", Type: model.NodeTask, Name: "任务1", CompensateTo: "t1"},
		{ID: "t2", Type: model.NodeTask, Name: "任务2", CompensateTo: "t2"},
		{ID: "e", Type: model.NodeEnd, Name: "结束"},
	}
	edges := []model.Edge{
		{ID: "a", From: "s", To: "t1"},
		{ID: "b", From: "t1", To: "t2"},
		{ID: "c", From: "t2", To: "e"},
	}
	var def model.Definition
	do(t, h, "POST", "/api/definitions", map[string]any{"name": "p", "nodes": nodes, "edges": edges}, &def)
	var inst model.Instance
	do(t, h, "POST", "/api/instances", map[string]any{
		"definitionId": def.ID,
		"data":         map[string]any{"failAt": "t2", "compFailAt": "t1"},
	}, &inst)
	if inst.Status != model.StatusFailed || inst.StoppedAt != "t1" {
		t.Fatalf("补偿失败停点应为 t1, got status=%s stop=%s", inst.Status, inst.StoppedAt)
	}
}

func TestHTTPLateInputIsRecordedNotRejected(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data3")
	h, _ := setup(t, dir)
	nodes := []model.Node{
		{ID: "s", Type: model.NodeStart, Name: "开始"},
		{ID: "w", Type: model.NodeWait, Name: "人工"},
		{ID: "e", Type: model.NodeEnd, Name: "结束"},
	}
	edges := []model.Edge{
		{ID: "a", From: "s", To: "w"},
		{ID: "b", From: "w", To: "e"},
	}
	var def model.Definition
	do(t, h, "POST", "/api/definitions", map[string]any{"name": "p", "nodes": nodes, "edges": edges}, &def)
	var inst model.Instance
	do(t, h, "POST", "/api/instances", map[string]any{"definitionId": def.ID, "data": map[string]any{}}, &inst)

	var completed model.Instance
	do(t, h, "POST", "/api/instances/"+inst.ID+"/inputs", map[string]any{
		"waitNode": "w", "key": "k1", "payload": map[string]any{"x": 1},
	}, &completed)
	if completed.Status != model.StatusCompleted {
		t.Fatalf("first input should complete, got %s", completed.Status)
	}

	// A NEW key arriving late must NOT be a 400 and must NOT rerun anything.
	var late model.Instance
	if code := do(t, h, "POST", "/api/instances/"+inst.ID+"/inputs", map[string]any{
		"waitNode": "w", "key": "late-key", "payload": map[string]any{"x": 999},
	}, &late); code != 200 {
		t.Fatalf("late input should be accepted/ignored, code=%d", code)
	}
	if late.Status != model.StatusCompleted || late.CurrentNode != "e" {
		t.Fatalf("late input must not change state: %s/%s", late.Status, late.CurrentNode)
	}
	if _, ok := late.ConsumedInputs["late-key"]; ok {
		t.Fatal("late input must not be marked consumed")
	}
	if late.Data["x"] != float64(1) {
		t.Fatalf("late payload must not overwrite data: %v", late.Data["x"])
	}
	hasLate := false
	hasProgress := 0
	for _, r := range late.Records {
		if r.Action == "late_input" && r.NodeID == "w" {
			hasLate = true
		}
		if r.Action == "traverse" {
			hasProgress++
		}
	}
	if !hasLate || hasProgress != 2 {
		t.Fatalf("expected one late_input event and no new traverses beyond the two initial, got late=%v traverse=%d", hasLate, hasProgress)
	}
}

func TestHTTPResumeRunningInstance(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data4")
	h, st := setup(t, dir)
	nodes := []model.Node{
		{ID: "s", Type: model.NodeStart, Name: "开始"},
		{ID: "t1", Type: model.NodeTask, Name: "任务1"},
		{ID: "e", Type: model.NodeEnd, Name: "结束"},
	}
	edges := []model.Edge{{ID: "a", From: "s", To: "t1"}, {ID: "b", From: "t1", To: "e"}}
	var def model.Definition
	do(t, h, "POST", "/api/definitions", map[string]any{"name": "p", "nodes": nodes, "edges": edges}, &def)

	// Simulate an instance persisted mid-flight in running state (crash before reaching wait/end).
	inst, err := st.CreateInstance(&def, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	inst.CurrentNode = "t1"
	if err := st.SaveInstance(inst); err != nil {
		t.Fatal(err)
	}
	var resumed model.Instance
	if code := do(t, h, "POST", "/api/instances/"+inst.ID+"/resume", nil, &resumed); code != 200 {
		t.Fatalf("resume code=%d", code)
	}
	if resumed.Status != model.StatusCompleted || resumed.StoppedAt != "e" {
		t.Fatalf("resume should reach end, got %s/%s", resumed.Status, resumed.StoppedAt)
	}
}

func TestHTTPStartLatestByName(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data5")
	h, _ := setup(t, dir)
	nodesV1 := []model.Node{
		{ID: "s", Type: model.NodeStart, Name: "开始"},
		{ID: "e", Type: model.NodeEnd, Name: "结束"},
	}
	var v1 model.Definition
	do(t, h, "POST", "/api/definitions", map[string]any{"name": "order-demo", "nodes": nodesV1, "edges": []model.Edge{{ID: "a", From: "s", To: "e"}}}, &v1)
	var inst model.Instance
	if code := do(t, h, "POST", "/api/instances", map[string]any{"data": map[string]any{}}, &inst); code != 201 {
		t.Fatalf("start without definitionId should resolve latest order-demo, code=%d", code)
	}
	if inst.DefinitionID != v1.ID {
		t.Fatalf("expected pinned v1, got %s", inst.DefinitionID)
	}
}

func TestHTTPWaitingMigrationPreviewAndConfirm(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data6")
	h, _ := setup(t, dir)
	nodes := []model.Node{
		{ID: "s", Type: model.NodeStart, Name: "开始"},
		{ID: "w", Type: model.NodeWait, Name: "人工"},
		{ID: "t", Type: model.NodeTask, Name: "任务"},
		{ID: "e", Type: model.NodeEnd, Name: "结束"},
	}
	edges := []model.Edge{
		{ID: "a", From: "s", To: "w"},
		{ID: "b", From: "w", To: "t"},
		{ID: "c", From: "t", To: "e"},
	}
	var v1 model.Definition
	do(t, h, "POST", "/api/definitions", map[string]any{"name": "p", "nodes": nodes, "edges": edges}, &v1)
	var inst model.Instance
	do(t, h, "POST", "/api/instances", map[string]any{"definitionId": v1.ID, "data": map[string]any{}}, &inst)
	if inst.Status != model.StatusWaiting {
		t.Fatalf("应等待, got %s", inst.Status)
	}

	// v2 adds a new task after the wait node.
	v2Nodes := append(append([]model.Node{}, nodes...), model.Node{ID: "t2", Type: model.NodeTask, Name: "新任务"})
	v2Edges := []model.Edge{
		{ID: "a", From: "s", To: "w"},
		{ID: "d", From: "w", To: "t2"},
		{ID: "e2", From: "t2", To: "t"},
		{ID: "c", From: "t", To: "e"},
	}
	var v2 model.Definition
	do(t, h, "POST", "/api/definitions", map[string]any{"name": "p", "nodes": v2Nodes, "edges": v2Edges}, &v2)

	// Preview without confirm flag is allowed via GET and reports t2 ahead.
	req := httptest.NewRequest("GET", "/api/instances/"+inst.ID+"/migration?definitionId="+v2.ID, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("preview code=%d body=%s", w.Code, w.Body.String())
	}
	var preview struct {
		Impact struct {
			Affected []struct {
				NodeID string `json:"nodeId"`
			} `json:"affectedAhead"`
		} `json:"impact"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	hit := false
	for _, a := range preview.Impact.Affected {
		if a.NodeID == "t2" {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("预览应列出等待点之后新增的 t2: %s", w.Body.String())
	}

	// Confirm without flag must be rejected.
	if code := do(t, h, "POST", "/api/instances/"+inst.ID+"/migration", map[string]any{"definitionId": v2.ID}, nil); code != 400 {
		t.Fatalf("未确认迁移应被拒绝, code=%d", code)
	}

	var migrated struct {
		Instance model.Instance `json:"instance"`
	}
	if code := do(t, h, "POST", "/api/instances/"+inst.ID+"/migration", map[string]any{"definitionId": v2.ID, "confirm": true}, &migrated); code != 200 {
		t.Fatalf("confirm code=%d", code)
	}
	if migrated.Instance.DefinitionID != v2.ID || migrated.Instance.Version != 2 {
		t.Fatalf("迁移后应固定 v2, got %s v%d", migrated.Instance.DefinitionID, migrated.Instance.Version)
	}

	// Delivering input now runs under v2: both t2 and t run once, completes.
	var done model.Instance
	do(t, h, "POST", "/api/instances/"+inst.ID+"/inputs", map[string]any{"key": "k1", "payload": map[string]any{}},
		&done)
	if done.Status != model.StatusCompleted {
		t.Fatalf("迁移后应按新版本完成, got %s", done.Status)
	}
	counts := map[string]int{}
	for _, r := range done.Records {
		if r.Action == "succeed" {
			counts[r.NodeID]++
		}
	}
	if counts["t"] != 1 || counts["t2"] != 1 {
		t.Fatalf("新旧版本步骤各只能执行一次: %v", counts)
	}
}

func TestEmptyListsReturnEmptyArrays(t *testing.T) {
	h, _ := setup(t, filepath.Join(t.TempDir(), "data"))
	for _, path := range []string{"/api/definitions", "/api/instances"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: got %d", path, w.Code)
		}
		if strings.TrimSpace(w.Body.String()) != "[]" {
			t.Fatalf("%s: 空数据必须返回 [] 而不是 null, got %s", path, w.Body.String())
		}
	}
}

func TestHTTPCollaborativeWaitFlow(t *testing.T) {
	h, _ := setup(t, filepath.Join(t.TempDir(), "data"))
	nodes := []model.Node{
		{ID: "start", Type: model.NodeStart, Name: "开始"},
		{ID: "sign", Type: model.NodeWait, Name: "会签", Participants: []string{"a", "b"}, AllowDelegate: true},
		{ID: "end", Type: model.NodeEnd, Name: "结束"},
	}
	edges := []model.Edge{
		{ID: "e1", From: "start", To: "sign"},
		{ID: "e2", From: "sign", To: "end"},
	}
	var def model.Definition
	if c := do(t, h, "POST", "/api/definitions", map[string]any{"name": "collab", "nodes": nodes, "edges": edges}, &def); c != 201 {
		t.Fatalf("publish: %d", c)
	}
	var inst model.Instance
	if c := do(t, h, "POST", "/api/instances", map[string]any{"definitionId": def.ID, "data": map[string]any{}}, &inst); c != 201 {
		t.Fatalf("start: %d", c)
	}
	if inst.Wait == nil || inst.Status != model.StatusWaiting {
		t.Fatal("应进入协作等待")
	}
	// Partial input: still waiting.
	if c := do(t, h, "POST", "/api/instances/"+inst.ID+"/inputs", map[string]any{"waitNode": "sign", "key": "k1", "from": "a"}, &inst); c != 200 {
		t.Fatalf("input a: %d", c)
	}
	if inst.Status != model.StatusWaiting {
		t.Fatal("部分输入不能完成步骤")
	}
	// Delegate b -> c, then c confirms.
	if c := do(t, h, "POST", "/api/instances/"+inst.ID+"/wait/scope", map[string]any{
		"grantDelegations": map[string]string{"b": "c"}, "keepPriorInputs": true, "reason": "b 委托 c",
	}, &inst); c != 200 {
		t.Fatalf("scope: %d", c)
	}
	if len(inst.Wait.Authorizations) != 2 {
		t.Fatal("新旧授权记录都应保留")
	}
	if c := do(t, h, "POST", "/api/instances/"+inst.ID+"/inputs", map[string]any{"waitNode": "sign", "key": "k2", "from": "c"}, &inst); c != 200 {
		t.Fatalf("input c: %d", c)
	}
	if inst.Status != model.StatusCompleted || inst.Wait.DecisionAuthID != "auth-2" {
		t.Fatalf("应按当时有效范围完成, got %s/%s", inst.Status, inst.Wait.DecisionAuthID)
	}
	// Timeout endpoint after completion is a no-op.
	if c := do(t, h, "POST", "/api/instances/"+inst.ID+"/timeout", nil, &inst); c != 200 {
		t.Fatalf("timeout: %d", c)
	}
	if inst.Status != model.StatusCompleted {
		t.Fatal("已有决定后超时判定不得改变结果")
	}
}
