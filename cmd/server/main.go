package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"workflow/internal/api"
	"workflow/internal/engine"
	"workflow/internal/model"
	"workflow/internal/store"
)

//go:embed all:webdist
var webAssets embed.FS

func main() {
	addr := flag.String("addr", envOr("ADDR", ":8080"), "监听地址")
	dataDir := flag.String("data", envOr("DATA_DIR", "./data"), "数据目录")
	flag.Parse()

	st, err := store.New(*dataDir)
	if err != nil {
		log.Fatalf("初始化存储失败: %v", err)
	}
	eng := engine.New(st)

	if err := seed(st); err != nil {
		log.Fatalf("初始化示例流程失败: %v", err)
	}
	if err := resumeAll(st, eng); err != nil {
		log.Fatalf("恢复运行中实例失败: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/api/", api.New(st, eng).Routes())
	staticRoot, err := fs.Sub(webAssets, "webdist")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", spaHandler(staticRoot))

	display := *addr
	if len(display) > 0 && display[0] == ':' {
		display = "localhost" + display
	}
	log.Printf("业务流程运行平台已启动: http://%s", display)
	log.Printf("数据目录: %s", *dataDir)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// resumeAll 重启后继续所有未走完的实例。等待输入的实例保持挂起，
// running / compensating 的实例从真实停留节点继续推进。
func resumeAll(st *store.Store, eng *engine.Engine) error {
	insts, err := st.ListInstances()
	if err != nil {
		return err
	}
	for _, inst := range insts {
		switch inst.Status {
		case model.StatusRunning, model.StatusCompensating:
			log.Printf("恢复实例 %s（流程 %s v%d）", inst.ID, inst.DefKey, inst.Version)
			if err := eng.Resume(inst); err != nil {
				return err
			}
		case model.StatusWaiting:
			log.Printf("实例 %s 正在等待外部输入 %v，保持挂起", inst.ID, inst.WaitingFor)
		}
	}
	return nil
}

// seed 首次启动时放入一个覆盖条件分支、等待输入、失败补偿的示例流程。
func seed(st *store.Store) error {
	v, err := st.LatestVersion("order-demo")
	if err != nil || v > 0 {
		return err
	}
	def := &model.Definition{
		Key:       "order-demo",
		Version:   1,
		Name:      "订单处理（条件/等待/补偿示例）",
		CreatedAt: time.Now(),
		Nodes: []model.Node{
			{ID: "n_start", Kind: model.KindStart, Name: "开始", Pos: model.Position{X: 40, Y: 160}},
			{ID: "n_check", Kind: model.KindTask, Name: "库存检查", Pos: model.Position{X: 220, Y: 160}},
			{ID: "n_vip", Kind: model.KindCondition, Name: "是否大额订单?", Condition: "amount >= 1000", Pos: model.Position{X: 420, Y: 160}},
			{ID: "n_review", Kind: model.KindWait, Name: "等待经理审批", InputKey: "approval", Pos: model.Position{X: 640, Y: 60}},
			{ID: "n_charge", Kind: model.KindTask, Name: "扣款", Compensate: "n_refund", Pos: model.Position{X: 640, Y: 260}},
			{ID: "n_ship", Kind: model.KindTask, Name: "发货", Pos: model.Position{X: 860, Y: 160}},
			{ID: "n_refund", Kind: model.KindTask, Name: "退款补偿", Pos: model.Position{X: 860, Y: 380}},
			{ID: "n_end", Kind: model.KindEnd, Name: "结束", Pos: model.Position{X: 1080, Y: 160}},
		},
		Edges: []model.Edge{
			{ID: "e1", From: "n_start", To: "n_check"},
			{ID: "e2", From: "n_check", To: "n_vip"},
			{ID: "e3", From: "n_vip", To: "n_review", Branch: model.BranchTrue},
			{ID: "e4", From: "n_vip", To: "n_charge", Branch: model.BranchFalse},
			{ID: "e5", From: "n_review", To: "n_charge"},
			{ID: "e6", From: "n_charge", To: "n_ship"},
			{ID: "e7", From: "n_ship", To: "n_end"},
		},
	}
	return st.SaveDefinition(def)
}

// spaHandler 服务已构建的前端静态资源；未知路径回退到 index.html。
func spaHandler(root fs.FS) http.Handler {
	fileServer := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if f, err := root.Open(p); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
