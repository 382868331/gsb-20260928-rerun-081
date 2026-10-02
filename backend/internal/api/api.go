package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"flowplatform/internal/engine"
	"flowplatform/internal/model"
	"flowplatform/internal/store"
)

type Server struct {
	store  *store.Store
	engine *engine.Engine
}

func NewHandler(st *store.Store, eng *engine.Engine) http.Handler {
	eng.WithPersist(func(inst *model.Instance) { _ = st.SaveInstance(inst) })
	s := &Server{store: st, engine: eng}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/processes", s.listProcesses)
	mux.HandleFunc("/api/definitions", s.definitions)
	mux.HandleFunc("/api/definitions/", s.getDef)
	mux.HandleFunc("/api/instances", s.instances)
	mux.HandleFunc("/api/instances/", s.instanceRoutes)
	return logging(cors(mux))
}

func (s *Server) definitions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listDefs(w, r)
	case http.MethodPost:
		s.publish(w, r)
	default:
		writeErr(w, http.StatusMethodNotAllowed, errMissing("method"))
	}
}

func (s *Server) instances(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listInstances(w, r)
	case http.MethodPost:
		s.startInstance(w, r)
	default:
		writeErr(w, http.StatusMethodNotAllowed, errMissing("method"))
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// listProcesses returns each process name with its latest version and counts
// of waiting instances, so the UI can drive empty/first-use flows and offer
// version migration without scanning every definition client-side.
func (s *Server) listProcesses(w http.ResponseWriter, r *http.Request) {
	latest, err := s.store.LatestDefinitionsByName()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if latest == nil {
		latest = map[string]model.Definition{}
	}
	writeJSON(w, http.StatusOK, latest)
}

type publishReq struct {
	Name  string       `json:"name"`
	Nodes []model.Node `json:"nodes"`
	Edges []model.Edge `json:"edges"`
}

func (s *Server) publish(w http.ResponseWriter, r *http.Request) {
	var req publishReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, errMissing("name"))
		return
	}
	def, err := s.store.PublishDraft(req.Name, req.Nodes, req.Edges)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, def)
}

func (s *Server) listDefs(w http.ResponseWriter, r *http.Request) {
	defs, err := s.store.ListDefinitions()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if defs == nil {
		defs = []model.Definition{}
	}
	writeJSON(w, http.StatusOK, defs)
}

func (s *Server) getDef(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/definitions/")
	def, err := s.store.GetDefinition(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, def)
}

type startReq struct {
	DefinitionID string         `json:"definitionId"`
	Name         string         `json:"name"` // fallback: start latest by process name
	Data         map[string]any `json:"data"`
}

func (s *Server) startInstance(w http.ResponseWriter, r *http.Request) {
	var req startReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var def *model.Definition
	var err error
	if req.DefinitionID != "" {
		def, err = s.store.GetDefinition(req.DefinitionID)
	} else {
		name := req.Name
		if name == "" {
			name = "order-demo"
		}
		def, err = s.store.LatestDefinition(name)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	inst, err := s.store.CreateInstance(def, req.Data)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Execute to first wait/completion/failure, then persist.
	s.engine.Run(def, inst)
	if err := s.store.SaveInstance(inst); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, inst)
}

