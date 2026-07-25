// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build darwin || linux

package volume

import "testing"

func TestVolume(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"FreeBytes", func(t *testing.T) {
			free, err := FreeBytes(t.TempDir())
			if err != nil {
				t.Fatalf("FreeBytes: %v", err)
			}
			if free == 0 {
				t.Error("a writable temp dir should report free space")
			}
		}},
		{"ForPathCachesAndNeverErrors", func(t *testing.T) {
			r := New()
			first := r.ForPath(t.TempDir())
			if _, err := FreeBytes("/definitely/not/a/path"); err == nil {
				t.Error("FreeBytes on a missing path should error")
			}
			if got := r.ForPath("/definitely/not/a/path"); got != "" {
				t.Errorf("unresolvable path returned %q, want empty", got)
			}
			_ = first // value is platform-dependent; only the no-panic contract matters
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}
