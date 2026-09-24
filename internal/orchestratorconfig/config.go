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

// Package orchestratorconfig stores machine-local Orchestrator preferences.
// It intentionally lives outside the shared node allowlist: whether the
// control host contributes its GPU is a decision of that one machine, not a
// property of every Tether installation.
package orchestratorconfig

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is deliberately small. More local desktop preferences can join this
// file without changing cluster topology or Agent configuration.
type Config struct {
	ContributeLocalGPU  bool   `yaml:"contribute_local_gpu"`
	LlamaServerPath     string `yaml:"llama_server_path,omitempty"`
	LlamaServerLocalGPU bool   `yaml:"llama_server_local_gpu,omitempty"`
}

// Default returns the established behavior for existing installations: use a
// local CUDA backend when one is available. A user can explicitly opt out in
// the Orchestrator UI without editing a file or building CUDA components.
func Default() Config {
	return Config{ContributeLocalGPU: true}
}

func DefaultPath() (string, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user config directory: %w", err)
	}
	return filepath.Join(directory, "tether", "orchestrator_config.yaml"), nil
}

// Load treats a missing file as the default so upgrading Tether never changes
// an existing Orchestrator's placement behavior unexpectedly.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("reading Orchestrator config %q: %w", path, err)
	}
	config := Default()
	if err := yaml.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("parsing Orchestrator config %q: %w", path, err)
	}
	return config, nil
}

func Save(path string, config Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating Orchestrator config directory: %w", err)
	}
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("encoding Orchestrator config: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tether-orchestrator-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temporary Orchestrator config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("saving Orchestrator config: %w", err)
	}
	return nil
}
