// Package api 提供流程平台的 HTTP 接口。
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"workflow/internal/engine"
	"workflow/internal/model"
	"workflow/internal/store"
)

type Server struct {
	store  *store.Store
	engine *engine.Engine
}

func New(s *store.Store, e *engine.Engine) *Server {
	return &Server{store: s, engine: e}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/definitions", s.listDefinitions)
	mux.HandleFunc("POST /api/definitions", s.publishDefinition)
	mux.HandleFunc("GET /api/definitions/{key}/{version}", s.getDefinition)
	mux.HandleFunc("POST /api/instances", s.startInstance)
	mux.HandleFunc("GET /api/instances", s.listInstances)
	mux.HandleFunc("GET /api/instances/{id}", s.getInstance)
	mux.HandleFunc("POST /api/instances/{id}/inputs", s.submitInput)
	handler := http.Handler(mux)
	handler = loggingHandler(recoveryHandler(handler))
	handler = corsHandler(handler)
	return handler
}

type publishRequest struct {
	model.Definition
}

type publishResponse struct {
	Key     string `json:"key"`
	Version int    `json:"version"`
}

func (s *Server) publishDefinition(w http.ResponseWriter, r *http.Request) {
	var def model.Definition
	if err := json.NewDecoder(r.Body).Decode(&def); err != nil {
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return
	}
	if err := engine.Validate(&def); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	latest, err := s.store.LatestVersion(def.Key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	def.Version = latest + 1 // 版本只增不改
	def.CreatedAt = time.Now()
	if err := s.store.SaveDefinition(&def); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, publishResponse{Key: def.Key, Version: def.Version})
}

func (s *Server) listDefinitions(w http.ResponseWriter, r *http.Request) {
	defs, err := s.store.ListDefinitions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, defs)
}

func (s *Server) getDefinition(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	v, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "版本号必须是整数")
		return
	}
	def, err := s.store.GetDefinition(key, v)
	if err != nil {
		writeError(w, http.StatusNotFound, "定义不存在")
		return
	}
	writeJSON(w, http.StatusOK, def)
}

type startRequest struct {
	DefKey string         `json:"defKey"`
	Input  map[string]any `json:"input"`
}

func (s *Server) startInstance(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.DefKey) == "" {
		writeError(w, http.StatusBadRequest, "defKey 不能为空")
		return
	}
	inst, err := s.engine.StartInstance(req.DefKey, req.Input)
	if err != nil {
		if strings.Contains(err.Error(), "尚未发布") || strings.Contains(err.Error(), "不存在") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, inst)
}

func (s *Server) listInstances(w http.ResponseWriter, r *http.Request) {
	insts, err := s.store.ListInstances()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, insts)
}

func (s *Server) getInstance(w http.ResponseWriter, r *http.Request) {
	inst, err := s.store.GetInstance(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "实例不存在")
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

type inputRequest struct {
	NodeID         string `json:"nodeId"`
	IdempotencyKey string `json:"idempotencyKey"`
	Payload        any    `json:"payload"`
}

func (s *Server) submitInput(w http.ResponseWriter, r *http.Request) {
	var req inputRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.NodeID == "" {
		writeError(w, http.StatusBadRequest, "nodeId 不能为空")
		return
	}
	inst, err := s.engine.SubmitInput(r.PathValue("id"), req.NodeID, req.IdempotencyKey, req.Payload)
	if err != nil {
		code := http.StatusBadRequest
		if strings.Contains(err.Error(), "不存在") {
			code = http.StatusNotFound
		}
		writeError(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, inst)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errorBody struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

func corsHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(s int) {
	w.status = s
	w.ResponseWriter.WriteHeader(s)
}

func loggingHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
	})
}

func recoveryHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				writeError(w, http.StatusInternalServerError, "服务器内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
