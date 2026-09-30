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

func TestHTTPResumeRunningInstanceAfterRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data-resume")
	h, st := setup(t, dir)
	nodes := []model.Node{
		{ID: "s", Type: model.NodeStart, Name: "开始"},
		{ID: "e", Type: model.NodeEnd, Name: "结束"},
	}
	edges := []model.Edge{{ID: "a", From: "s", To: "e"}}
	var def model.Definition
	do(t, h, "POST", "/api/definitions", map[string]any{"name": "p", "nodes": nodes, "edges": edges}, &def)
	var inst model.Instance
	do(t, h, "POST", "/api/instances", map[string]any{"definitionId": def.ID, "data": map[string]any{}},
		&inst)
	if inst.Status != model.StatusCompleted {
		t.Fatalf("start-to-end 实例应完成, got %s", inst.Status)
	}

	// Fabricate a "running" instance on disk, as if the process crashed mid-run.
	inst.Status = model.StatusRunning
	inst.CurrentNode = "s"
	inst.Records = nil
	inst.StoppedAt = ""
	if err := st.SaveInstance(&inst); err != nil {
		t.Fatal(err)
	}

	// Restart = fresh handler over the same directory, then resume.
	h2, _ := setup(t, dir)
	var resumed model.Instance
	if code := do(t, h2, "POST", "/api/instances/"+inst.ID+"/resume", nil, &resumed); code != 200 {
		t.Fatalf("resume code=%d", code)
	}
	if resumed.Status != model.StatusCompleted || resumed.StoppedAt != "e" {
		t.Fatalf("resume 应推进到结束, got status=%s stop=%s", resumed.Status, resumed.StoppedAt)
	}

	// Resuming a completed instance is a safe no-op.
	var again model.Instance
	do(t, h2, "POST", "/api/instances/"+inst.ID+"/resume", nil, &again)
	if again.Status != model.StatusCompleted {
		t.Fatalf("已完成实例 resume 必须是 no-op, got %s", again.Status)
	}
}
