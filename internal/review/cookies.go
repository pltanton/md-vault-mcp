package review

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const (
	sessionCookie = "mdv_session"
	flowCookie    = "mdv_login"
	sessionTTL    = 12 * time.Hour
	flowTTL       = 10 * time.Minute
)

type session struct {
	Kind   string   `json:"k"`
	User   string   `json:"u"`
	Groups []string `json:"g"`
	Exp    int64    `json:"e"`
}

type flow struct {
	Kind     string `json:"k"`
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Next     string `json:"x"`
	Exp      int64  `json:"e"`
}

func (s *Server) mac(payload string) string {
	m := hmac.New(sha256.New, s.cfg.Key)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Server) seal(v any) string {
	b, _ := json.Marshal(v)
	p := base64.RawURLEncoding.EncodeToString(b)
	return p + "." + s.mac(p)
}

func (s *Server) open(raw string, v any) bool {
	p, sig, ok := strings.Cut(raw, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.mac(p))) {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(p)
	return err == nil && json.Unmarshal(b, v) == nil
}

func (s *Server) sessionFrom(raw string) (session, bool) {
	var sess session
	if !s.open(raw, &sess) || sess.Kind != "session" || sess.Exp < s.now().Unix() {
		return session{}, false
	}
	return sess, true
}

func (s *Server) csrfToken(rawSession string) string { return s.mac("csrf:" + rawSession) }

func (s *Server) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: int(ttl.Seconds()),
		Secure: s.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (s *Server) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1,
		Secure: s.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
