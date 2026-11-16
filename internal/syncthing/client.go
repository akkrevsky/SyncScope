// Package syncthing is a small REST client for the Syncthing API.
package syncthing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

type Status struct {
	MyID    string `json:"myID"`
	Version string `json:"version"`
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var out Status
	err := c.do(ctx, http.MethodGet, "/rest/system/status", nil, &out)
	return out, err
}

func (c *Client) Version(ctx context.Context) (string, error) {
	var out struct {
		Version string `json:"version"`
	}
	err := c.do(ctx, http.MethodGet, "/rest/system/version", nil, &out)
	return out.Version, err
}

// Folder returns the raw folder configuration. The returned map has a
// "devices" key, which is convenient for merging a new device in.
func (c *Client) Folder(ctx context.Context, id string) (map[string]any, error) {
	var out map[string]any
	err := c.do(ctx, http.MethodGet, "/rest/config/folders/"+id, nil, &out)
	return out, err
}

func (c *Client) PutFolder(ctx context.Context, id string, folder map[string]any) error {
	return c.do(ctx, http.MethodPut, "/rest/config/folders/"+id, folder, nil)
}

func (c *Client) DeleteFolder(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/rest/config/folders/"+id, nil, nil)
}

func (c *Client) PutDevice(ctx context.Context, id string, device map[string]any) error {
	return c.do(ctx, http.MethodPut, "/rest/config/devices/"+id, device, nil)
}

func (c *Client) Device(ctx context.Context, id string) (map[string]any, error) {
	var out map[string]any
	err := c.do(ctx, http.MethodGet, "/rest/config/devices/"+id, nil, &out)
	return out, err
}

// EnsureFolder creates the folder if it does not exist, otherwise leaves it
// untouched. It returns the resulting folder configuration.
func (c *Client) EnsureFolder(ctx context.Context, id string, folder map[string]any) (map[string]any, error) {
	got, err := c.Folder(ctx, id)
	if err == nil {
		return got, nil
	}
	if !IsNotFound(err) {
		return nil, err
	}
	if err := c.PutFolder(ctx, id, folder); err != nil {
		return nil, err
	}
	return c.Folder(ctx, id)
}

// AddDeviceToFolder appends a device to the folder's device list, preserving
// other configuration.
func (c *Client) AddDeviceToFolder(ctx context.Context, folderID, deviceID string) error {
	f, err := c.Folder(ctx, folderID)
	if err != nil {
		return err
	}
	devices, _ := f["devices"].([]any)
	for _, d := range devices {
		if m, ok := d.(map[string]any); ok {
			if fmt.Sprint(m["deviceID"]) == deviceID {
				return nil
			}
		}
	}
	f["devices"] = append(devices, map[string]any{"deviceID": deviceID})
	return c.PutFolder(ctx, folderID, f)
}

func (c *Client) SetIgnores(ctx context.Context, folderID string, patterns []string) error {
	body := map[string]any{"ignore": patterns}
	return c.do(ctx, http.MethodPost, "/rest/db/ignores?folder="+folderID, body, nil)
}

func (c *Client) Ignores(ctx context.Context, folderID string) ([]string, error) {
	var out struct {
		Ignore []string `json:"ignore"`
	}
	err := c.do(ctx, http.MethodGet, "/rest/db/ignores?folder="+folderID, nil, &out)
	return out.Ignore, err
}

func (c *Client) Scan(ctx context.Context, folderID string) error {
	return c.do(ctx, http.MethodPost, "/rest/db/scan?folder="+folderID, nil, nil)
}

type FolderStatus struct {
	State      string `json:"state"`
	NeedFiles  int    `json:"needFiles"`
	LocalFiles int    `json:"localFiles"`
	Errors     int    `json:"errors"`
}

func (c *Client) FolderStatus(ctx context.Context, folderID string) (FolderStatus, error) {
	var out FolderStatus
	err := c.do(ctx, http.MethodGet, "/rest/db/status?folder="+folderID, nil, &out)
	return out, err
}

type Connection struct {
	Connected bool   `json:"connected"`
	Type      string `json:"type"`
	Address   string `json:"address"`
}

type Connections struct {
	Connections map[string]Connection `json:"connections"`
}

func (c *Client) Connections(ctx context.Context) (Connections, error) {
	var out Connections
	err := c.do(ctx, http.MethodGet, "/rest/system/connections", nil, &out)
	return out, err
}

type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("syncthing: HTTP %d: %s", e.Status, strings.TrimSpace(e.Body))
}

func IsNotFound(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.Status == http.StatusNotFound
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", c.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{Status: resp.StatusCode, Body: string(data)}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// EnsureDevice creates or updates a device entry. It preserves unrelated
// fields when the device already exists.
func (c *Client) EnsureDevice(ctx context.Context, id, name, address string) error {
	addr := address
	if addr == "" {
		addr = "dynamic"
	}
	dev, err := c.Device(ctx, id)
	if err != nil {
		if !IsNotFound(err) {
			return err
		}
		dev = map[string]any{"deviceID": id}
	}
	dev["deviceID"] = id
	if name != "" {
		dev["name"] = name
	}
	dev["addresses"] = []string{addr}
	return c.PutDevice(ctx, id, dev)
}

// SetFolderDevices replaces the folder's device list with the given IDs.
func (c *Client) SetFolderDevices(ctx context.Context, folderID string, ids []string) error {
	f, err := c.Folder(ctx, folderID)
	if err != nil {
		return err
	}
	devices := make([]any, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		devices = append(devices, map[string]any{"deviceID": id})
	}
	f["devices"] = devices
	return c.PutFolder(ctx, folderID, f)
}
