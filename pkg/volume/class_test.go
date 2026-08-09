// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build darwin || linux

package volume

import "testing"

func TestClassForPath(t *testing.T) {
	// The class is advisory: whatever this machine is, resolving it must not
	// panic and must not report an error to the caller
	if got := ClassForPath(t.TempDir()); got.String() == "" {
		t.Fatalf("ClassForPath returned a class with no name: %d", got)
	}

	if got := ClassForPath("/definitely/not/a/real/path/xyzzy"); got != ClassUnknown {
		t.Errorf("ClassForPath(bogus) = %v, want %v", got, ClassUnknown)
	}
}

func TestClassString(t *testing.T) {
	tests := []struct {
		class Class
		want  string
	}{
		{ClassUnknown, "unknown"},
		{ClassRotational, "rotational"},
		{ClassSolidState, "solid-state"},
		{ClassRemovable, "removable"},
		{ClassNetwork, "network"},
		{Class(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.class.String(); got != tt.want {
			t.Errorf("Class(%d).String() = %q, want %q", tt.class, got, tt.want)
		}
	}
}
