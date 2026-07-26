// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package deps

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

			err := Download(context.Background(), dest, srv.URL, tt.want, nil)
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
