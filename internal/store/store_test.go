package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLibraryCatalogAndGrant(t *testing.T) {
	dir := t.TempDir()
	libRoot := filepath.Join(dir, "library")
	if err := os.MkdirAll(filepath.Join(libRoot, "2024"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(libRoot, "2024", "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	dev, err := s.CreateDevice("client", "DEVICE-ID", "", "linux")
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.CreateLibrary("Photo", libRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Catalog(dev.ID, lib.ID, ""); err == nil {
		t.Fatal("catalog must be denied before grant")
	}
	if err := s.AddGrant(lib.ID, dev.ID); err != nil {
		t.Fatal(err)
	}
	nodes, err := s.Catalog(dev.ID, lib.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Name != "2024" || !nodes[0].IsDir {
		t.Fatalf("unexpected root nodes: %+v", nodes)
	}
	children, err := s.Catalog(dev.ID, lib.ID, "2024")
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 1 || children[0].Name != "a.txt" || children[0].IsDir {
		t.Fatalf("unexpected children: %+v", children)
	}
}

func TestOverlappingLibrariesRejected(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "library")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateLibrary("root", root); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateLibrary("sub", filepath.Join(root, "sub")); err == nil {
		t.Fatal("overlapping library must be rejected")
	}
}

func TestRevokeRemovesAccess(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "library")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	dev, err := s.CreateDevice("client", "DEVICE-ID", "", "linux")
	if err != nil {
		t.Fatal(err)
	}
	lib, err := s.CreateLibrary("lib", root)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddGrant(lib.ID, dev.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeDevice(dev.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Catalog(dev.ID, lib.ID, ""); err == nil {
		t.Fatal("revoked device must not read catalog")
	}
	if got := s.LibrariesForDevice(dev.ID); len(got) != 0 {
		t.Fatalf("revoked device still has libraries: %+v", got)
	}
}
