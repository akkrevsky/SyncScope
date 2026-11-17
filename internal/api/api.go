// Package api exposes the control-plane HTTP API.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"syncscope/internal/oidc"
	"syncscope/internal/share"
	"syncscope/internal/store"
)

type Server struct {
	Store       *store.Store
	AdminToken  string
	Log         *slog.Logger
	OIDC        *oidc.Client
	Issuer      string
	ShareSecret string
}

func New(st *store.Store, adminToken string, log *slog.Logger) *Server {
	if adminToken == "" {
		adminToken = "dev-admin-token"
	}
	if log == nil {
		log = slog.Default()
	}
	return &Server{Store: st, AdminToken: adminToken, Log: log}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.health)
	mux.HandleFunc("GET /v1/auth/config", s.authConfig)
	mux.HandleFunc("POST /v1/auth/device/start", s.authDeviceStart)
	mux.HandleFunc("POST /v1/auth/device/poll", s.authDevicePoll)
	mux.HandleFunc("GET /v1/me", s.me)
	mux.HandleFunc("POST /v1/shares", s.device(s.createShare))
	mux.HandleFunc("POST /v1/devices", s.createDevice)
	mux.HandleFunc("GET /v1/devices", s.admin(s.listDevices))
	mux.HandleFunc("POST /v1/devices/{id}/revoke", s.admin(s.revokeDevice))
	mux.HandleFunc("POST /v1/libraries", s.admin(s.createLibrary))
	mux.HandleFunc("GET /v1/libraries", s.device(s.listLibraries))
	mux.HandleFunc("POST /v1/libraries/{id}/grants", s.admin(s.addGrant))
	mux.HandleFunc("GET /v1/catalog/{id}", s.device(s.catalog))
	mux.HandleFunc("GET /v1/origin/libraries", s.admin(s.originLibraries))
	return s.logRequests(mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type deviceRequest struct {
	Name        string `json:"name"`
	SyncthingID string `json:"syncthing_id"`
	Address     string `json:"address"`
	Platform    string `json:"platform"`
}

func (s *Server) createDevice(w http.ResponseWriter, r *http.Request) {
	var req deviceRequest
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	var d *store.Device
	var err error
	if s.isAdmin(r) {
		d, err = s.Store.CreateDevice(req.Name, req.SyncthingID, req.Address, req.Platform)
	} else if u, ok := s.Store.UserBySession(bearer(r)); ok {
		d, err = s.Store.CreateDeviceForUser(u.ID, req.Name, req.SyncthingID, req.Address, req.Platform)
	} else {
		fail(w, http.StatusUnauthorized, errors.New("admin token or user session required"))
		return
	}
	if err != nil {
		fail(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"device": d, "token": d.Token})
}

func (s *Server) authConfig(w http.ResponseWriter, r *http.Request) {
	enabled := s.OIDC != nil
	out := map[string]any{"enabled": enabled}
	if enabled {
		out["issuer"] = s.Issuer
		out["flow"] = "device_code"
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) authDeviceStart(w http.ResponseWriter, r *http.Request) {
	if s.OIDC == nil {
		fail(w, http.StatusNotImplemented, errors.New("OIDC is not configured"))
		return
	}
	da, err := s.OIDC.StartDevice(r.Context())
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, da)
}

type pollRequest struct {
	DeviceCode string `json:"device_code"`
}

func (s *Server) authDevicePoll(w http.ResponseWriter, r *http.Request) {
	if s.OIDC == nil {
		fail(w, http.StatusNotImplemented, errors.New("OIDC is not configured"))
		return
	}
	var req pollRequest
	if err := decode(r, &req); err != nil || req.DeviceCode == "" {
		fail(w, http.StatusBadRequest, errors.New("device_code is required"))
		return
	}
	tok, err := s.OIDC.Poll(r.Context(), req.DeviceCode)
	if err != nil {
		switch {
		case errors.Is(err, oidc.ErrPending):
			fail(w, http.StatusPreconditionRequired, errors.New("authorization_pending"))
		case errors.Is(err, oidc.ErrSlowDown):
			fail(w, http.StatusTooManyRequests, errors.New("slow_down"))
		default:
			fail(w, http.StatusBadGateway, err)
		}
		return
	}
	ui, err := s.OIDC.UserInfo(r.Context(), tok)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	u, err := s.Store.CreateOrGetUser(ui.Subject, ui.Email, ui.Name)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	sess, err := s.Store.CreateSession(u.ID, 30*24*time.Hour)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session_token": sess.Token, "user": u})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, ok := s.Store.UserBySession(bearer(r))
	if !ok {
		fail(w, http.StatusUnauthorized, errors.New("user session required"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	type view struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		SyncthingID string `json:"syncthing_id"`
		Address     string `json:"address,omitempty"`
		Platform    string `json:"platform,omitempty"`
		Status      string `json:"status"`
	}
	var out []view
	for _, d := range s.Store.ListDevices() {
		out = append(out, view{d.ID, d.Name, d.SyncthingID, d.Address, d.Platform, d.Status})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

func (s *Server) revokeDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.RevokeDevice(r.PathValue("id")); err != nil {
		fail(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

type libraryRequest struct {
	Name      string `json:"name"`
	RootPath  string `json:"root_path"`
	OriginURL string `json:"origin_url"`
}

func (s *Server) createLibrary(w http.ResponseWriter, r *http.Request) {
	var req libraryRequest
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	lib, err := s.Store.CreateLibrary(req.Name, req.RootPath)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if req.OriginURL != "" {
		if err := s.Store.SetLibraryOrigin(lib.ID, req.OriginURL); err != nil {
			fail(w, http.StatusInternalServerError, err)
			return
		}
		lib, _ = s.Store.Library(lib.ID)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"library": lib})
}

func (s *Server) listLibraries(w http.ResponseWriter, r *http.Request, dev *store.Device) {
	libs := s.Store.LibrariesForDevice(dev.ID)
	writeJSON(w, http.StatusOK, map[string]any{"libraries": libs})
}

type grantRequest struct {
	DeviceID string `json:"device_id"`
}

func (s *Server) addGrant(w http.ResponseWriter, r *http.Request) {
	var req grantRequest
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Store.AddGrant(r.PathValue("id"), req.DeviceID); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "granted"})
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request, dev *store.Device) {
	libID := r.PathValue("id")
	if _, ok := s.Store.Library(libID); !ok {
		fail(w, http.StatusNotFound, errors.New("library not found"))
		return
	}
	nodes, err := s.Store.Catalog(dev.ID, libID, r.URL.Query().Get("parent"))
	if err != nil {
		fail(w, http.StatusForbidden, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes, "version": s.Store.CatalogVersion(libID)})
}

type createShareRequest struct {
	Library   string `json:"library"`
	Path      string `json:"path"`
	IsDir     bool   `json:"is_dir"`
	ExpiresIn int    `json:"expires_in"`
	Password  string `json:"password"`
}

func (s *Server) createShare(w http.ResponseWriter, r *http.Request, dev *store.Device) {
	if s.ShareSecret == "" {
		fail(w, http.StatusNotImplemented, errors.New("sharing is not configured"))
		return
	}
	var req createShareRequest
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if req.Library == "" || req.Path == "" {
		fail(w, http.StatusBadRequest, errors.New("library and path are required"))
		return
	}
	if !s.Store.DeviceCanAccess(dev.ID, req.Library) {
		fail(w, http.StatusForbidden, errors.New("no access to library"))
		return
	}
	lib, ok := s.Store.Library(req.Library)
	if !ok {
		fail(w, http.StatusNotFound, errors.New("library not found"))
		return
	}
	if lib.OriginURL == "" {
		fail(w, http.StatusConflict, errors.New("library has no origin_url"))
		return
	}
	ttl := req.ExpiresIn
	if ttl <= 0 {
		ttl = 7 * 24 * 3600
	}
	if ttl > 30*24*3600 {
		ttl = 30 * 24 * 3600
	}
	path := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(req.Path)), "/")
	if path == "" || path == "." || strings.HasPrefix(path, "../") {
		fail(w, http.StatusBadRequest, errors.New("invalid path"))
		return
	}
	expires := time.Now().Add(time.Duration(ttl) * time.Second)
	p := share.Payload{
		Library: lib.ID,
		Path:    path,
		Dir:     req.IsDir,
		Expires: expires.Unix(),
	}
	if req.Password != "" {
		p.Password = share.PasswordHash(req.Password)
	}
	token, err := share.Sign(s.ShareSecret, p)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	url := strings.TrimRight(lib.OriginURL, "/") + "/s/" + token
	writeJSON(w, http.StatusCreated, map[string]any{"url": url, "token": token, "expires_at": expires})
}

type originDevice struct {
	ID          string `json:"id"`
	SyncthingID string `json:"syncthing_id"`
	Address     string `json:"address,omitempty"`
}

func (s *Server) originLibraries(w http.ResponseWriter, r *http.Request) {
	type originLibrary struct {
		Library *store.Library `json:"library"`
		Devices []originDevice `json:"devices"`
	}
	var out []originLibrary
	for _, lib := range s.Store.ListLibraries() {
		item := originLibrary{Library: lib}
		for _, g := range s.Store.GrantsForLibrary(lib.ID) {
			d, ok := s.Store.Device(g.DeviceID)
			if !ok || d.Status != "active" {
				continue
			}
			item.Devices = append(item.Devices, originDevice{
				ID:          d.ID,
				SyncthingID: d.SyncthingID,
				Address:     d.Address,
			})
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"libraries": out})
}

func (s *Server) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isAdmin(r) {
			fail(w, http.StatusUnauthorized, errors.New("admin token required"))
			return
		}
		next(w, r)
	}
}

func (s *Server) device(next func(http.ResponseWriter, *http.Request, *store.Device)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		d, ok := s.Store.DeviceByToken(tok)
		if !ok {
			fail(w, http.StatusUnauthorized, errors.New("device token required"))
			return
		}
		next(w, r, d)
	}
}

func (s *Server) isAdmin(r *http.Request) bool {
	if r.Header.Get("X-Admin-Token") == s.AdminToken {
		return true
	}
	return bearer(r) == s.AdminToken
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return ""
}

func decode(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Log.Info("request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
