package main

import (
	"io/fs"
	"strings"
	"testing"
)

func TestAgentFrontendUsesProtectedRepairDialog(t *testing.T) {
	html, err := fs.ReadFile(desktopAssets, "frontend/dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	javascript, err := fs.ReadFile(desktopAssets, "frontend/dist/app.js")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(html), `id="repair-dialog"`) {
		t.Fatal("agent frontend must include the protected re-pair dialog")
	}
	if strings.Contains(string(javascript), "window.confirm") {
		t.Fatal("agent frontend must not use the browser confirm prompt")
	}
}
