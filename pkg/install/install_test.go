// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestReadinessIsNotReadyUntilClosed covers the non-blocking peeks: before
// anything closes locReady, LocationReady and LocationDBIfReady must not
// block and must report "not ready" — the whole point of these being
// separate from the blocking Location/Exiftool getters.
func TestReadinessIsNotReadyUntilClosed(t *testing.T) {
	c := New(Options{})

	if c.LocationReady() {
		t.Error("LocationReady() = true before Start, want false")
	}
	if got := c.LocationDBIfReady(); got != nil {
		t.Errorf("LocationDBIfReady() = %v before Start, want nil", got)
	}
}

// TestGettersUnblockOnceReady covers the happens-before contract the whole
// package exists for: once locReady/exifReady close, the blocking getters
// return the values written before the close, from a different goroutine,
// with no data race (run with -race).
func TestGettersUnblockOnceReady(t *testing.T) {
	c := New(Options{})

	go func() {
		c.exifPath, c.exifErr = "/bin/exiftool", nil
		close(c.exifReady)
		c.resolver, c.locErr = nil, nil
		close(c.locReady)
	}()

	path, err := c.Exiftool()
	if err != nil || path != "/bin/exiftool" {
		t.Errorf("Exiftool() = %q, %v, want /bin/exiftool, nil", path, err)
	}
	// Location() blocks until locReady closes, establishing the
	// happens-before edge the LocationReady() check below relies on.
	if _, err := c.Location(); err != nil {
		t.Errorf("Location() = %v, want nil", err)
	}
	if !c.LocationReady() {
		t.Error("LocationReady() = false after locReady closed")
	}
}

func TestDownloadVerifiesChecksum(t *testing.T) {
	body := []byte("wandersort dependency payload")
	goodSum := fmt.Sprintf("%x", sha256.Sum256(body))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	tests := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{"no digest requested", "", false},
		{"correct digest", goodSum, false},
		{"wrong digest", "0000000000000000000000000000000000000000000000000000000000000000", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), "payload.bin")

			err := downloadFile(context.Background(), dest, srv.URL, tt.want, nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Download error = %v, wantErr %v", err, tt.wantErr)
			}

			got, statErr := os.ReadFile(dest)
			if tt.wantErr {
				// a rejected download must not leave the bad file behind
				if statErr == nil {
					t.Errorf("dest still exists after a checksum mismatch")
				}
				return
			}
			if statErr != nil {
				t.Fatalf("dest missing after a successful download: %v", statErr)
			}
			if string(got) != string(body) {
				t.Errorf("dest = %q, want %q", got, body)
			}
		})
	}
}
