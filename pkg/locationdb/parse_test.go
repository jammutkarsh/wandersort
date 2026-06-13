package locationdb

import (
	"math"
	"testing"
)

func TestParseGPS_ValidDMS(t *testing.T) {
	lat, lon, err := ParseGPS(`31 deg 34' 5.84" N`, `77 deg 22' 14.32" E`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantLat := 31 + 34.0/60 + 5.84/3600
	wantLon := 77 + 22.0/60 + 14.32/3600

	if !almostEqual(lat, wantLat, 1e-6) {
		t.Errorf("lat = %v, want %v", lat, wantLat)
	}
	if !almostEqual(lon, wantLon, 1e-6) {
		t.Errorf("lon = %v, want %v", lon, wantLon)
	}
}

func TestParseGPS_SouthWestNegative(t *testing.T) {
	lat, lon, err := ParseGPS(`33 deg 52' 0.00" S`, `151 deg 12' 36.00" W`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if lat >= 0 {
		t.Errorf("expected negative latitude for S, got %v", lat)
	}

	if lon >= 0 {
		t.Errorf("expected negative longitude for W, got %v", lon)
	}
}

func TestParseGPS_InvalidInput_NoHemishpere(t *testing.T) {
	_, _, err := ParseGPS(`31 deg 34' 5.84"`, `77 deg 22' 14.32" E`)
	if err == nil {
		t.Error("expected error for missing hemisphere, got nil")
	}
}

func TestParseGPS_InvalidInput_Empty(t *testing.T) {
	_, _, err := ParseGPS("", "")
	if err == nil {
		t.Error("expected error for empty string, got nil")
	}
}

func TestParseGPS_InvalidInput_GarbageText(t *testing.T) {
	_, _, err := ParseGPS("not a coordinate", "also garbage")
	if err == nil {
		t.Error("expected error for garbage input, got nil")
	}
}

// almostEqual reports whether a and b differ by less than epsilon(small tolerance value).
func almostEqual(a, b, eps float64) bool {
	return math.Abs(a-b) < eps
}
