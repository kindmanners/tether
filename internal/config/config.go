// Package agentconfig holds an Agent's own local, operator-set
// configuration: where its llama.cpp ggml-rpc-server binary lives and which
// local address it exposes. This is deliberately NOT something the
// Orchestrator can set remotely: a compromised Orchestrator must never be
// able to choose a binary or make the Agent bind an arbitrary address.
package agentconfig

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultPath returns the standard location for an Agent's local config:
// the same platform-specific config directory internal/certs and
// internal/trust already use (%AppData%\tether on Windows,
// ~/.config/tether on Linux), NOT relative to the working directory like
// node_allowlist.yaml. This file is genuinely per-machine, operator-set
// state — Ataraxia's rpc_server_path and rpc_listen_host will legitimately
// differ from Mathesis's — so it belongs alongside that machine's own
// identity and trust store, not checked into git or assumed to be copied
// between machines the way the shared node_allowlist.yaml is.
func DefaultPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user config dir: %w", err)
	}
	return filepath.Join(base, "tether", "agent_config.yaml"), nil
}

// Config is an Agent's local configuration, loaded once at startup from
// a YAML file the operator edits by hand — the same pattern as
// node_allowlist.yaml (design doc §4.2): human-authored, git-diffable
// config, not something written by the program itself.
type Config struct {
	RPCServerPath string `yaml:"rpc_server_path"`
	RPCListenHost string `yaml:"rpc_listen_host"`
}

// Load reads and validates an Agent config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading agent config %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing agent config YAML in %q: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid agent config %q: %w", path, err)
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if c.RPCServerPath == "" {
		return fmt.Errorf("rpc_server_path is required")
	}
	if info, err := os.Stat(c.RPCServerPath); err != nil {
		return fmt.Errorf("rpc_server_path %q: %w", c.RPCServerPath, err)
	} else if info.IsDir() {
		return fmt.Errorf("rpc_server_path %q is a directory, not an executable file", c.RPCServerPath)
	}

	if c.RPCListenHost == "" {
		return fmt.Errorf("rpc_listen_host is required")
	}

	return nil
}
