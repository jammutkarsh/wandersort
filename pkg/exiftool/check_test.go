package exiftool

import (
	"testing"
)

func TestCheckVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		wantErr bool
	}{
		{"meets minimum", "12.00", false},
		{"above minimum", "13.50", false},
		{"below minimum", "11.99", true},
		{"zero version", "00.00", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkVersion(tt.version)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkVersion(%q) error = %v, wantErr %v", tt.version, err, tt.wantErr)
			}
		})
	}
}