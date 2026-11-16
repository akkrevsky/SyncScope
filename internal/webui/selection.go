package webui

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Selection struct {
	Library   string    `json:"library"`
	Included  []string  `json:"included"`
	Excluded  []string  `json:"excluded"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SelectionStore struct {
	mu   sync.RWMutex
	path string
	data Selection
}

func LoadSelection(file, library string) (*SelectionStore, error) {
	s := &SelectionStore{path: file, data: Selection{Library: library, Included: []string{}, Excluded: []string{}}}
	b, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s.data); err != nil {
			return nil, fmt.Errorf("load selection: %w", err)
		}
	}
	if s.data.Library != library {
		s.data = Selection{Library: library, Included: []string{}, Excluded: []string{}}
	}
	if s.data.Included == nil {
		s.data.Included = []string{}
	}
	if s.data.Excluded == nil {
		s.data.Excluded = []string{}
	}
	return s, nil
}

func (s *SelectionStore) Get() Selection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return clone(s.data)
}

func (s *SelectionStore) Set(included, excluded []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Included = dedupe(included)
	s.data.Excluded = dedupe(excluded)
	s.data.UpdatedAt = time.Now().UTC()
	return s.saveLocked()
}

func (s *SelectionStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func cleanRel(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("invalid path")
	}
	return c, nil
}

func isAncestor(a, b string) bool { return strings.HasPrefix(b, a+"/") }

func covered(p string, roots []string) bool {
	for _, r := range roots {
		if p == r || isAncestor(r, p) {
			return true
		}
	}
	return false
}

func hasDescendant(p string, roots []string) bool {
	for _, r := range roots {
		if isAncestor(p, r) {
			return true
		}
	}
	return false
}

func dedupe(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func clone(in Selection) Selection {
	out := in
	out.Included = append([]string{}, in.Included...)
	out.Excluded = append([]string{}, in.Excluded...)
	return out
}
