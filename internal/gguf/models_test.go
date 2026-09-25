// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package gguf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanGroupsSplitModelsAndOmitsProjectors(t *testing.T) {
	dir := t.TempDir()
	writeModelFile(t, dir, "ordinary.GGUF", 3)
	writeModelFile(t, dir, "mmproj-vision.gguf", 7)
	writeModelFile(t, dir, "split-00001-of-00002.gguf", 5)
	writeModelFile(t, dir, "split-00002-of-00002.GGUF", 11)

	models, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("Scan() returned %d models, want 2: %#v", len(models), models)
	}
	if models[0].ID != "ordinary" || models[0].Size != 3 {
		t.Fatalf("ordinary model = %#v", models[0])
	}
	if models[1].ID != "split" || models[1].Size != 16 {
		t.Fatalf("split model = %#v", models[1])
	}
	size, err := Size(models[1].Path)
	if err != nil || size != 16 {
		t.Fatalf("Size(split) = %d, %v; want 16, nil", size, err)
	}
}

func TestScanReportsIncompleteAndDuplicateModels(t *testing.T) {
	t.Run("missing shard", func(t *testing.T) {
		dir := t.TempDir()
		writeModelFile(t, dir, "split-00001-of-00002.gguf", 1)
		_, err := Scan(dir)
		if err == nil || !strings.Contains(err.Error(), "incomplete") {
			t.Fatalf("Scan() error = %v, want incomplete split error", err)
		}
	})
	t.Run("duplicate id", func(t *testing.T) {
		dir := t.TempDir()
		writeModelFile(t, dir, "same.gguf", 1)
		writeModelFile(t, dir, "same-00001-of-00002.gguf", 1)
		writeModelFile(t, dir, "same-00002-of-00002.gguf", 1)
		_, err := Scan(dir)
		if err == nil || !strings.Contains(err.Error(), "duplicate model id") {
			t.Fatalf("Scan() error = %v, want duplicate id error", err)
		}
	})
}

func TestScanMissingDirectoryAndSizeRejectsUnknownFile(t *testing.T) {
	models, err := Scan(filepath.Join(t.TempDir(), "missing"))
	if err != nil || len(models) != 0 {
		t.Fatalf("Scan(missing) = %#v, %v", models, err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("not a model"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Size(path); err == nil {
		t.Fatal("Size() accepted a non-GGUF file")
	}
}

func writeModelFile(t *testing.T, dir, name string, size int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}
