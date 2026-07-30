// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package location

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	// minutesPerDegree is the number of minutes in one degree
	minutesPerDegree = 60
	// secondsPerDegree is the number of seconds in one degree
	secondsPerDegree = 3600
)

// ParseGPS converts EXIF DMS strings (e.g. `31 deg 34' 5.84" N`) into signed
// decimal-degree floats. Never panics; returns an error on malformed input.
func ParseGPS(latStr, lonStr string) (float64, float64, error) {
	lat, err := parseDMS(latStr)
	if err != nil {
		return 0, 0, fmt.Errorf("parseGPS latitude: %w", err)
	}
	lon, err := parseDMS(lonStr)
	if err != nil {
		return 0, 0, fmt.Errorf("parseGPS longitude: %w", err)
	}
	return lat, lon, nil
}

// parseDMS parses one EXIF DMS string (`<deg> deg <min>' <sec>" <N/S/E/W>`)
// into a signed decimal-degree float64. Never panics on malformed input.
func parseDMS(dms string) (float64, error) {
	dms = strings.TrimSpace(dms)
	if dms == "" {
		return 0, fmt.Errorf("empty DMS")
	}

	upper := strings.ToUpper(dms)

	var sign float64
	switch {
	case strings.HasSuffix(upper, "N"), strings.HasSuffix(upper, "E"):
		sign = 1
	case strings.HasSuffix(upper, "S"), strings.HasSuffix(upper, "W"):
		sign = -1
	default:
		return 0, fmt.Errorf("missing hemisphere indicator (N/S/E/W)")
	}

	// strip the hemisphere letter and the deg/'/" markers, leaving 3 numbers
	body := strings.TrimSpace(dms[:len(dms)-1])
	body = strings.ReplaceAll(body, "deg", " ")
	body = strings.ReplaceAll(body, "'", " ")
	body = strings.ReplaceAll(body, `"`, " ")

	parts := strings.Fields(body)
	if len(parts) != 3 {
		return 0, fmt.Errorf("expected 3 numeric fields, got %d", len(parts))
	}

	var vals [3]float64
	for i := 0; i < 3; i++ {
		var err error
		if vals[i], err = strconv.ParseFloat(parts[i], 64); err != nil {
			return 0, fmt.Errorf("failed to parse DMS component: %w", err)
		}
		if vals[i] < 0 {
			return 0, fmt.Errorf("negative value in DMS: %v", parts[i])
		}
	}
	degrees, minutes, seconds := vals[0], vals[1], vals[2]

	// Convert DMS to decimal degrees: 1° = 60′ = 3600″
	decimalDegrees := degrees + minutes/minutesPerDegree + seconds/secondsPerDegree
	return sign * decimalDegrees, nil
}
