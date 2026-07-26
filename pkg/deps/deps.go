// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package deps is the single place a downloadable dependency is fetched and
// verified. exiftool and the location database differ in what they do with
// the file afterwards (unpack an archive vs open a database), but the fetch
// itself — atomic write, progress reporting, checksum — is one implementation.
package deps

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Download fetches url and writes the body to dest atomically (via a temp file
// in the same directory) so a partial or tampered download never leaves a bad
// file at dest.
//
// onProgress (may be nil) is invoked as bytes arrive with (bytesSoFar,
// totalBytes); total is -1 when the server sends no Content-Length. It runs on
// the download goroutine and must not block.
//
// wantSHA256 (may be empty) is the expected hex digest. A mismatch removes dest
// and returns an error — this is the one place a dependency download is
// verified, so exiftool and the location database can't drift apart on it.
func Download(ctx context.Context, dest, url, wantSHA256 string, onProgress func(done, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: unexpected status %s", url, resp.Status)
	}

	// Write to a temp file in the same directory so os.Rename is atomic
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".dl-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // no-op if Rename succeeded
	}()

	var src io.Reader = resp.Body
	if onProgress != nil {
		src = &progressReader{r: resp.Body, total: resp.ContentLength, onProgress: onProgress}
	}
	if _, err := io.Copy(tmp, src); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("rename to %s: %w", dest, err)
	}

	if wantSHA256 != "" {
		sum, err := SHA256File(dest)
		if err != nil {
			os.Remove(dest)
			return fmt.Errorf("checksum %s: %w", dest, err)
		}
		if sum != wantSHA256 {
			os.Remove(dest)
			return fmt.Errorf("checksum mismatch for %s: got %s, want %s", filepath.Base(dest), sum, wantSHA256)
		}
	}

	return nil
}

// SHA256File returns the hex-encoded SHA-256 hash of the file at path.
func SHA256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

// progressReader reports cumulative bytes read to onProgress as they flow.
type progressReader struct {
	r          io.Reader
	total      int64
	done       int64
	onProgress func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.done += int64(n)
		p.onProgress(p.done, p.total)
	}
	return n, err
}
