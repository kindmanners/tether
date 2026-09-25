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

package certs

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

func TestLoadOrCreatePersistsIdentityAndRejectsInvalidNames(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("APPDATA", configHome)
	first, err := LoadOrCreate("test-identity")
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreate("test-identity")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.CertDER, second.CertDER) {
		t.Fatal("LoadOrCreate generated a different identity on reload")
	}
	if first.Certificate.Subject.CommonName != "test-identity" || first.TLSCertificate().Leaf.Subject.CommonName != "test-identity" {
		t.Fatalf("unexpected certificate identity: %#v", first.Certificate.Subject)
	}
	for _, name := range []string{"", ".", "..", "../evil", `sub\\dir`} {
		if _, err := LoadOrCreate(name); err == nil {
			t.Errorf("LoadOrCreate(%q) succeeded", name)
		}
	}
}

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
