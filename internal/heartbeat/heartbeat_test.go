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
