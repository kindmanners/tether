// Package heartbeat persists and checks the paired Orchestrator's Tailnet
// identity. It uses Tailscale's control-plane-aware ping rather than opening
// an additional Tether listener on the Orchestrator.
package heartbeat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const Interval = 10 * time.Minute

type peer struct {
	Hostname string `json:"hostname"`
}

func path() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user config directory: %w", err)
	}
	return filepath.Join(base, "tether", "orchestrator-peer.json"), nil
}

// Save records the Tailnet hostname supplied during a successful, code-gated
// pairing exchange. Hostnames are deliberately kept simple because they are
// passed to the Tailscale CLI, never to a shell.
func Save(hostname string) error {
	if hostname == "" || strings.ContainsAny(hostname, "/\\ \t\r\n") {
		return fmt.Errorf("invalid Orchestrator Tailnet hostname %q", hostname)
	}
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("creating heartbeat state directory: %w", err)
	}
	data, err := json.Marshal(peer{Hostname: hostname})
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0600)
}

func Load() (string, error) {
	p, err := path()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	var saved peer
	if err := json.Unmarshal(data, &saved); err != nil {
		return "", fmt.Errorf("parsing heartbeat state: %w", err)
	}
	if saved.Hostname == "" || strings.ContainsAny(saved.Hostname, "/\\ \t\r\n") {
		return "", fmt.Errorf("heartbeat state has no valid Orchestrator hostname")
	}
	return saved.Hostname, nil
}

func Delete() error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing heartbeat state: %w", err)
	}
	return nil
}

// Ping performs exactly one bounded Tailnet-layer ping. It avoids shell
// interpretation and is safe to run in a background heartbeat loop.
func Ping(ctx context.Context, hostname string) error {
	output, err := exec.CommandContext(ctx, "tailscale", "ping", "--c=1", "--timeout=5s", hostname).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tailscale ping %s: %w: %s", hostname, err, strings.TrimSpace(string(output)))
	}
	return nil
}
