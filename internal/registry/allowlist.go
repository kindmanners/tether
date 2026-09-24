// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package registry

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// AllowlistEntry is a single hand-maintained node declaration from
// node_allowlist.yaml. This is intentionally a separate type from Node
// rather than reusing Node directly: the allowlist only knows what a human
// configured (hostname, role, ports) — it has no idea about live state like
// TailscaleIP, Status, or LastSeen. Collapsing these two into one type would
// mean either the YAML file has to carry fields it can't actually populate,
// or Node would need to tolerate being constructed half-empty. Keeping them
// separate means each type only ever holds data it's actually responsible
// for; registry.go's merge step is where an AllowlistEntry and a live
// Tailscale peer combine into a real Node.
type AllowlistEntry struct {
	Hostname  string `yaml:"hostname"`
	Role      string `yaml:"role"`
	AgentPort int    `yaml:"agent_port"`
	RPCPort   int    `yaml:"rpc_port"`
}

// Allowlist is the parsed contents of node_allowlist.yaml.
type Allowlist struct {
	Nodes []AllowlistEntry `yaml:"nodes"`
}

// LoadAllowlist reads and parses the allowlist YAML file at path.
//
// This intentionally does very little: read bytes, unmarshal YAML, do basic
// sanity checks, return. It does not touch Tailscale, does not build Node
// values, and does not decide what's "trusted" beyond parsing the file
// correctly — that keeps this file testable with nothing but a temp YAML
// file on disk, no network or external commands involved.
func LoadAllowlist(path string) (*Allowlist, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading allowlist file %q: %w", path, err)
	}

	var allowlist Allowlist
	if err := yaml.Unmarshal(data, &allowlist); err != nil {
		return nil, fmt.Errorf("parsing allowlist YAML in %q: %w", path, err)
	}

	if err := validateAllowlist(&allowlist); err != nil {
		return nil, fmt.Errorf("invalid allowlist %q: %w", path, err)
	}

	return &allowlist, nil
}

// validateAllowlist catches config mistakes early — at load time, with a
// clear error pointing at the bad entry — rather than letting a malformed
// entry silently produce a broken Node later during the Tailscale merge,
// where the error would be much harder to trace back to "you forgot a port
// in the YAML."
func validateAllowlist(a *Allowlist) error {
	seen := make(map[string]bool, len(a.Nodes))

	for i, entry := range a.Nodes {
		if entry.Hostname == "" {
			return fmt.Errorf("entry %d: hostname is required", i)
		}
		if seen[entry.Hostname] {
			return fmt.Errorf("entry %d: duplicate hostname %q", i, entry.Hostname)
		}
		seen[entry.Hostname] = true

		if entry.Role == "" {
			return fmt.Errorf("entry %d (%s): role is required", i, entry.Hostname)
		}
		if entry.AgentPort <= 0 || entry.AgentPort > 65535 {
			return fmt.Errorf("entry %d (%s): agent_port %d is not a valid port", i, entry.Hostname, entry.AgentPort)
		}
		if entry.RPCPort <= 0 || entry.RPCPort > 65535 {
			return fmt.Errorf("entry %d (%s): rpc_port %d is not a valid port", i, entry.Hostname, entry.RPCPort)
		}
		if entry.AgentPort == entry.RPCPort {
			return fmt.Errorf("entry %d (%s): agent_port and rpc_port must differ (both %d)", i, entry.Hostname, entry.AgentPort)
		}
	}

	return nil
}