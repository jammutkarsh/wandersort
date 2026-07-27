// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package classifier

import "testing"

func TestParseMetadataIsScreenshot(t *testing.T) {
	tests := []struct {
		name string
		json string
		want bool
	}{
		{"macOS description", `{"Description":"Screenshot"}`, true},
		{"iOS user comment", `{"UserComment":"Screenshot"}`, true},
		{"samsung screenshot", `{"SamsungCaptureInfo":"Screenshot"}`, true},
		{"samsung screen recording", `{"SamsungCaptureInfo":"Screen recording"}`, true},
		{"camera photo", `{"Make":"Canon","Model":"EOS 700D"}`, false},
		{"unrelated description", `{"Description":"A day at the beach"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta, err := ParseMetadata("jpg", []byte(tt.json))
			if err != nil {
				t.Fatal(err)
			}
			if meta.IsScreenshot != tt.want {
				t.Errorf("IsScreenshot = %v, want %v", meta.IsScreenshot, tt.want)
			}
		})
	}
}
