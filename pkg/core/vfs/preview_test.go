// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package vfs

import (
	"testing"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
)

func TestPreviewPathsCollapseDropsUniformDeviceOnly(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Rules = []string{RuleDevice}
	cfg.CollapseLevels = true

	takenAt := time.Date(2024, time.August, 2, 12, 0, 0, 0, time.UTC)
	base := func(device, file string) Sample {
		return Sample{TakenAt: takenAt, Device: device, MediaType: classifier.MediaTypeImage, FileName: file}
	}

	// two samples, different devices -> device level kept
	diff := PreviewPaths(cfg, []Sample{base("iPhone 13", "a.jpg"), base("Canon EOS 700D", "b.jpg")})
	if want := "2024/08_August/iPhone-13/a.jpg"; diff[0] != want {
		t.Errorf("distinct device a: got %q, want %q", diff[0], want)
	}
	// Canon-Eos-700d, not Canon-EOS-700D: applyNameCase title-cases every
	// word that isn't whitelisted, and the preview now runs it because the
	// real pipeline does. Showing the unmangled name here was the drift this
	// preview is meant not to have — it promised a folder the pipeline would
	// never create. (That the pipeline mangles it at all is its own bug.)
	if want := "2024/08_August/Canon-Eos-700d/b.jpg"; diff[1] != want {
		t.Errorf("distinct device b: got %q, want %q", diff[1], want)
	}

	// two samples, same device -> device level collapses away
	same := PreviewPaths(cfg, []Sample{base("iPhone 13", "a.jpg"), base("iPhone 13", "b.jpg")})
	if want := "2024/08_August/a.jpg"; same[0] != want {
		t.Errorf("uniform device: got %q, want %q (device level should collapse)", same[0], want)
	}
}
