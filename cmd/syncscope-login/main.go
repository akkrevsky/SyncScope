// Command syncscope-login performs the OIDC device flow, then registers the
// local Syncthing device and stores the resulting device token.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

type apiClient struct {
	base string
	http *http.Client
}

func (c *apiClient) do(ctx context.Context, method, path, token string, body any, out any) (int, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 && out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, err
		}
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(data))
		}
		return resp.StatusCode, errors.New(e.Error)
	}
	return resp.StatusCode, nil
}

type deviceAuth struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

func main() {
	control := flag.String("control", "http://127.0.0.1:8088", "control plane URL")
	name := flag.String("name", hostname(), "device name")
	stURL := flag.String("syncthing", "http://127.0.0.1:8385", "Syncthing API URL")
	stKey := flag.String("syncthing-key", os.Getenv("SYNCTHING_API_KEY"), "Syncthing API key")
	address := flag.String("address", "", "this device's Syncthing address (optional)")
	out := flag.String("out", "device-token.json", "where to save the device token")
	flag.Parse()

	if *stKey == "" {
		fmt.Fprintln(os.Stderr, "-syncthing-key or SYNCTHING_API_KEY is required")
		os.Exit(2)
	}
	api := &apiClient{base: strings.TrimRight(*control, "/"), http: &http.Client{Timeout: 30 * time.Second}}
	ctx := context.Background()

	var cfg struct {
		Enabled bool   `json:"enabled"`
		Issuer  string `json:"issuer"`
	}
	if _, err := api.do(ctx, http.MethodGet, "/v1/auth/config", "", nil, &cfg); err != nil {
		fmt.Fprintln(os.Stderr, "auth config:", err)
		os.Exit(1)
	}
	if !cfg.Enabled {
		fmt.Fprintln(os.Stderr, "OIDC is not configured on the control plane")
		os.Exit(1)
	}

	var da deviceAuth
	if _, err := api.do(ctx, http.MethodPost, "/v1/auth/device/start", "", nil, &da); err != nil {
		fmt.Fprintln(os.Stderr, "device flow:", err)
		os.Exit(1)
	}
	fmt.Printf("Откройте: %s\n", da.VerificationURI)
	if da.VerificationURIComplete != "" {
		fmt.Printf("Или сразу: %s\n", da.VerificationURIComplete)
	}
	fmt.Printf("Код: %s\n\n", da.UserCode)

	interval := time.Duration(da.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(da.ExpiresIn) * time.Second)
	var session string
	for time.Now().Before(deadline) {
		time.Sleep(interval)
		var res struct {
			SessionToken string `json:"session_token"`
		}
		code, err := api.do(ctx, http.MethodPost, "/v1/auth/device/poll", "", map[string]string{"device_code": da.DeviceCode}, &res)
		if err != nil {
			if code == http.StatusPreconditionRequired {
				continue
			}
			if code == http.StatusTooManyRequests {
				interval += 2 * time.Second
				continue
			}
			fmt.Fprintln(os.Stderr, "poll:", err)
			os.Exit(1)
		}
		session = res.SessionToken
		break
	}
	if session == "" {
		fmt.Fprintln(os.Stderr, "device flow timed out")
		os.Exit(1)
	}

	var status struct {
		MyID string `json:"myID"`
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(*stURL, "/")+"/rest/system/status", nil)
	req.Header.Set("X-API-Key", *stKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "syncthing:", err)
		os.Exit(1)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "syncthing: HTTP %d: %s\n", resp.StatusCode, strings.TrimSpace(string(body)))
		os.Exit(1)
	}
	if err := json.Unmarshal(body, &status); err != nil || status.MyID == "" {
		fmt.Fprintln(os.Stderr, "syncthing: cannot read device id")
		os.Exit(1)
	}

	var reg struct {
		Device struct {
			ID string `json:"id"`
		} `json:"device"`
		Token string `json:"token"`
	}
	_, err = api.do(ctx, http.MethodPost, "/v1/devices", session, map[string]string{
		"name":         *name,
		"syncthing_id": status.MyID,
		"address":      *address,
		"platform":     runtime.GOOS,
	}, &reg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "register device:", err)
		os.Exit(1)
	}
	saved := map[string]string{
		"control":      strings.TrimRight(*control, "/"),
		"device_id":    reg.Device.ID,
		"device_token": reg.Token,
		"syncthing_id": status.MyID,
	}
	b, _ := json.MarshalIndent(saved, "", "  ")
	if err := os.WriteFile(*out, b, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "save:", err)
		os.Exit(1)
	}
	fmt.Printf("Устройство зарегистрировано: %s\nТокен сохранён: %s\n", reg.Device.ID, *out)
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "device"
	}
	return h
}
