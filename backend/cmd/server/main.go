package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"flowplatform/internal/api"
	"flowplatform/internal/engine"
	"flowplatform/internal/model"
	"flowplatform/internal/store"
)

// Automatic task behavior is data-driven for the first round:
//
//	data["failAt"]      = node id  -> that task fails
//	data["compFailAt"]  = node id  -> compensation for that task fails
//
// This lets the UI simulate failure and failed compensation without code changes.
func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dataDir := flag.String("data", "./data", "persistence directory")
	static := flag.String("static", "", "directory of built frontend (optional)")
	flag.Parse()

	st, err := store.New(*dataDir)
	if err != nil {
		log.Fatal(err)
	}

	taskHandler := func(inst *model.Instance, node model.Node) error {
		if v, _ := inst.Data["failAt"].(string); v != "" && v == node.ID {
			return fmt.Errorf("任务 %s 按模拟数据失败", node.Name)
		}
		return nil
	}
	compHandler := func(inst *model.Instance, node model.Node) error {
		if v, _ := inst.Data["compFailAt"].(string); v != "" && v == node.ID {
			return fmt.Errorf("任务 %s 的补偿失败", node.Name)
		}
		return nil
	}
	eng := engine.New(taskHandler, compHandler)
	recoverRunning(st, eng)
	handler := api.NewHandler(st, eng)

	mux := http.NewServeMux()
	mux.Handle("/api/", handler)
	if *static != "" {
		fs := http.FileServer(http.Dir(*static))
		mux.Handle("/", spaHandler{static: *static, h: fs})
	}

	log.Printf("流程平台已启动: %s (数据目录 %s)", *addr, *dataDir)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

// recoverRunning pushes any instance left in "running" status by a previous
// crash/restart forward to its next wait/completion/failure and persists it.
func recoverRunning(st *store.Store, eng *engine.Engine) {
	list, err := st.ListInstances()
	if err != nil {
		log.Printf("重启恢复: 读取实例失败: %v", err)
		return
	}
	for i := range list {
		inst := &list[i]
		if inst.Status != model.StatusRunning {
			continue
		}
		def, err := st.GetDefinition(inst.DefinitionID)
		if err != nil {
			log.Printf("重启恢复: 实例 %s 的定义缺失: %v", inst.ID, err)
			continue
		}
		eng.Resume(def, inst)
		if err := st.SaveInstance(inst); err != nil {
			log.Printf("重启恢复: 保存实例 %s 失败: %v", inst.ID, err)
			continue
		}
		log.Printf("重启恢复: 实例 %s 推进至 %s (停点 %s)", inst.ID, inst.Status, inst.StoppedAt)
	}
}

// spaHandler serves index.html for unknown non-API routes (React Router-less SPA fallback).
type spaHandler struct {
	static string
	h      http.Handler
}

func (s spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	// If a real file exists, serve it; otherwise index.html.
	full := s.static + r.URL.Path
	if r.URL.Path == "/" {
		full = s.static + "/index.html"
	}
	if _, err := os.Stat(full); err != nil {
		r.URL.Path = "/"
	}
	s.h.ServeHTTP(w, r)
}
