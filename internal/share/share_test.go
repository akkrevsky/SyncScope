package share

import (
	"testing"
	"time"
)

func TestSignVerify(t *testing.T) {
	p := Payload{Library: "lib", Path: "docs", Dir: true, Expires: time.Now().Add(time.Hour).Unix(), Password: PasswordHash("secret")}
	tok, err := Sign("key", p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Verify("key", tok)
	if err != nil {
		t.Fatal(err)
	}
	if got.Library != p.Library || got.Path != p.Path || got.Password != p.Password {
		t.Fatalf("got %+v", got)
	}
	if _, err := Verify("other", tok); err == nil {
		t.Fatal("wrong secret accepted")
	}
	if _, err := Verify("key", tok+"x"); err == nil {
		t.Fatal("tampered token accepted")
	}
}

func TestExpired(t *testing.T) {
	tok, err := Sign("key", Payload{Library: "lib", Path: "docs", Expires: time.Now().Add(-time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Verify("key", tok); err == nil {
		t.Fatal("expired token accepted")
	}
}
