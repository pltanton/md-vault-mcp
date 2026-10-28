package notes

import (
	"fmt"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/pltanton/md-vault-mcp/internal/access"
)

type Config struct {
	Vault       string
	StateDir    string
	DatabaseURL string
	EditWindow  time.Duration
	MaxFileSize int64
	Extensions  []string
	Read        access.RuleSet
	Write       access.RuleSet
	Transport   string
	Listen      string
	TokenFile   string
	Review      *ReviewConfig
}

type ReviewConfig struct {
	Listen               string   `toml:"listen"`
	PublicURL            string   `toml:"public_url"`
	AllowedGroups        []string `toml:"allowed_groups"`
	OIDCIssuer           string   `toml:"oidc_issuer"`
	OIDCClientID         string   `toml:"oidc_client_id"`
	OIDCClientSecretFile string   `toml:"oidc_client_secret_file"`
}

type fileConfig struct {
	Vault       string         `toml:"vault"`
	StateDir    string         `toml:"state_dir"`
	DatabaseURL string         `toml:"database_url"`
	EditWindow  string         `toml:"edit_window"`
	MaxFileSize int64          `toml:"max_file_size"`
	Extensions  []string       `toml:"extensions"`
	Read        access.RuleSet `toml:"read"`
	Write       access.RuleSet `toml:"write"`
	Transport   string         `toml:"transport"`
	HTTP        struct {
		Listen    string `toml:"listen"`
		TokenFile string `toml:"token_file"`
	} `toml:"http"`
	Review *ReviewConfig `toml:"review"`
}

func LoadConfig(path string) (Config, error) {
	var fc fileConfig
	md, err := toml.DecodeFile(path, &fc)
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if extra := md.Undecoded(); len(extra) > 0 {
		keys := make([]string, len(extra))
		for i, k := range extra {
			keys[i] = k.String()
		}
		return Config{}, fmt.Errorf("config: unknown keys %s", strings.Join(keys, ", "))
	}
	cfg := Config{
		Vault:       fc.Vault,
		StateDir:    fc.StateDir,
		DatabaseURL: fc.DatabaseURL,
		MaxFileSize: fc.MaxFileSize,
		Extensions:  fc.Extensions,
		Read:        fc.Read,
		Write:       fc.Write,
		Transport:   fc.Transport,
		Listen:      fc.HTTP.Listen,
		TokenFile:   fc.HTTP.TokenFile,
	}
	if fc.EditWindow != "" {
		if cfg.EditWindow, err = time.ParseDuration(fc.EditWindow); err != nil {
			return Config{}, fmt.Errorf("config: edit_window: %w", err)
		}
	}
	if cfg.Vault == "" {
		return Config{}, fmt.Errorf("config: vault is required")
	}
	if cfg.StateDir == "" {
		return Config{}, fmt.Errorf("config: state_dir is required")
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("config: database_url is required")
	}
	if r := fc.Review; r != nil {
		r.PublicURL = strings.TrimRight(r.PublicURL, "/")
		var missing []string
		for _, f := range []struct{ key, value string }{
			{"listen", r.Listen}, {"public_url", r.PublicURL}, {"oidc_issuer", r.OIDCIssuer},
			{"oidc_client_id", r.OIDCClientID}, {"oidc_client_secret_file", r.OIDCClientSecretFile},
		} {
			if f.value == "" {
				missing = append(missing, "review."+f.key)
			}
		}
		if len(r.AllowedGroups) == 0 {
			missing = append(missing, "review.allowed_groups")
		}
		if len(missing) > 0 {
			return Config{}, fmt.Errorf("config: %s required", strings.Join(missing, ", "))
		}
		if !strings.HasPrefix(r.PublicURL, "https://") && !strings.HasPrefix(r.PublicURL, "http://") {
			return Config{}, fmt.Errorf("config: review.public_url must start with https://")
		}
		cfg.Review = r
	}
	return cfg, nil
}
