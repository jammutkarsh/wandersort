package locationdb

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseGPS converts a pair of EXIF DMS strings into signed decimal-degree floats.
//
// latStr must be a latitude string (N/S hemisphere), e.g. `31 deg 34' 5.84" N`.
// lonStr must be a longitude string (E/W hemisphere), e.g. `77 deg 22' 14.32" E`.
//
// N and E produce positive values; S and W produce negative values.
// Returns an error on malformed input; never panics.
func ParseGPS(latStr, lonStr string) (float64, float64, error) {
	lat, err := parseDMS(latStr)
	if err != nil {
		return 0, 0, fmt.Errorf("locationdb: parseGPS latitude: %w", err)
	}
	lon, err := parseDMS(lonStr)
	if err != nil {
		return 0, 0, fmt.Errorf("locationdb: parseGPS longitude: %w", err)
	}
	return lat, lon, nil
}

// parseDMS parses a single EXIF DMS string into a signed decimal-degree
// float64.
//
// Supported format: `<degrees> deg <minutes>' <seconds>" <hemisphere>`
// where hemisphere is one of N, S, E, W (case-insensitive).
//
// N/E → positive result; S/W → negative result.
// Returns a descriptive error on malformed input; never panics.
func parseDMS(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty string")
	}

	upper := strings.ToUpper(s)

	// Determine sign from the trailing hemisphere letter.
	// Directions are geographic constants: N/E → positive, S/W → negative.
	var sign float64
	switch {
	case strings.HasSuffix(upper, "N"), strings.HasSuffix(upper, "E"):
		sign = 1
	case strings.HasSuffix(upper, "S"), strings.HasSuffix(upper, "W"):
		sign = -1
	default:
		return 0, fmt.Errorf("missing hemisphere indicator (N/S/E/W)")
	}

	// Strip the trailing hemisphere character (last byte) from the body.
	body := s[:len(s)-1]
	body = strings.TrimSpace(body)

	// Normalise DMS separators to spaces so Fields can split uniformly.
	body = strings.ReplaceAll(body, "deg", " ")
	body = strings.ReplaceAll(body, "'", " ")
	body = strings.ReplaceAll(body, `"`, " ")

	parts := strings.Fields(body)
	if len(parts) != 3 {
		return 0, fmt.Errorf("expected 3 numeric fields after stripping tokens, got %d in %q", len(parts), s)
	}

	degrees, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, fmt.Errorf("degrees %q: %w", parts[0], err)
	}

	minutes, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, fmt.Errorf("minutes %q: %w", parts[1], err)
	}

	seconds, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0, fmt.Errorf("seconds %q: %w", parts[2], err)
	}

	if degrees < 0 || minutes < 0 || seconds < 0 {
		return 0, fmt.Errorf("negative component in DMS value %q", s)
	}

	// Convert DMS to decimal degrees: 1° = 60′ = 3600″
	decimalDegrees := degrees + minutes/60 + seconds/3600
	return sign * decimalDegrees, nil
}
