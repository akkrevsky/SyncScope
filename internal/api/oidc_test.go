package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"syncscope/internal/oidc"
	"syncscope/internal/store"
)

func TestOIDCDeviceFlowAndRegistration(t *testing.T) {
	var issuer string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeJSON(w, 200, map[string]string{
				"device_authorization_endpoint": issuer + "/device",
				"token_endpoint":                issuer + "/token",
				"userinfo_endpoint":             issuer + "/userinfo",
			})
		case "/device":
			_ = r.ParseForm()
			if r.Form.Get("client_id") != "client" {
				http.Error(w, "client", 400)
				return
			}
			writeJSON(w, 200, map[string]any{
				"device_code":      "dev-code",
				"user_code":        "USER-1",
				"verification_uri": issuer + "/verify",
				"expires_in":       600,
				"interval":         1,
			})
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("device_code") != "dev-code" {
				writeJSON(w, 400, map[string]string{"error": "authorization_pending"})
				return
			}
			writeJSON(w, 200, map[string]string{"access_token": "access-1", "token_type": "Bearer"})
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer access-1" {
				http.Error(w, "auth", 401)
				return
			}
			writeJSON(w, 200, map[string]string{"sub": "user-1", "email": "a@example.com", "name": "Alice"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer mock.Close()
	issuer = mock.URL

	oc, err := oidc.New(context.Background(), oidc.Config{Issuer: issuer, ClientID: "client"})
	if err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := New(st, "admin", nil)
	srv.OIDC = oc
	srv.Issuer = issuer
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	post := func(path, bearer string, body any) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, ts.URL+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	code, body := post("/v1/auth/device/start", "", nil)
	if code != 200 || body["device_code"] != "dev-code" {
		t.Fatalf("start: %d %v", code, body)
	}
	code, body = post("/v1/auth/device/poll", "", map[string]string{"device_code": "dev-code"})
	if code != 200 {
		t.Fatalf("poll: %d %v", code, body)
	}
	session, _ := body["session_token"].(string)
	if session == "" {
		t.Fatalf("no session token: %v", body)
	}

	code, body = post("/v1/devices", session, map[string]string{
		"name": "laptop", "syncthing_id": "DEVICE-ID", "address": "tcp://127.0.0.1:22001",
	})
	if code != 201 {
		t.Fatalf("register: %d %v", code, body)
	}
	d := body["device"].(map[string]any)
	if uid, _ := d["user_id"].(string); uid == "" {
		t.Fatalf("device is not linked to user: %v", d)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var me struct {
		User store.User `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	if me.User.Subject != "user-1" {
		t.Fatalf("unexpected user: %+v", me.User)
	}
}
