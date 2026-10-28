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
