// Package store 用 JSON 文件保存流程定义与实例。
// 每次写操作整文件原子替换（写临时文件后 rename），保证重启后状态完整。
// 服务为单进程，内存锁 + 文件落盘足以满足第一轮需求。
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"workflow/internal/model"
)

type Store struct {
	dir string
	mu  sync.Mutex
}

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "definitions"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "instances"), 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// SaveDefinition 发布新版本。定义一旦写入不再修改。
func (s *Store) SaveDefinition(d *model.Definition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, "definitions", fmt.Sprintf("%s.v%d.json", d.Key, d.Version))
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("definition %s version %d already exists", d.Key, d.Version)
	}
	return writeJSON(path, d)
}

func (s *Store) GetDefinition(key string, version int) (*model.Definition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, "definitions", fmt.Sprintf("%s.v%d.json", key, version))
	var d model.Definition
	if err := readJSON(path, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// LatestVersion 返回某流程的最大版本号；不存在时返回 0。
func (s *Store) LatestVersion(key string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	matches, err := filepath.Glob(filepath.Join(s.dir, "definitions", key+".v*.json"))
	if err != nil {
		return 0, err
	}
	max := 0
	for _, m := range matches {
		var d model.Definition
		if err := readJSON(m, &d); err == nil && d.Version > max {
			max = d.Version
		}
	}
	return max, nil
}

func (s *Store) ListDefinitions() ([]*model.Definition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := filepath.Glob(filepath.Join(s.dir, "definitions", "*.json"))
	if err != nil {
		return nil, err
	}
	out := make([]*model.Definition, 0, len(files))
	seen := map[string]int{}
	for _, f := range files {
		var d model.Definition
		if err := readJSON(f, &d); err != nil {
			continue
		}
		if v, ok := seen[d.Key]; !ok || d.Version > v {
			seen[d.Key] = d.Version
			found := false
			for i, e := range out {
				if e.Key == d.Key {
					out[i] = &d
					found = true
					break
				}
			}
			if !found {
				out = append(out, &d)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (s *Store) SaveInstance(inst *model.Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(filepath.Join(s.dir, "instances", inst.ID+".json"), inst)
}

func (s *Store) GetInstance(id string) (*model.Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, "instances", id+".json")
	var inst model.Instance
	if err := readJSON(path, &inst); err != nil {
		return nil, err
	}
	return &inst, nil
}

func (s *Store) ListInstances() ([]*model.Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := filepath.Glob(filepath.Join(s.dir, "instances", "*.json"))
	if err != nil {
		return nil, err
	}
	out := make([]*model.Instance, 0, len(files))
	for _, f := range files {
		var inst model.Instance
		if err := readJSON(f, &inst); err == nil {
			out = append(out, &inst)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
