package certs

import (
	"errors"
	"os"
	"testing"
)

func TestDeleteRemovesIdentityPair(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("APPDATA", configHome)
	if _, err := LoadOrCreate("agent"); err != nil {
		t.Fatal(err)
	}
	if err := Delete("agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("agent"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load after Delete error = %v, want not-exist", err)
	}
}
