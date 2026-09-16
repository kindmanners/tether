package orchestratorconfig

import (
	"os"
	"path/filepath"
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
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions = %o, want 600", info.Mode().Perm())
	}
}
