package location

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	// minutesPerDegree is the number of minutes in one degree.
	minutesPerDegree = 60
	// secondsPerDegree is the number of seconds in one degree.
	secondsPerDegree = 3600
)

// ParseGPS converts a pair of EXIF DMS strings into signed decimal-degree floats
//
// latStr must be a latitude string (N/S hemisphere), e.g. `31 deg 34' 5.84" N`
// lonStr must be a longitude string (E/W hemisphere), e.g. `77 deg 22' 14.32" E`
//
// N and E produce positive values; S and W produce negative values
// Returns an error on malformed input; never panics
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

// parseDMS parses a single EXIF DMS string into a signed decimal-degree
// float64
//
// Supported format: `<degrees> deg <minutes>' <seconds>" <hemisphere>`
// where hemisphere is one of N, S, E, W (case-insensitive)
//
// N/E → positive result; S/W → negative result
// Returns a descriptive error on malformed input; never panics
func parseDMS(dms string) (float64, error) {
	dms = strings.TrimSpace(dms)
	if dms == "" {
		return 0, fmt.Errorf("empty DMS")
	}

	upper := strings.ToUpper(dms)

	// Determine sign from the trailing hemisphere letter
	// Directions are geographic constants: N/E → positive, S/W → negative
	var sign float64
	switch {
	case strings.HasSuffix(upper, "N"), strings.HasSuffix(upper, "E"):
		sign = 1
	case strings.HasSuffix(upper, "S"), strings.HasSuffix(upper, "W"):
		sign = -1
	default:
		return 0, fmt.Errorf("missing hemisphere indicator (N/S/E/W)")
	}

	// trim N/S/E/W
	// `31 deg 34' 5.84" N` -> `31 deg 34' 5.84" `
	body := dms[:len(dms)-1]

	// `31 deg 34' 5.84" ` -> `31 deg 34' 5.84"`
	body = strings.TrimSpace(body)

	// `31 deg 34' 5.84"` -> `31   34' 5.84"`
	body = strings.ReplaceAll(body, "deg", " ")

	// `31   34' 5.84"` -> `31   34  5.84"`
	body = strings.ReplaceAll(body, "'", " ")

	// `31   34  5.84"` -> `31   34  5.84 `
	body = strings.ReplaceAll(body, `"`, " ")

	// `31   34  5.84 ` -> ["31", "34", "5.84"]
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
