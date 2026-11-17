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

// Selections stores per-library selections in one local JSON file.
type Selections struct {
	mu   sync.RWMutex
	path string
	data map[string]Selection
}

func LoadSelections(file string) (*Selections, error) {
	s := &Selections{path: file, data: map[string]Selection{}}
	b, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(b) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		// Migrate the old single-selection file.
		var one Selection
		if err2 := json.Unmarshal(b, &one); err2 != nil {
			return nil, fmt.Errorf("load selections: %w", err)
		}
		if one.Library != "" {
			s.data[one.Library] = one
		}
	}
	for lib, sel := range s.data {
		if sel.Included == nil {
			sel.Included = []string{}
		}
		if sel.Excluded == nil {
			sel.Excluded = []string{}
		}
		sel.Library = lib
		s.data[lib] = sel
	}
	return s, nil
}

func (s *Selections) Get(library string) Selection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sel, ok := s.data[library]
	if !ok {
		return Selection{Library: library, Included: []string{}, Excluded: []string{}}
	}
	return clone(sel)
}

func (s *Selections) Set(library string, included, excluded []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[library] = Selection{
		Library:   library,
		Included:  dedupe(included),
		Excluded:  dedupe(excluded),
		UpdatedAt: time.Now().UTC(),
	}
	return s.saveLocked()
}

func (s *Selections) saveLocked() error {
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
