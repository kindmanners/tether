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
