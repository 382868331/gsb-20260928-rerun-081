package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"workflow/internal/engine"
	"workflow/internal/model"
	"workflow/internal/store"
)

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	eng := engine.New(st)
	return New(st, eng), st
}

func doJSON(t *testing.T, h *Server, method, path string, body any) (map[string]any, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)
	raw := rec.Body.Bytes()
	var m map[string]any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("响应不是 JSON: %s", raw)
		}
	}
	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("%s %s -> %d %s", method, path, rec.Code, raw)
	}
	return m, raw
}

func TestFullLifecycleWithRestartAndCompFailure(t *testing.T) {
	h, st := newTestServer(t)

	// 1. 发布定义 v1
	def := map[string]any{
		"key":  "pay",
		"name": "支付流程",
		"nodes": []map[string]any{
			{"id": "s", "kind": "start", "name": "开始", "pos": map[string]int{"x": 0, "y": 0}},
			{"id": "cond", "kind": "condition", "name": "校验", "condition": "vip == true", "pos": map[string]int{"x": 0, "y": 0}},
			{"id": "w", "kind": "wait", "name": "等确认", "inputKey": "confirm", "pos": map[string]int{"x": 0, "y": 0}},
			{"id": "pay", "kind": "task", "name": "支付", "compensate": "undo", "pos": map[string]int{"x": 0, "y": 0}},
			{"id": "undo", "kind": "task", "name": "撤销支付", "pos": map[string]int{"x": 0, "y": 0}},
			{"id": "e", "kind": "end", "name": "结束", "pos": map[string]int{"x": 0, "y": 0}},
		},
		"edges": []map[string]any{
			{"id": "a", "from": "s", "to": "cond"},
			{"id": "b1", "from": "cond", "to": "w", "branch": "true"},
			{"id": "b2", "from": "cond", "to": "pay", "branch": "false"},
			{"id": "c", "from": "w", "to": "pay"},
			{"id": "d", "from": "pay", "to": "e"},
		},
	}
	res, _ := doJSON(t, h, "POST", "/api/definitions", def)
	if res["version"].(float64) != 1 {
		t.Fatalf("首版应为 1, got %v", res["version"])
	}

	// 2. 再发布一次相同内容 → 版本递增为 2
	res2, _ := doJSON(t, h, "POST", "/api/definitions", def)
	if res2["version"].(float64) != 2 {
		t.Fatalf("重复发布应产生 v2, got %v", res2["version"])
	}

	// 3. 启动一个会挂起的实例（vip=true）
	startBody, _ := doJSON(t, h, "POST", "/api/instances", map[string]any{
		"defKey": "pay",
		"input":  map[string]any{"vip": true},
	})
	waitingID := startBody["id"].(string)
	if startBody["status"] != "waiting" {
		t.Fatalf("应等待输入, got %v", startBody["status"])
	}

	// 4. “重启”：新建 store/engine 指向同一数据目录，等待实例仍挂起
	eng2 := engine.New(st)
	insts, err := st.ListInstances()
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range insts {
		if err := eng2.Resume(in); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := doJSON(t, h, "GET", "/api/instances/"+waitingID, nil)
	if got["status"] != "waiting" {
		t.Fatalf("重启后应仍等待, got %v", got["status"])
	}
	if got["version"].(float64) != 2 {
		t.Fatalf("实例应固化最新版本 v2, got %v", got["version"])
	}

	// 5. 同一幂等键提交两次：只推进一次；同时让支付与补偿都失败
	input := map[string]any{
		"nodeId":         "w",
		"idempotencyKey": "idem-1",
		"payload": map[string]any{
			"confirm":   true,
			"failTasks": "pay",
			"failComp":  "undo",
		},
	}
	doJSON(t, h, "POST", "/api/instances/"+waitingID+"/inputs", input)
	again, _ := doJSON(t, h, "POST", "/api/instances/"+waitingID+"/inputs", input)
	if again["status"] != "compensation_failed" {
		t.Fatalf("补偿应失败并停住, got %v", again["status"])
	}
	cur := again["current"].([]any)
	if len(cur) != 1 || cur[0] != "undo" {
		t.Fatalf("应停在补偿节点 undo, got %v", cur)
	}
	inputs := again["inputs"].([]any)
	if len(inputs) != 2 {
		t.Fatalf("应有两条输入记录(有效+重复), got %d", len(inputs))
	}
	dup := inputs[1].(map[string]any)["duplicate"]
	if dup != true {
		t.Fatalf("第二条必须标记 duplicate, got %v", dup)
	}
	// 只产生一次 w -> pay 的流转
	fired := map[string]int{}
	for _, tr := range again["transitions"].([]any) {
		tm := tr.(map[string]any)
		fired[tm["edgeId"].(string)]++
	}
	if fired["c"] != 1 {
		t.Fatalf("等待后流转只应发生一次, got %d", fired["c"])
	}
	if fired["comp:pay->undo"] != 1 {
		t.Fatalf("缺少补偿流转记录")
	}

	// 6. 新流程定义发布不影响已挂起实例的版本快照：校验 definition 内嵌 v2
	var inst model.Instance
	if raw := mustGet(t, h, waitingID); true {
		if err := json.Unmarshal(raw, &inst); err != nil {
			t.Fatal(err)
		}
	}
	if inst.Definition.Version != 2 || len(inst.Definition.Nodes) != 6 {
		t.Fatalf("实例内嵌定义快照错误: v%d nodes=%d", inst.Definition.Version, len(inst.Definition.Nodes))
	}
}

func mustGet(t *testing.T, h *Server, id string) []byte {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/instances/"+id, nil)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)
	return rec.Body.Bytes()
}

func TestInvalidDefinitionRejected(t *testing.T) {
	h, _ := newTestServer(t)
	bad := map[string]any{
		"key": "bad",
		"nodes": []map[string]any{
			{"id": "s", "kind": "start", "name": "开始"},
		},
		"edges": []any{},
	}
	var buf bytes.Buffer
	_ = json.NewEncoder(&buf).Encode(bad)
	req := httptest.NewRequest("POST", "/api/definitions", &buf)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("非法定义应 400, got %d", rec.Code)
	}
}
