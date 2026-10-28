package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadExampleConfig(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join("..", "..", "md-vault-mcp.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EditWindow != 48*time.Hour || cfg.Read.Mode != "allowlist" || len(cfg.Write.Allow) != 2 || cfg.Listen != "127.0.0.1:8093" ||
		cfg.DatabaseURL != "host=/run/postgresql dbname=md_vault_mcp" {
		t.Errorf("got %+v", cfg)
	}
}

func TestConfigRejectsUnknownKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(p, []byte("vault = \"/v\"\nstate_dir = \"/s\"\n[read]\nalow = [\"x\"]\n"), 0o600)
	if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), "read.alow") {
		t.Errorf("got %v", err)
	}
}

func TestConfigNeedsDatabase(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(p, []byte("vault = \"/v\"\nstate_dir = \"/s\"\n"), 0o600)
	if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), "database_url") {
		t.Errorf("got %v", err)
	}
}

func TestConfigReview(t *testing.T) {
	base := "vault = \"/v\"\nstate_dir = \"/s\"\ndatabase_url = \"x\"\n"
	full := base + `[review]
listen = "127.0.0.1:8193"
public_url = "https://notes.example.org/"
allowed_groups = ["admins"]
oidc_issuer = "https://id.example.org/oauth2/openid/notes"
oidc_client_id = "notes"
oidc_client_secret_file = "/run/secret"
`
	p := filepath.Join(t.TempDir(), "c.toml")
	os.WriteFile(p, []byte(full), 0o600)
	cfg, err := LoadConfig(p)
	if err != nil || cfg.Review == nil || cfg.Review.PublicURL != "https://notes.example.org" || cfg.Review.OIDCClientID != "notes" {
		t.Fatalf("got %+v %v", cfg.Review, err)
	}
	os.WriteFile(p, []byte(base), 0o600)
	if cfg, err := LoadConfig(p); err != nil || cfg.Review != nil {
		t.Errorf("without review: %+v %v", cfg.Review, err)
	}
	os.WriteFile(p, []byte(strings.Replace(full, "oidc_issuer", "#", 1)), 0o600)
	if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), "review.oidc_issuer") {
		t.Errorf("missing issuer: %v", err)
	}
	os.WriteFile(p, []byte(strings.Replace(full, "https://notes", "notes", 1)), 0o600)
	if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), "public_url") {
		t.Errorf("bad url: %v", err)
	}
}
