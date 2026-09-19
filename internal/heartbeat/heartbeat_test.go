package heartbeat

import "testing"

func TestSaveLoadDelete(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("APPDATA", home)
	if err := Save("ataraxia"); err != nil {
		t.Fatal(err)
	}
	hostname, err := Load()
	if err != nil || hostname != "ataraxia" {
		t.Fatalf("Load() = %q, %v", hostname, err)
	}
	if err := Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("Load() succeeded after Delete")
	}
}

func TestSaveRejectsUnsafeHostname(t *testing.T) {
	if err := Save("bad host"); err == nil {
		t.Fatal("Save() accepted an unsafe hostname")
	}
}
