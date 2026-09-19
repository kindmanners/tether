package trust

import (
	"testing"
	"tether/internal/certs"
)

func TestDeleteRemovesPinnedPeer(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("APPDATA", configHome)
	identity, err := certs.LoadOrCreate("agent")
	if err != nil {
		t.Fatal(err)
	}
	if err := Pin("orchestrator", identity.CertDER); err != nil {
		t.Fatal(err)
	}
	if err := Delete("orchestrator"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Get("orchestrator"); err != nil || found {
		t.Fatalf("Get after Delete = found %t, err %v", found, err)
	}
}
