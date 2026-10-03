package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

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
	if len(parts) == 2 && parts[1] == "resume" && r.Method == http.MethodPost {
		s.resumeInstance(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "timeout" && r.Method == http.MethodPost {
		s.evaluateTimeout(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "review" && r.Method == http.MethodGet {
		s.getReview(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "review" && r.Method == http.MethodPost {
		s.openReview(w, r, id)
		return
	}
	if len(parts) == 3 && parts[1] == "review" && parts[2] == "inputs" && r.Method == http.MethodPost {
		s.deliverReviewInput(w, r, id)
		return
	}
	if len(parts) == 3 && parts[1] == "review" && parts[2] == "timeout" && r.Method == http.MethodPost {
		s.evaluateReviewTimeout(w, r, id)
		return
	}
	if len(parts) == 3 && parts[1] == "review" && parts[2] == "scope" && r.Method == http.MethodPost {
		s.updateReviewScope(w, r, id)
		return
	}
	if len(parts) == 3 && parts[1] == "wait" && parts[2] == "scope" && r.Method == http.MethodPost {
		s.updateWaitScope(w, r, id)
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

// evaluateTimeout lets an operator (or scheduler) trigger the deadline
// check for a collaborative wait. The decision itself is made at most once;
// calling again after a decision is a no-op.
func (s *Server) evaluateTimeout(w http.ResponseWriter, r *http.Request, id string) {
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
	s.engine.CheckTimeout(def, inst, time.Now().UTC())
	if err := s.store.SaveInstance(inst); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

// updateWaitScope changes the acceptance scope of a live collaborative wait
// (participants / delegation grants / revocations). Every change is appended
// as a new authorization version; the old ones stay in the instance history.
func (s *Server) updateWaitScope(w http.ResponseWriter, r *http.Request, id string) {
	var req engine.ScopeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	def, err := s.store.GetDefinition(inst.DefinitionID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.engine.UpdateWaitScope(def, inst, req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.SaveInstance(inst); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

// openReview starts a reconsideration round on the instance's decided
// collaborative wait. The original decision history is never rewritten.
func (s *Server) openReview(w http.ResponseWriter, r *http.Request, id string) {
	var req engine.OpenReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	def, err := s.store.GetDefinition(inst.DefinitionID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.engine.OpenReview(def, inst, req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.SaveInstance(inst); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

// deliverReviewInput records one review opinion (its own key namespace).
func (s *Server) deliverReviewInput(w http.ResponseWriter, r *http.Request, id string) {
	var input model.ExternalInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if input.Key == "" {
		writeErr(w, http.StatusBadRequest, errMissing("key"))
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
	if err := s.engine.DeliverReviewInput(def, inst, input); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.SaveInstance(inst); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

// evaluateReviewTimeout triggers the review deadline check; at most one
// review decision is ever formed.
func (s *Server) evaluateReviewTimeout(w http.ResponseWriter, r *http.Request, id string) {
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
	s.engine.CheckReviewTimeout(def, inst, time.Now().UTC())
	if err := s.store.SaveInstance(inst); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

// updateReviewScope changes the participation scope of the live review round.
func (s *Server) updateReviewScope(w http.ResponseWriter, r *http.Request, id string) {
	var req engine.ScopeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	def, err := s.store.GetDefinition(inst.DefinitionID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.engine.UpdateReviewScope(def, inst, req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.SaveInstance(inst); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

// getReview returns the review rounds plus the progress view: how far the
// original decision advanced, what is still pausable, what must be
// compensated, and which compensations remain unfinished.
func (s *Server) getReview(w http.ResponseWriter, r *http.Request, id string) {
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
	var progress any
	if rv := inst.LastReview(); rv != nil {
		progress = s.engine.ReviewProgress(def, inst, rv)
	}
	writeJSON(w, http.StatusOK, map[string]any{"reviews": inst.Reviews, "progress": progress})
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
