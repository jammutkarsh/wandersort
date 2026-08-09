// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build linux

package volume

import "testing"

func TestBlockBase(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"sda1", "sda"},
		{"sdb12", "sdb"},
		{"nvme0n1p2", "nvme0n1"},
		{"mmcblk0p1", "mmcblk0"},
		// already a whole disk, or nothing to strip: stop guessing. Callers
		// only reach blockBase once the name failed as a whole disk, so ""
		// is "give up", not "this is the disk"
		{"sda", ""},
		{"dm-0", ""},
		{"loop0", "loop"}, // never reached: /sys/block/loop0 has its own queue/
	}

	for _, tt := range tests {
		if got := blockBase(tt.name); got != tt.want {
			t.Errorf("blockBase(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}
