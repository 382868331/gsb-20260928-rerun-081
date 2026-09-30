package store

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"flowplatform/internal/model"
)

// Store persists definitions and instances as JSON files.
// Each write is atomic (temp file + rename) so a restart always sees
// a consistent state. Definitions are append-only/immutable.
type Store struct {
	root string
	mu   sync.Mutex
}

func New(root string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(root, "definitions"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "instances"), 0o755); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

func NewID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

func (s *Store) PublishDraft(name string, nodes []model.Node, edges []model.Edge) (*model.Definition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateGraph(nodes, edges); err != nil {
		return nil, err
	}
	defs, _ := s.listDefsLocked()
	version := 1
	for _, d := range defs {
		if d.Name == name && d.Version >= version {
			version = d.Version + 1
		}
	}
	id := NewID("def")
	def := &model.Definition{
		ID:          id,
		Name:        name,
		Version:     version,
		Nodes:       nodes,
		Edges:       edges,
		PublishedAt: time.Now().UTC(),
	}
	if err := s.writeJSON(filepath.Join("definitions", id+".json"), def); err != nil {
		return nil, err
	}
	return def, nil
}

func (s *Store) GetDefinition(id string) (*model.Definition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getDefLocked(id)
}

func (s *Store) getDefLocked(id string) (*model.Definition, error) {
	raw, err := os.ReadFile(filepath.Join(s.root, "definitions", id+".json"))
	if err != nil {
		return nil, fmt.Errorf("流程定义不存在: %w", err)
	}
	var d model.Definition
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// LatestDefinition returns the newest version id for a process name.
func (s *Store) LatestDefinition(name string) (*model.Definition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defs, err := s.listDefsLocked()
	if err != nil {
		return nil, err
	}
	var best *model.Definition
	for i := range defs {
		if defs[i].Name == name && (best == nil || defs[i].Version > best.Version) {
			best = &defs[i]
		}
	}
	if best == nil {
		return nil, fmt.Errorf("没有名为 %q 的已发布流程", name)
	}
	return best, nil
}

func (s *Store) ListDefinitions() ([]model.Definition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listDefsLocked()
}

func (s *Store) listDefsLocked() ([]model.Definition, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "definitions"))
	if err != nil {
		return nil, err
	}
	defs := []model.Definition{} // never nil: first-run empty set must serialize as []
	for _, en := range entries {
		if filepath.Ext(en.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.root, "definitions", en.Name()))
		if err != nil {
			continue
		}
		var d model.Definition
		if json.Unmarshal(raw, &d) == nil {
			defs = append(defs, d)
		}
	}
	sort.Slice(defs, func(i, j int) bool {
		if defs[i].Name != defs[j].Name {
			return defs[i].Name < defs[j].Name
		}
		return defs[i].Version < defs[j].Version
	})
	return defs, nil
}

func (s *Store) CreateInstance(def *model.Definition, data map[string]any) (*model.Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	start := ""
	for _, n := range def.Nodes {
		if n.Type == model.NodeStart {
			start = n.ID
			break
		}
	}
	if start == "" {
		return nil, fmt.Errorf("流程缺少开始节点")
	}
	now := time.Now().UTC()
	inst := &model.Instance{
		ID:             NewID("inst"),
		DefinitionID:   def.ID,
		Version:        def.Version,
		Status:         model.StatusRunning,
		Data:           data,
		CurrentNode:    start,
		ConsumedInputs: map[string]bool{},
		Records:        nil,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if inst.Data == nil {
		inst.Data = map[string]any{}
	}
	if err := s.writeJSON(filepath.Join("instances", inst.ID+".json"), inst); err != nil {
		return nil, err
	}
	return inst, nil
}

func (s *Store) GetInstance(id string) (*model.Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getInstanceLocked(id)
}

func (s *Store) getInstanceLocked(id string) (*model.Instance, error) {
	raw, err := os.ReadFile(filepath.Join(s.root, "instances", id+".json"))
	if err != nil {
		return nil, fmt.Errorf("实例不存在: %w", err)
	}
	var inst model.Instance
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&inst); err != nil {
		return nil, err
	}
	if inst.ConsumedInputs == nil {
		inst.ConsumedInputs = map[string]bool{}
	}
	if inst.Data == nil {
		inst.Data = map[string]any{}
	}
	return &inst, nil
}

func (s *Store) SaveInstance(inst *model.Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst.UpdatedAt = time.Now().UTC()
	return s.writeJSON(filepath.Join("instances", inst.ID+".json"), inst)
}

func (s *Store) ListInstances() ([]model.Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(filepath.Join(s.root, "instances"))
	if err != nil {
		return nil, err
	}
	list := []model.Instance{} // never nil: empty list must serialize as []
	for _, en := range entries {
		if filepath.Ext(en.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.root, "instances", en.Name()))
		if err != nil {
			continue
		}
		var inst model.Instance
		if json.Unmarshal(raw, &inst) == nil {
			list = append(list, inst)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	return list, nil
}

func (s *Store) writeJSON(rel string, v any) error {
	full := filepath.Join(s.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := full + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, full)
}

func validateGraph(nodes []model.Node, edges []model.Edge) error {
	if len(nodes) == 0 {
		return fmt.Errorf("流程为空")
	}
	ids := map[string]bool{}
	starts, ends := 0, 0
	for _, n := range nodes {
		if n.ID == "" {
			return fmt.Errorf("存在没有 id 的节点")
		}
		if ids[n.ID] {
			return fmt.Errorf("节点 id 重复: %s", n.ID)
		}
		ids[n.ID] = true
		switch n.Type {
		case model.NodeStart:
			starts++
		case model.NodeEnd:
			ends++
		}
	}
	if starts != 1 {
		return fmt.Errorf("流程必须恰好有一个开始节点")
	}
	if ends < 1 {
		return fmt.Errorf("流程至少需要一个结束节点")
	}
	for _, e := range edges {
		if !ids[e.From] || !ids[e.To] {
			return fmt.Errorf("边 %s 引用了不存在的节点", e.ID)
		}
	}
	return nil
}
