// Package originshare serves signed public share pages directly from the
// origin library. The control plane only signs links; no file data flows
// through it.
package originshare

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"syncscope/internal/share"
)

type Server struct {
	Secret string
	Base   string
	Log    *slog.Logger

	mu    sync.RWMutex
	roots map[string]string
}

func New(secret, base string, log *slog.Logger) *Server {
	if base == "" {
		base = "/"
	}
	if !strings.HasPrefix(base, "/") {
		base = "/" + base
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return &Server{Secret: secret, Base: base, Log: log, roots: map[string]string{}}
}

func (s *Server) SetLibraries(roots map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roots = roots
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /s/{token}", s.page)
	mux.HandleFunc("GET /s/{token}/download", s.download)
	return mux
}

func (s *Server) root(library string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	root, ok := s.roots[library]
	return root, ok
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	p, err := share.Verify(s.Secret, r.PathValue("token"))
	if err != nil {
		http.Error(w, "Ссылка недействительна или истекла", http.StatusNotFound)
		return
	}
	root, ok := s.root(p.Library)
	if !ok {
		http.Error(w, "Библиотека не найдена", http.StatusNotFound)
		return
	}
	if p.Password != "" && !s.passwordOK(w, r, p) {
		render(w, http.StatusUnauthorized, pageData{
			Title:            "Защищённая ссылка",
			Base:             s.Base,
			Token:            r.PathValue("token"),
			PasswordRequired: true,
			Error:            passwordError(r),
		})
		return
	}

	rel, err := cleanQuery(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, "Некорректный путь", http.StatusBadRequest)
		return
	}
	targetRel := p.Path
	if p.Dir {
		targetRel = path.Join(p.Path, rel)
	}
	target, ok := joinInside(root, targetRel)
	if !ok {
		http.Error(w, "Некорректный путь", http.StatusBadRequest)
		return
	}
	st, err := os.Stat(target)
	if err != nil {
		http.Error(w, "Не найдено", http.StatusNotFound)
		return
	}

	if !st.IsDir() {
		render(w, http.StatusOK, pageData{
			Title:    filepath.Base(target),
			Base:     s.Base,
			Token:    r.PathValue("token"),
			IsFile:   true,
			Name:     filepath.Base(target),
			Size:     humanSize(st.Size()),
			Modified: st.ModTime().Format("02.01.2006 15:04"),
		})
		return
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		http.Error(w, "Не удалось прочитать папку", http.StatusInternalServerError)
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	var list []entry
	for _, e := range entries {
		name := e.Name()
		if name == ".stfolder" || name == ".stversions" || name == ".stignore" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		childRel := path.Join(rel, name)
		list = append(list, entry{
			Name:     name,
			Path:     childRel,
			IsDir:    e.IsDir(),
			Size:     humanSize(info.Size()),
			Modified: info.ModTime().Format("02.01.2006 15:04"),
		})
	}
	parent := ""
	if rel != "" {
		parent = path.Dir(rel)
		if parent == "." {
			parent = ""
		}
	}
	title := filepath.Base(p.Path)
	if rel != "" {
		title = path.Base(rel)
	}
	render(w, http.StatusOK, pageData{
		Title:    title,
		Base:     s.Base,
		Token:    r.PathValue("token"),
		Path:     rel,
		Parent:   parent,
		Entries:  list,
		IsFolder: true,
	})
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	p, err := share.Verify(s.Secret, r.PathValue("token"))
	if err != nil {
		http.Error(w, "Ссылка недействительна или истекла", http.StatusNotFound)
		return
	}
	root, ok := s.root(p.Library)
	if !ok {
		http.Error(w, "Библиотека не найдена", http.StatusNotFound)
		return
	}
	if p.Password != "" && !s.passwordOK(w, r, p) {
		http.Error(w, "Требуется пароль", http.StatusUnauthorized)
		return
	}
	rel, err := cleanQuery(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, "Некорректный путь", http.StatusBadRequest)
		return
	}
	targetRel := p.Path
	if p.Dir {
		targetRel = path.Join(p.Path, rel)
	}
	target, ok := joinInside(root, targetRel)
	if !ok {
		http.Error(w, "Некорректный путь", http.StatusBadRequest)
		return
	}
	st, err := os.Stat(target)
	if err != nil || st.IsDir() {
		http.Error(w, "Не найдено", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(filepath.Base(target)))
	http.ServeFile(w, r, target)
}

func (s *Server) passwordOK(w http.ResponseWriter, r *http.Request, p share.Payload) bool {
	if q := r.URL.Query().Get("password"); q != "" {
		if share.PasswordHash(q) == p.Password {
			http.SetCookie(w, &http.Cookie{
				Name:     cookieName(r.PathValue("token")),
				Value:    p.Password,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
				MaxAge:   3600,
			})
			return true
		}
		return false
	}
	c, err := r.Cookie(cookieName(r.PathValue("token")))
	return err == nil && c.Value == p.Password
}

func cookieName(token string) string {
	if len(token) > 12 {
		token = token[:12]
	}
	return "scope_share_" + token
}

func passwordError(r *http.Request) string {
	if r.URL.Query().Get("password") != "" {
		return "Неверный пароль"
	}
	return ""
}

func cleanQuery(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	p = strings.TrimPrefix(p, "/")
	if p == "" || p == "." {
		return "", nil
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("invalid path")
	}
	return c, nil
}

func joinInside(root, rel string) (string, bool) {
	target := filepath.Join(root, filepath.FromSlash(rel))
	r, err := filepath.Rel(root, target)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return target, true
}

func humanSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d Б", n)
	}
	units := []string{"КБ", "МБ", "ГБ", "ТБ"}
	v := float64(n) / 1024
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if v >= 10 {
		return fmt.Sprintf("%.0f %s", v, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

type entry struct {
	Name     string
	Path     string
	IsDir    bool
	Size     string
	Modified string
}

type pageData struct {
	Title            string
	Base             string
	Token            string
	Path             string
	Parent           string
	Entries          []entry
	IsFolder         bool
	IsFile           bool
	Name             string
	Size             string
	Modified         string
	PasswordRequired bool
	Error            string
}

var page = template.Must(template.New("share").Parse(`<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} — SyncScope share</title>
<style>
:root{--bg:#f3f5f8;--panel:#fff;--ink:#1c2430;--muted:#7b8797;--line:#e4e8ee;--accent:#2563eb}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font:14px/1.45 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}
header{height:56px;display:flex;align-items:center;gap:10px;padding:0 18px;background:var(--panel);border-bottom:1px solid var(--line)}
.logo{width:28px;height:28px;border-radius:8px;background:var(--accent);color:#fff;display:grid;place-items:center;font-weight:800}
main{max-width:1040px;margin:22px auto;padding:0 18px}
.card{background:var(--panel);border:1px solid var(--line);border-radius:12px;overflow:hidden;box-shadow:0 8px 28px rgba(16,24,40,.06)}
.head{padding:14px 16px;border-bottom:1px solid var(--line)}
h1{margin:0;font-size:18px}.sub{color:var(--muted);font-size:12px;margin-top:4px}
table{width:100%;border-collapse:collapse}td,th{text-align:left;padding:9px 14px;border-bottom:1px solid var(--line);font-size:13px}th{color:var(--muted);font-weight:600;background:#fbfcfd}
tr:last-child td{border-bottom:0}a{color:var(--accent);text-decoration:none}a:hover{text-decoration:underline}
.muted{color:var(--muted)}.empty{padding:36px;text-align:center;color:var(--muted)}
.password{padding:26px;max-width:420px}.password input{width:100%;padding:9px 10px;border:1px solid var(--line);border-radius:8px;margin:8px 0 12px}
.password button{padding:8px 14px;border:0;border-radius:8px;background:var(--accent);color:#fff;cursor:pointer}
.error{color:#b42318;font-size:12px;margin-bottom:8px}
.file{padding:28px}.file b{font-size:16px}.btn{display:inline-block;margin-top:14px;padding:9px 14px;border-radius:8px;background:var(--accent);color:#fff}
</style></head><body>
<header><span class="logo">S</span><b>SyncScope</b><span class="muted">публичная ссылка</span></header>
<main><div class="card">
{{if .PasswordRequired}}
<div class="password"><h1>{{.Title}}</h1>{{if .Error}}<div class="error">{{.Error}}</div>{{end}}
<form method="get" action="{{.Base}}s/{{.Token}}"><label class="muted">Пароль</label><input type="password" name="password" autofocus><button type="submit">Открыть</button></form></div>
{{else if .IsFile}}
<div class="file"><b>{{.Name}}</b><div class="sub">{{.Size}} · {{.Modified}}</div><a class="btn" href="{{.Base}}s/{{.Token}}/download">Скачать</a></div>
{{else}}
<div class="head"><h1>{{.Title}}</h1><div class="sub">{{if .Parent}}<a href="{{.Base}}s/{{.Token}}?path={{.Parent}}">← Наверх</a> · {{end}}SyncScope</div></div>
{{if .Entries}}<table><thead><tr><th>Имя</th><th>Размер</th><th>Изменён</th></tr></thead><tbody>
{{range .Entries}}<tr><td>{{if .IsDir}}<a href="{{$.Base}}s/{{$.Token}}?path={{.Path}}">📁 {{.Name}}</a>{{else}}<a href="{{$.Base}}s/{{$.Token}}/download?path={{.Path}}">📄 {{.Name}}</a>{{end}}</td><td class="muted">{{if .IsDir}}—{{else}}{{.Size}}{{end}}</td><td class="muted">{{.Modified}}</td></tr>{{end}}
</tbody></table>{{else}}<div class="empty">Папка пуста</div>{{end}}
{{end}}
</div></main></body></html>`))

func render(w http.ResponseWriter, status int, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := page.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
