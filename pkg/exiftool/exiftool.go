// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package exiftool

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
)

// Extractor runs exiftool as a subprocess and parses its JSON output
type Extractor struct {
	exiftoolPath string
}

func New(exiftoolPath string) *Extractor {
	return &Extractor{exiftoolPath: exiftoolPath}
}

// Extract runs exiftool on a single file and returns the parsed metadata or error
func (e *Extractor) Extract(ctx context.Context, path string) (classifier.CommonMetadata, error) {
	// -json: output as JSON array; -n: numeric values (no unit strings)
	raw, err := exec.CommandContext(ctx, e.exiftoolPath, "-json", "-n", path).Output()
	if err != nil {
		return classifier.CommonMetadata{}, fmt.Errorf("exiftool %s: %w", path, err)
	}

	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return classifier.CommonMetadata{}, fmt.Errorf("exiftool output is not a JSON array: %w", err)
	}
	if len(arr) == 0 {
		return classifier.CommonMetadata{}, fmt.Errorf("exiftool returned an empty array")
	}

	return classifier.ParseMetadata(filepath.Ext(path), arr[0])
}
