// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// Package gguf provides the model-library rules shared by the desktop and
// inference gateway.
package gguf

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var splitName = regexp.MustCompile(`(?i)^(.*)-(\d{5})-of-(\d{5})\.gguf$`)

// Model is one loadable GGUF model. Path is the ordinary file or first shard;
// Size is the sum of all shards.
type Model struct {
	ID       string
	Filename string
	Path     string
	Size     int64
}

// Scan finds loadable models, omitting multimodal projector files and grouping
// split GGUF shards into a single model rooted at shard one.
func Scan(dir string) ([]Model, error) {
	models := make([]Model, 0)
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".gguf") || strings.HasPrefix(strings.ToLower(entry.Name()), "mmproj") {
			return nil
		}
		name := entry.Name()
		matches := splitName.FindStringSubmatch(name)
		if matches != nil && matches[2] != "00001" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		model := Model{ID: strings.TrimSuffix(name, filepath.Ext(name)), Filename: name, Path: path, Size: info.Size()}
		if matches != nil {
			total, err := strconv.Atoi(matches[3])
			if err != nil || total < 2 {
				return fmt.Errorf("invalid split GGUF name %q", name)
			}
			model.ID = matches[1]
			model.Size = 0
			for shard := 1; shard <= total; shard++ {
				shardName := fmt.Sprintf("%s-%05d-of-%05d.gguf", matches[1], shard, total)
				shardPath, resolveErr := caseInsensitiveFile(filepath.Dir(path), shardName)
				if resolveErr != nil {
					return fmt.Errorf("split model %q is incomplete (missing %s): %w", model.ID, shardName, resolveErr)
				}
				shardInfo, statErr := os.Stat(shardPath)
				if statErr != nil {
					return fmt.Errorf("split model %q is incomplete (missing %s): %w", model.ID, shardName, statErr)
				}
				model.Size += shardInfo.Size()
			}
		}
		models = append(models, model)
		return nil
	})
	if os.IsNotExist(err) {
		return models, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning GGUF models in %q: %w", dir, err)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	for i := 1; i < len(models); i++ {
		if models[i-1].ID == models[i].ID {
			return nil, fmt.Errorf("duplicate model id %q; GGUF file names must be unique", models[i].ID)
		}
	}
	return models, nil
}

func caseInsensitiveFile(dir, name string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(entry.Name(), name) {
			return filepath.Join(dir, entry.Name()), nil
		}
	}
	return "", os.ErrNotExist
}

// Size returns the full size of the model containing path.
func Size(path string) (int64, error) {
	models, err := Scan(filepath.Dir(path))
	if err != nil {
		return 0, err
	}
	clean := filepath.Clean(path)
	for _, model := range models {
		if filepath.Clean(model.Path) == clean {
			return model.Size, nil
		}
	}
	return 0, fmt.Errorf("GGUF model %q is not loadable", path)
}
