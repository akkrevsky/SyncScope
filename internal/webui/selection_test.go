package webui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSelectionsPerMountAndReload(t *testing.T) {
	p := filepath.Join(t.TempDir(), "selection.json")
	s, err := LoadSelections(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("lib-a", []string{"2024"}, []string{"2024/private"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("lib-b", []string{"docs"}, nil); err != nil {
		t.Fatal(err)
	}
	again, err := LoadSelections(p)
	if err != nil {
		t.Fatal(err)
	}
	a := again.Get("lib-a")
	if len(a.Included) != 1 || a.Included[0] != "2024" || len(a.Excluded) != 1 {
		t.Fatalf("lib-a: %+v", a)
	}
	b := again.Get("lib-b")
	if len(b.Included) != 1 || b.Included[0] != "docs" {
		t.Fatalf("lib-b: %+v", b)
	}
	if c := again.Get("lib-c"); len(c.Included) != 0 || len(c.Excluded) != 0 {
		t.Fatalf("empty mount: %+v", c)
	}
}

func TestSelectionsMigrateSingle(t *testing.T) {
	p := filepath.Join(t.TempDir(), "old.json")
	old := `{"library":"lib-old","included":["a"],"excluded":[]}`
	if err := os.WriteFile(p, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSelections(p)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Get("lib-old")
	if len(got.Included) != 1 || got.Included[0] != "a" {
		t.Fatalf("got %+v", got)
	}
}
