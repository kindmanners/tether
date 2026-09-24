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
