// Package control is a small client for the control-plane API.
package control

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"syncscope/internal/store"
)

type Client struct {
	BaseURL    string
	Token      string
	AdminToken string
	HTTP       *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

type OriginDevice struct {
	ID          string `json:"id"`
	SyncthingID string `json:"syncthing_id"`
	Address     string `json:"address,omitempty"`
}

type OriginLibrary struct {
	Library store.Library  `json:"library"`
	Devices []OriginDevice `json:"devices"`
}

func (c *Client) OriginLibraries(ctx context.Context) ([]OriginLibrary, error) {
	var out struct {
		Libraries []OriginLibrary `json:"libraries"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/origin/libraries", nil, &out)
	return out.Libraries, err
}

func (c *Client) Libraries(ctx context.Context) ([]store.Library, error) {
	var out struct {
		Libraries []store.Library `json:"libraries"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/libraries", nil, &out)
	return out.Libraries, err
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
	if c.AdminToken != "" {
		req.Header.Set("X-Admin-Token", c.AdminToken)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
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
		return fmt.Errorf("control: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

type CatalogPage struct {
	Nodes   []store.Node `json:"nodes"`
	Version uint64       `json:"version"`
}

func (c *Client) Catalog(ctx context.Context, libraryID, parent string) (CatalogPage, error) {
	q := url.Values{}
	if parent != "" {
		q.Set("parent", parent)
	}
	var out CatalogPage
	path := "/v1/catalog/" + url.PathEscape(libraryID)
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
