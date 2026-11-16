// Package api exposes the control-plane HTTP API.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"syncscope/internal/store"
)

type Server struct {
	Store      *store.Store
	AdminToken string
	Log        *slog.Logger
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
	mux.HandleFunc("POST /v1/devices", s.admin(s.createDevice))
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
	d, err := s.Store.CreateDevice(req.Name, req.SyncthingID, req.Address, req.Platform)
	if err != nil {
		fail(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"device": d, "token": d.Token})
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
	Name     string `json:"name"`
	RootPath string `json:"root_path"`
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
