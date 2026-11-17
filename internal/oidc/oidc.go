// Package oidc implements the parts of OpenID Connect needed for the
// device authorization flow and user info lookup.
package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrPending  = errors.New("authorization_pending")
	ErrSlowDown = errors.New("slow_down")
)

type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	Scopes       string
}

type Client struct {
	cfg         Config
	http        *http.Client
	deviceURL   string
	tokenURL    string
	userinfoURL string
}

type DeviceAuth struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type UserInfo struct {
	Subject string `json:"sub"`
	Email   string `json:"email"`
	Name    string `json:"name"`
}

type discovery struct {
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint"`
	TokenEndpoint               string `json:"token_endpoint"`
	UserinfoEndpoint            string `json:"userinfo_endpoint"`
}

func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" {
		return nil, fmt.Errorf("oidc: issuer and client_id are required")
	}
	if cfg.Scopes == "" {
		cfg.Scopes = "openid profile email"
	}
	c := &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: 20 * time.Second},
	}
	discoURL := strings.TrimRight(cfg.Issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("oidc discovery: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var d discovery
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	if d.DeviceAuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.UserinfoEndpoint == "" {
		return nil, fmt.Errorf("oidc discovery: missing endpoints")
	}
	c.deviceURL = d.DeviceAuthorizationEndpoint
	c.tokenURL = d.TokenEndpoint
	c.userinfoURL = d.UserinfoEndpoint
	return c, nil
}

func (c *Client) StartDevice(ctx context.Context) (DeviceAuth, error) {
	form := url.Values{}
	form.Set("client_id", c.cfg.ClientID)
	form.Set("scope", c.cfg.Scopes)
	if c.cfg.ClientSecret != "" {
		form.Set("client_secret", c.cfg.ClientSecret)
	}
	var out DeviceAuth
	if err := c.postForm(ctx, c.deviceURL, form, &out); err != nil {
		return out, err
	}
	if out.Interval <= 0 {
		out.Interval = 5
	}
	return out, nil
}

func (c *Client) Poll(ctx context.Context, deviceCode string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	form.Set("device_code", deviceCode)
	form.Set("client_id", c.cfg.ClientID)
	if c.cfg.ClientSecret != "" {
		form.Set("client_secret", c.cfg.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		switch e.Error {
		case "authorization_pending":
			return "", ErrPending
		case "slow_down":
			return "", ErrSlowDown
		default:
			return "", fmt.Errorf("oidc token: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("oidc token: empty access_token")
	}
	return tok.AccessToken, nil
}

func (c *Client) UserInfo(ctx context.Context, accessToken string) (UserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.userinfoURL, nil)
	if err != nil {
		return UserInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return UserInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return UserInfo{}, fmt.Errorf("oidc userinfo: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var ui UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&ui); err != nil {
		return UserInfo{}, err
	}
	if ui.Subject == "" {
		return UserInfo{}, fmt.Errorf("oidc userinfo: missing sub")
	}
	return ui, nil
}

func (c *Client) postForm(ctx context.Context, endpoint string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oidc: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}
