// Package agentconfig holds an Agent's own local, operator-set
// configuration: where its llama.cpp rpc-server binary lives, and which
// model files it's willing to serve. This is deliberately NOT something
// the Orchestrator can set remotely — per the design decision behind
// internal/agent's command server, the Orchestrator may only choose FROM
// this pre-approved list, never supply an arbitrary path of its own. A
// compromised or buggy Orchestrator should never be able to make an
// Agent execute an arbitrary binary or open an arbitrary file — the
// worst it can do is pick among choices a human operator already
// approved locally, on that specific machine.
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
// state — Ataraxia's rpc_server_path and model list will legitimately
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

// ModelEntry is one operator-approved model this Agent is willing to
// serve. Name is what the Orchestrator selects by (never the raw Path) —
// keeping the network-facing identifier separate from the filesystem
// path means the Orchestrator's request never contains anything that
// looks like a path at all, which removes an entire category of
// path-traversal-flavored bugs at the protocol level rather than relying
// solely on validating them away after the fact (the same category of
// bug found and fixed twice already in this project, in internal/certs
// and internal/trust — designing it out here is cheaper than patching it
// in later).
type ModelEntry struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

// Config is an Agent's local configuration, loaded once at startup from
// a YAML file the operator edits by hand — the same pattern as
// node_allowlist.yaml (design doc §4.2): human-authored, git-diffable
// config, not something written by the program itself.
type Config struct {
	RPCServerPath string       `yaml:"rpc_server_path"`
	Models        []ModelEntry `yaml:"models"`
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

	if len(c.Models) == 0 {
		return fmt.Errorf("at least one entry under models is required")
	}

	seenNames := make(map[string]bool, len(c.Models))
	for i, m := range c.Models {
		if m.Name == "" {
			return fmt.Errorf("models[%d]: name is required", i)
		}
		if seenNames[m.Name] {
			return fmt.Errorf("models[%d]: duplicate model name %q", i, m.Name)
		}
		seenNames[m.Name] = true

		if m.Path == "" {
			return fmt.Errorf("models[%d] (%s): path is required", i, m.Name)
		}
		absPath, err := filepath.Abs(m.Path)
		if err != nil {
			return fmt.Errorf("models[%d] (%s): resolving path %q: %w", i, m.Name, m.Path, err)
		}
		if info, err := os.Stat(absPath); err != nil {
			return fmt.Errorf("models[%d] (%s): path %q: %w", i, m.Name, m.Path, err)
		} else if info.IsDir() {
			return fmt.Errorf("models[%d] (%s): path %q is a directory, not a model file", i, m.Name, m.Path)
		}
	}

	return nil
}

// Resolve looks up an approved model by name, returning its actual
// filesystem path. This is the ONLY way the rest of the Agent should ever
// obtain a model file path to hand to rpc-server — never accept a path
// directly from a network request. Returns an error for any name not in
// the pre-approved list, including a name that LOOKS like a path (e.g.
// "../../etc/passwd") — such a string simply won't match any configured
// Name, so it's rejected by the same lookup logic as any other unknown
// name, with no special-casing needed.
func (c *Config) Resolve(modelName string) (string, error) {
	for _, m := range c.Models {
		if m.Name == modelName {
			return m.Path, nil
		}
	}
	return "", fmt.Errorf("model %q is not in this agent's approved model list", modelName)
}