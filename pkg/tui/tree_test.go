// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package tui

import (
	"reflect"
	"testing"
)

func TestGuides(t *testing.T) {
	tests := []struct {
		name   string
		depths []int
		want   []string
	}{
		{
			name:   "flat list of depth-0 rows",
			depths: []int{0, 0, 0},
			want:   []string{"", "", ""},
		},
		{
			name:   "one parent with two children",
			depths: []int{0, 1, 1},
			want:   []string{"", "├─ ", "└─ "},
		},
		{
			name:   "three levels with a middle last-child",
			depths: []int{0, 1, 2, 1},
			want:   []string{"", "├─ ", "│  └─ ", "└─ "},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Guides(tt.depths)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Guides(%v) = %v, want %v", tt.depths, got, tt.want)
			}
		})
	}
}
