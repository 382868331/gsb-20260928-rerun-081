package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
