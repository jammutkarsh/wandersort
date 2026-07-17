// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package workflow

import (
	"testing"

	"github.com/google/uuid"
)

func TestClaimRoots(t *testing.T) {
	wf := &Workflow{activeRoots: map[uuid.UUID][]string{}}

	first := uuid.New()
	if err := wf.claimRoots(first, []string{"/photos", "/videos"}); err != nil {
		t.Fatalf("first claim: %v", err)
	}

	tests := []struct {
		name    string
		paths   []string
		wantErr bool
	}{
		{"same root", []string{"/photos"}, true},
		{"nested under active", []string{"/photos/2024"}, true},
		{"parent of active", []string{"/"}, true},
		{"disjoint", []string{"/music"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := uuid.New()
			err := wf.claimRoots(id, tt.paths)
			if (err != nil) != tt.wantErr {
				t.Errorf("claimRoots(%v) error = %v, wantErr %v", tt.paths, err, tt.wantErr)
			}
			if err == nil {
				wf.releaseRoots(id)
			}
		})
	}

	// released roots stop blocking
	wf.releaseRoots(first)
	if err := wf.claimRoots(uuid.New(), []string{"/photos"}); err != nil {
		t.Errorf("claim after release: %v", err)
	}
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		n    uint64
		want string
	}{
		{512, "512 B"},
		{2048, "2.0 KiB"},
		{5 << 30, "5.0 GiB"},
		{1610612736, "1.5 GiB"},
	}
	for _, tt := range tests {
		if got := humanBytes(tt.n); got != tt.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}
