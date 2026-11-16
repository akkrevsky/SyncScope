// Package ignore builds Syncthing-compatible whitelist patterns for a
// client-side selection.
package ignore

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// Item is a selected or excluded path relative to a library root.
// Paths use forward slashes and never start with a slash.
type Item struct {
	Path  string
	IsDir bool
}

// Patterns returns the .stignore contents for a selection.
//
// The generated list is a whitelist: excluded paths first, then rooted
// negated includes, and a catch-all "*" last. Syncthing uses the first
// matching pattern, so order is significant.
func Patterns(selected, excluded []Item) ([]string, error) {
	sel, err := normalizeAll(selected)
	if err != nil {
		return nil, fmt.Errorf("selected: %w", err)
	}
	excl, err := normalizeAll(excluded)
	if err != nil {
		return nil, fmt.Errorf("excluded: %w", err)
	}

	rootSelected := false
	for _, s := range sel {
		if s.Path == "." {
			rootSelected = true
			break
		}
	}

	if rootSelected {
		// Everything is selected. Only explicit exclusions are needed.
		if len(sel) > 1 {
			sel = sel[:1]
		}
		out := make([]string, 0, len(excl)+1)
		if len(excl) > 0 {
			out = append(out, "#escape=\\")
		}
		for _, e := range excl {
			if e.Path == "." {
				return nil, fmt.Errorf("cannot exclude the library root")
			}
			out = append(out, "/"+escape(e.Path))
		}
		return dedupe(out), nil
	}

	if len(sel) == 0 {
		return []string{"*"}, nil
	}

	for _, s := range sel {
		if !s.IsDir {
			return nil, fmt.Errorf("file selection is not supported; select the parent directory")
		}
	}

	for _, s := range sel {
		if s.Path == "." {
			continue
		}
		for _, e := range excl {
			if e.Path == "." {
				return nil, fmt.Errorf("cannot exclude the library root")
			}
			if s.Path == e.Path {
				return nil, fmt.Errorf("%q is both selected and excluded", s.Path)
			}
			if isAncestor(e.Path, s.Path) {
				return nil, fmt.Errorf("excluded %q contains selected %q", e.Path, s.Path)
			}
		}
	}

	out := make([]string, 0, 2+len(sel)*4)
	out = append(out, "#escape=\\")
	for _, e := range excl {
		out = append(out, "/"+escape(e.Path))
	}

	// Shallow paths first keeps ancestor negations ahead of descendants.
	sort.SliceStable(sel, func(i, j int) bool {
		if depth(sel[i].Path) != depth(sel[j].Path) {
			return depth(sel[i].Path) < depth(sel[j].Path)
		}
		return sel[i].Path < sel[j].Path
	})

	for _, s := range sel {
		for _, a := range ancestors(s.Path) {
			out = append(out, "!/"+escape(a))
		}
		out = append(out, "!/"+escape(s.Path))
		if s.IsDir {
			out = append(out, "!/"+escape(s.Path)+"/**")
		}
	}

	out = append(out, "*")
	return dedupe(out), nil
}

func normalizeAll(items []Item) ([]Item, error) {
	out := make([]Item, 0, len(items))
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		p, err := normalize(it.Path)
		if err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%s\x00%t", p, it.IsDir)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Item{Path: p, IsDir: it.IsDir})
	}
	return out, nil
}

func normalize(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	p = strings.ReplaceAll(p, "\\", "/")
	if strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("absolute path not allowed: %q", p)
	}
	clean := path.Clean(p)
	if clean == "." {
		return ".", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("path escapes library root: %q", p)
	}
	if strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("absolute path not allowed: %q", p)
	}
	return clean, nil
}

func escape(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch r {
		case '\\', '*', '?', '[', ']', '{', '}':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func ancestors(p string) []string {
	parts := strings.Split(p, "/")
	if len(parts) < 2 {
		return nil
	}
	out := make([]string, 0, len(parts)-1)
	for i := 1; i < len(parts); i++ {
		out = append(out, strings.Join(parts[:i], "/"))
	}
	return out
}

func isAncestor(a, b string) bool {
	return strings.HasPrefix(b, a+"/")
}

func depth(p string) int {
	if p == "." {
		return 0
	}
	return strings.Count(p, "/") + 1
}

func dedupe(in []string) []string {
	out := in[:0]
	seen := make(map[string]bool, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
