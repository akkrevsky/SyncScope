// Package share signs self-contained public share links.
package share

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Payload struct {
	Library  string `json:"lib"`
	Path     string `json:"path"`
	Dir      bool   `json:"dir"`
	Expires  int64  `json:"exp"`
	Password string `json:"pwd,omitempty"`
}

func PasswordHash(password string) string {
	sum := sha256.Sum256([]byte("syncscope-share:" + password))
	return hex.EncodeToString(sum[:])
}

func Sign(secret string, p Payload) (string, error) {
	if secret == "" {
		return "", fmt.Errorf("share secret is empty")
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "." + sig, nil
}

func Verify(secret, token string) (Payload, error) {
	var p Payload
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return p, errors.New("malformed share token")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return p, errors.New("bad share signature")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, err
	}
	if p.Library == "" || p.Path == "" {
		return p, errors.New("incomplete share payload")
	}
	if p.Expires < time.Now().Unix() {
		return p, errors.New("share link expired")
	}
	return p, nil
}
