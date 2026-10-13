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
	EditWindow  time.Duration
	MaxFileSize int64
	Extensions  []string
	Read        access.RuleSet
	Write       access.RuleSet
	Transport   string
	Listen      string
	TokenFile   string
}

type fileConfig struct {
	Vault       string         `toml:"vault"`
	StateDir    string         `toml:"state_dir"`
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
	return cfg, nil
}
