package webui

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"syncscope/internal/control"
	"syncscope/internal/ignore"
	"syncscope/internal/store"
	"syncscope/internal/syncthing"
)

//go:embed static
var staticFS embed.FS

type Server struct {
	Control   *control.Client
	Syncthing *syncthing.Client
	Library   store.Library
	FolderID  string
	LocalPath string
	Selection *SelectionStore
	Log       *slog.Logger
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/library", s.library)
	mux.HandleFunc("GET /api/catalog", s.catalog)
	mux.HandleFunc("GET /api/selection", s.getSelection)
	mux.HandleFunc("POST /api/selection", s.setSelection)
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("POST /api/free-space", s.freeSpace)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(mustSub(staticFS, "static")))))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		b, err := staticFS.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})
	return mux
}

func (s *Server) library(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"id":     s.Library.ID,
		"name":   s.Library.Name,
		"folder": s.FolderID,
		"local":  s.LocalPath,
	})
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request) {
	parent := r.URL.Query().Get("parent")
	page, err := s.Control.Catalog(r.Context(), s.Library.ID, parent)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, page)
}

func (s *Server) getSelection(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Selection.Get())
}

type selectionRequest struct {
	Included []string `json:"included"`
	Excluded []string `json:"excluded"`
}

func (s *Server) setSelection(w http.ResponseWriter, r *http.Request) {
	var req selectionRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	inc, err := normalizeAll(req.Included)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	exc, err := normalizeAll(req.Excluded)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	sel := Selection{Library: s.Library.ID, Included: inc, Excluded: exc, UpdatedAt: time.Now().UTC()}
	patterns, err := buildPatterns(sel)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if err := s.apply(r.Context(), patterns); err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	if err := s.Selection.Set(inc, exc); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"selection": s.Selection.Get(), "patterns": patterns})
}

type statusResponse struct {
	State      string `json:"state"`
	NeedFiles  int    `json:"need_files"`
	LocalFiles int    `json:"local_files"`
	Errors     int    `json:"errors"`
	Connected  bool   `json:"connected"`
	ConnType   string `json:"connection_type"`
	ConnAddr   string `json:"connection_address"`
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	fs, err := s.Syncthing.FolderStatus(r.Context(), s.FolderID)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	out := statusResponse{
		State:      fs.State,
		NeedFiles:  fs.NeedFiles,
		LocalFiles: fs.LocalFiles,
		Errors:     fs.Errors,
	}
	conns, err := s.Syncthing.Connections(r.Context())
	if err == nil {
		for _, c := range conns.Connections {
			if c.Connected {
				out.Connected = true
				out.ConnType = c.Type
				out.ConnAddr = c.Address
				break
			}
		}
	}
	writeJSON(w, 200, out)
}

type freeSpaceRequest struct {
	Path string `json:"path"`
}

func (s *Server) freeSpace(w http.ResponseWriter, r *http.Request) {
	var req freeSpaceRequest
	if err := decode(r, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	p, err := cleanRel(req.Path)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	sel := s.Selection.Get()
	if !covered(p, sel.Included) || covered(p, sel.Excluded) {
		writeJSON(w, 400, map[string]string{"error": "path is not selected"})
		return
	}

	inc := removePrefix(sel.Included, p)
	exc := removePrefix(sel.Excluded, p)
	if covered(p, inc) {
		exc = append(exc, p)
	}
	next := Selection{Library: s.Library.ID, Included: inc, Excluded: exc}
	patterns, err := buildPatterns(next)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if err := s.apply(r.Context(), patterns); err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	if err := s.Selection.Set(inc, exc); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if err := s.waitIdle(r.Context(), 15*time.Second); err != nil {
		writeJSON(w, 409, map[string]string{"error": "folder did not become idle: " + err.Error()})
		return
	}

	target := filepath.Join(s.LocalPath, filepath.FromSlash(p))
	rel, err := filepath.Rel(s.LocalPath, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		writeJSON(w, 400, map[string]string{"error": "invalid local path"})
		return
	}
	if err := os.RemoveAll(target); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "removed": p})
}

func (s *Server) apply(ctx context.Context, patterns []string) error {
	if err := s.Syncthing.SetIgnores(ctx, s.FolderID, patterns); err != nil {
		return err
	}
	return s.Syncthing.Scan(ctx, s.FolderID)
}

func (s *Server) waitIdle(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		fs, err := s.Syncthing.FolderStatus(ctx, s.FolderID)
		if err != nil {
			return err
		}
		if fs.State == "idle" && fs.NeedFiles == 0 && fs.Errors == 0 {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout")
}

func buildPatterns(sel Selection) ([]string, error) {
	inc := make([]ignore.Item, 0, len(sel.Included))
	for _, p := range sel.Included {
		inc = append(inc, ignore.Item{Path: p, IsDir: true})
	}
	exc := make([]ignore.Item, 0, len(sel.Excluded))
	for _, p := range sel.Excluded {
		exc = append(exc, ignore.Item{Path: p, IsDir: true})
	}
	return ignore.Patterns(inc, exc)
}

func normalizeAll(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, p := range in {
		c, err := cleanRel(p)
		if err != nil {
			return nil, err
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out, nil
}

func contains(in []string, p string) bool {
	for _, s := range in {
		if s == p {
			return true
		}
	}
	return false
}

func remove(in []string, p string) []string {
	out := in[:0]
	for _, s := range in {
		if s != p {
			out = append(out, s)
		}
	}
	return out
}

func removePrefix(in []string, p string) []string {
	out := in[:0]
	for _, s := range in {
		if s == p || isAncestor(p, s) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func decode(r *http.Request, dst any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func mustSub(f embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

// Apply writes the current selection to Syncthing and triggers a scan.
func (s *Server) Apply(ctx context.Context) error {
	patterns, err := buildPatterns(s.Selection.Get())
	if err != nil {
		return err
	}
	return s.apply(ctx, patterns)
}
