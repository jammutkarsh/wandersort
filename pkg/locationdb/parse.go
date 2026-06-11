package locationdb

import (
	"fmt"
	"strconv"
	"strings"
)

// Latitude (N/S) measures position north or south of the Equator
// Longitude (E/W) measures position east or west of the Equator
// Hemisphere suffixes N/E produce positive values and S/W produce negative values
func ParseGPS(latStr, lonStr string) (float64, float64, error) {
	lat, err := parseDMS(latStr, "N", "S")
	if err != nil {
		return 0, 0, fmt.Errorf("locationdb: parseGPS latitude: %w", err)
	}

	lon, err := parseDMS(lonStr, "E", "W")
	if err != nil {
		return 0, 0, fmt.Errorf("locationdb: parseGPS longitude: %w", err)
	}

	return lat, lon, nil
}

func parseDMS(s, posHemi, negHemi string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty string")
	}

	upper := strings.ToUpper(s)
	var sign float64
	switch {
	case strings.HasSuffix(upper, posHemi):
		sign = 1
	case strings.HasSuffix(upper, negHemi):
		sign = -1
	default:
		return 0, fmt.Errorf("missing Hemisphere indicator: (%s/%s)", posHemi, negHemi)
	}

	body := s[:len(s)-1]
	body = strings.TrimSpace(body)
	body = strings.ReplaceAll(body, "deg", " ")
	body = strings.ReplaceAll(body, "'", " ")
	body = strings.ReplaceAll(body, `"`, " ")

	parts := strings.Fields(body)
	if len(parts) != 3 {
		return 0, fmt.Errorf("expected 3 numeric fields, got %d in %q", len(parts), s)
	}

	degrees, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, fmt.Errorf("degrees %q: %w", parts[0], err)
	}

	mintues, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, fmt.Errorf("minutes %q: %w", parts[1], err)
	}

	seconds, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0, fmt.Errorf("seconds %q: %w", parts[2], err)
	}

	if degrees < 0 || mintues < 0 || seconds < 0 {
		return 0, fmt.Errorf("negative component in DMS value %q", s)
	}

	// 1 degree = 60 minutes
	decimalDegrees := degrees + mintues/60 + seconds/3600
	return sign * decimalDegrees, nil
}
