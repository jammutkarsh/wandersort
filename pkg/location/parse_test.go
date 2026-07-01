// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package location

import (
	"math"
	"testing"
)

func TestParseGPS(t *testing.T) {
	tests := []struct {
		name    string
		latStr  string
		lonStr  string
		wantLat float64
		wantLon float64
		wantErr bool
	}{
		{
			name:    "valid north-east coordinates",
			latStr:  `31 deg 34' 5.84" N`,
			lonStr:  `77 deg 22' 14.32" E`,
			wantLat: 31 + 34.0/60 + 5.84/3600,
			wantLon: 77 + 22.0/60 + 14.32/3600,
			wantErr: false,
		},
		{
			name:    "valid south-west coordinates",
			latStr:  `33 deg 52' 0.00" S`,
			lonStr:  `151 deg 12' 36.00" W`,
			wantLat: -(33 + 52.0/60),
			wantLon: -(151 + 12.0/60 + 36.0/3600),
			wantErr: false,
		},
		{
			name:    "empty string",
			latStr:  "",
			lonStr:  "",
			wantErr: true,
		},
		{
			name:    "garbage input",
			latStr:  "not a coordinate",
			lonStr:  "also garbage",
			wantErr: true,
		},
		{
			name:    "missing hemisphere indicator",
			latStr:  `31 deg 34' 5.84"`,
			lonStr:  `77 deg 22' 14.32"`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lat, lon, err := ParseGPS(tc.latStr, tc.lonStr)

			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}

			if !almostEqual(lat, tc.wantLat, 1e-6) {
				t.Errorf("lat = %v, want %v", lat, tc.wantLat)
			}
			if !almostEqual(lon, tc.wantLon, 1e-6) {
				t.Errorf("lon = %v, want %v", lon, tc.wantLon)
			}
		})
	}
}

// almostEqual reports whether a and b differ by less than epsilon(small tolerance value).
func almostEqual(a, b, eps float64) bool {
	return math.Abs(a-b) < eps
}
