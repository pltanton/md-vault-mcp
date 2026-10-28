package review

import (
	"context"
	"html/template"
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
