package review

import (
	"context"
	"crypto/hmac"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/pltanton/md-vault-mcp/internal/index"
	"github.com/pltanton/md-vault-mcp/internal/notes"
)

type Queue interface {
	ListProposals(ctx context.Context) (pending, decided []index.Proposal, err error)
	GetProposal(ctx context.Context, id string) (index.Proposal, bool, error)
	PreviewProposal(p index.Proposal) []notes.EditPreview
	ApplyProposal(ctx context.Context, id, by, comment string) (index.Proposal, error)
	RejectProposal(ctx context.Context, id, by, comment string) (index.Proposal, error)
}

var _ Queue = (*notes.Service)(nil)

type Config struct {
	PublicURL     string
	AllowedGroups []string
	Issuer        string
	ClientID      string
	ClientSecret  string
	Key           []byte
}

type Server struct {
	cfg    Config
	queue  Queue
	secure bool
	pages  *template.Template
	now    func() time.Time

	mu       sync.Mutex
	provider *oidc.Provider
}

func New(cfg Config, q Queue) *Server {
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	return &Server{cfg: cfg, queue: q, secure: strings.HasPrefix(cfg.PublicURL, "https://"), pages: parsePages(), now: time.Now}
}

// The provider keeps the context for later key fetches, so it must not be a request's.
func (s *Server) oidcClient() (*oidc.Provider, *oauth2.Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.provider == nil {
		p, err := oidc.NewProvider(context.Background(), s.cfg.Issuer)
		if err != nil {
			return nil, nil, err
		}
		s.provider = p
	}
	return s.provider, &oauth2.Config{
		ClientID:     s.cfg.ClientID,
		ClientSecret: s.cfg.ClientSecret,
		Endpoint:     s.provider.Endpoint(),
		RedirectURL:  s.cfg.PublicURL + "/callback",
		Scopes:       []string{oidc.ScopeOpenID, "profile", "groups"},
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", s.login)
	mux.HandleFunc("GET /callback", s.callback)
	mux.Handle("POST /logout", s.auth(s.logout))
	mux.Handle("GET /{$}", s.auth(s.list))
	mux.Handle("GET /p/{id}", s.auth(s.show))
	mux.Handle("POST /p/{id}/apply", s.auth(s.decide(true)))
	mux.Handle("POST /p/{id}/reject", s.auth(s.decide(false)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) auth(next func(http.ResponseWriter, *http.Request, viewer)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sess session
		var raw string
		ok := false
		if c, err := r.Cookie(sessionCookie); err == nil {
			raw = c.Value
			sess, ok = s.sessionFrom(raw)
		}
		if !ok {
			if r.Method != http.MethodGet {
				s.message(w, http.StatusForbidden, viewer{}, "Сессия закончилась", "Войди заново и повтори.")
				return
			}
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
			return
		}
		v := viewer{User: sess.User, CSRF: s.csrfToken(raw)}
		if r.Method == http.MethodPost && !hmac.Equal([]byte(r.PostFormValue("csrf")), []byte(v.CSRF)) {
			s.message(w, http.StatusForbidden, v, "Форма устарела", "Обнови страницу и повтори.")
			return
		}
		next(w, r, v)
	})
}
