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

package orchestratorconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadMissingUsesEstablishedDefault(t *testing.T) {
	config, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !config.ContributeLocalGPU {
		t.Fatal("missing config must preserve local GPU contribution by default")
	}
}

func TestSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "orchestrator_config.yaml")
	want := Config{ContributeLocalGPU: false}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
	want = Config{ContributeLocalGPU: true, LlamaServerPath: filepath.Join("C:", "tether", "llama-server.exe"), LlamaServerLocalGPU: true}
	if err := Save(path, want); err != nil {
		t.Fatalf("replacing existing config: %v", err)
	}
	if got, err = Load(path); err != nil || got != want {
		t.Fatalf("Load() after replacement = %#v, %v; want %#v", got, err, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows reports synthesized permission bits; the actual access policy is
	// carried by the file's ACL. Unix platforms must retain the private mode.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions = %o, want 600", info.Mode().Perm())
	}
}
