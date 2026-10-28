package review

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	return New(Config{PublicURL: "https://review.test", Key: []byte("0123456789abcdef0123456789abcdef")}, nil)
}

func TestSealedCookies(t *testing.T) {
	s := testServer(t)
	raw := s.seal(session{Kind: "session", User: "anton", Exp: time.Now().Add(time.Hour).Unix()})
	var got session
	if !s.open(raw, &got) || got.User != "anton" {
		t.Fatalf("round trip: %+v", got)
	}
	if s.open(raw[:len(raw)-2]+"xx", &got) {
		t.Error("tampered signature accepted")
	}
	other := New(Config{Key: []byte("ffffffffffffffffffffffffffffffff")}, nil)
	if other.open(raw, &got) {
		t.Error("another key accepted")
	}
	f := s.seal(flow{Kind: "flow", Exp: time.Now().Add(time.Hour).Unix()})
	if _, ok := s.sessionFrom(f); ok {
		t.Error("a login cookie works as a session")
	}
	expired := s.seal(session{Kind: "session", User: "anton", Exp: time.Now().Add(-time.Minute).Unix()})
	if _, ok := s.sessionFrom(expired); ok {
		t.Error("expired session accepted")
	}
	if s.csrfToken(raw) == s.csrfToken(expired) {
		t.Error("csrf token does not depend on the session")
	}
}

func TestLoadKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "review.key")
	a, err := LoadKey(p)
	if err != nil || len(a) != 32 {
		t.Fatal(len(a), err)
	}
	b, err := LoadKey(p)
	if err != nil || string(a) != string(b) {
		t.Error("key changed between loads")
	}
	os.WriteFile(p, []byte("short\n"), 0o600)
	if _, err := LoadKey(p); err == nil {
		t.Error("corrupt key accepted")
	}
}
