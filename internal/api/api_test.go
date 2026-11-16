package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"syncscope/internal/store"
)

func TestDeviceLibraryCatalogFlow(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "library")
	if err := os.MkdirAll(filepath.Join(root, "2024"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(st, "admin", nil).Handler())
	defer srv.Close()

	post := func(path, token string, body any) (int, map[string]any) {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Admin-Token", token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	if code, _ := post("/v1/devices", "wrong", map[string]any{"syncthing_id": "X"}); code != http.StatusUnauthorized {
		t.Fatalf("wrong admin token: got %d", code)
	}
	code, body := post("/v1/devices", "admin", map[string]any{
		"name": "client", "syncthing_id": "DEVICE-ID", "platform": "linux",
	})
	if code != http.StatusCreated {
		t.Fatalf("create device: %d %v", code, body)
	}
	dev := body["device"].(map[string]any)
	token := body["token"].(string)
	devID := dev["id"].(string)

	code, body = post("/v1/libraries", "admin", map[string]any{"name": "Photo", "root_path": root})
	if code != http.StatusCreated {
		t.Fatalf("create library: %d %v", code, body)
	}
	lib := body["library"].(map[string]any)
	libID := lib["id"].(string)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/catalog/"+libID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("catalog before grant: got %d", resp.StatusCode)
	}

	code, body = post("/v1/libraries/"+libID+"/grants", "admin", map[string]any{"device_id": devID})
	if code != http.StatusCreated {
		t.Fatalf("grant: %d %v", code, body)
	}

	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/v1/catalog/"+libID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("catalog: got %d", resp.StatusCode)
	}
	var out struct {
		Nodes []store.Node `json:"nodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Nodes) != 1 || out.Nodes[0].Name != "2024" {
		t.Fatalf("unexpected nodes: %+v", out.Nodes)
	}
}