func (s *Server) resumeInstance(w http.ResponseWriter, r *http.Request, id string) {
	unlock := s.store.LockInstance(id)
	defer unlock()
	inst, err := s.store.GetInstance(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	def, err := s.store.GetDefinition(inst.DefinitionID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.engine.Resume(def, inst)
	if err := s.store.SaveInstance(inst); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

func (s *Server) listInstances(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListInstances()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if list == nil {
		list = []model.Instance{}
	}
	writeJSON(w, http.StatusOK, list)
}

// instanceRoutes dispatches /api/instances/{id} and /api/instances/{id}/inputs
func (s *Server) instanceRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/instances/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeErr(w, http.StatusNotFound, errMissing("id"))
		return
	}
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		inst, err := s.store.GetInstance(id)
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		def, _ := s.store.GetDefinition(inst.DefinitionID)
		writeJSON(w, http.StatusOK, map[string]any{"instance": inst, "definition": def})
		return
	}
	if len(parts) == 2 && parts[1] == "inputs" && r.Method == http.MethodPost {
		s.deliverInput(w, r, id)
		return
	}
	if len(parts) >= 3 && parts[1] == "collab" && r.Method == http.MethodPost {
		action := parts[2]
		if len(parts) == 4 && parts[2] == "scope" {
			action = "scope_" + parts[3]
		}
		s.collabAction(w, r, id, action)
		return
	}
	if len(parts) == 2 && parts[1] == "resume" && r.Method == http.MethodPost {
		s.resumeInstance(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "migration" && r.Method == http.MethodGet {
		s.previewMigration(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "migration" && r.Method == http.MethodPost {
		s.confirmMigration(w, r, id)
		return
	}
	writeErr(w, http.StatusNotFound, errMissing("route"))
}

func (s *Server) collabAction(w http.ResponseWriter, r *http.Request, id, action string) {
	var req model.GrantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.WaitNode == "" {
		writeErr(w, http.StatusBadRequest, errMissing("waitNode"))
		return
	}
	unlock := s.store.LockInstance(id)
	defer unlock()
	inst, err := s.store.GetInstance(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	def, err := s.store.GetDefinition(inst.DefinitionID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Resolve any due deadline under the same instance lock first, so an
	// authorization change and a timeout cannot race into two decisions.
	s.engine.CheckTimeouts(def, inst)
	switch action {
	case "delegate":
		err = s.engine.GrantDelegate(def, inst, req)
	case "revoke":
		err = s.engine.RevokeDelegate(def, inst, req)
	case "scope_add":
		err = s.engine.ChangeScope(def, inst, true, req)
	case "scope_remove":
		err = s.engine.ChangeScope(def, inst, false, req)
	case "timeout":
		err = s.engine.ForceTimeout(def, inst, req.WaitNode)
	default:
		writeErr(w, http.StatusNotFound, errMissing("collab action: delegate/revoke/scope/add/scope/remove/timeout"))
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.SaveInstance(inst); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

func (s *Server) deliverInput(w http.ResponseWriter, r *http.Request, id string) {
	var input model.ExternalInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	unlock := s.store.LockInstance(id)
	defer unlock()
	inst, err := s.store.GetInstance(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	// Definition is pinned at start time; republishing never mutates running instances.
	def, err := s.store.GetDefinition(inst.DefinitionID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.engine.DeliverInput(def, inst, input); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.SaveInstance(inst); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

type migrationReq struct {
	DefinitionID string `json:"definitionId"`
	Name         string `json:"name"` // alternative: migrate to latest version of this process name
	Confirm      bool   `json:"confirm"`
}

func (s *Server) migrationTarget(w http.ResponseWriter, inst *model.Instance, req migrationReq) *model.Definition {
	if req.DefinitionID != "" {
		def, err := s.store.GetDefinition(req.DefinitionID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return nil
		}
		return def
	}
	name := req.Name
	if name == "" {
		if cur, err := s.store.GetDefinition(inst.DefinitionID); err == nil {
			name = cur.Name
		}
	}
	def, err := s.store.LatestDefinition(name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return nil
	}
	return def
}

func (s *Server) previewMigration(w http.ResponseWriter, r *http.Request, id string) {
	unlock := s.store.LockInstance(id)
	defer unlock()
	inst, err := s.store.GetInstance(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	from, err := s.store.GetDefinition(inst.DefinitionID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	var req migrationReq
	if r.URL.RawQuery != "" {
		req.DefinitionID = r.URL.Query().Get("definitionId")
		req.Name = r.URL.Query().Get("name")
	}
	to := s.migrationTarget(w, inst, req)
	if to == nil {
		return
	}
	impact, err := engine.PreviewMigration(inst, from, to)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"impact": impact, "from": from, "to": to})
}

func (s *Server) confirmMigration(w http.ResponseWriter, r *http.Request, id string) {
	var req migrationReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !req.Confirm {
		writeErr(w, http.StatusBadRequest, errMissing("confirm（请先查看受影响步骤并确认）"))
		return
	}
	unlock := s.store.LockInstance(id)
	defer unlock()
	inst, err := s.store.GetInstance(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	from, err := s.store.GetDefinition(inst.DefinitionID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	to := s.migrationTarget(w, inst, req)
	if to == nil {
		return
	}
	impact, err := s.engine.MigrateWaiting(inst, from, to)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.SaveInstance(inst); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"impact": impact, "instance": inst})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func errMissing(s string) error { return &simpleError{msg: "缺少参数: " + s} }

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}
