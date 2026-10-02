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

package main

import (
	"io/fs"
	"strings"
	"testing"
)

func TestOrchestratorUsesInAppConfirmationDialogs(t *testing.T) {
	html, err := fs.ReadFile(desktopAssets, "frontend/dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	javascript, err := fs.ReadFile(desktopAssets, "frontend/dist/app.js")
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{`id="model-plan-dialog"`, `id="safety-dialog"`} {
		if !strings.Contains(string(html), id) {
			t.Errorf("orchestrator UI is missing %s", id)
		}
	}
	if strings.Contains(string(javascript), "window.confirm") {
		t.Fatal("orchestrator UI still uses a browser confirmation box")
	}
}
