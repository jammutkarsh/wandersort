// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package exiftool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
)

const readyToken = "{ready}"

// exiftoolTags lists every tag ParseMetadata reads. Passing these to exiftool
// instead of requesting all tags reduces the JSON payload by ~90%. -fast2 is
// deliberately omitted: it drops GPS/CreationDate from QuickTime videos.
var exiftoolTags = []string{
	"-ExifToolVersion", "-SourceFile", "-Directory", "-FileName", "-FileSize",
	"-FilePermissions", "-FileType", "-FileTypeExtension", "-MIMEType",
	"-FileModifyDate", "-FileAccessDate", "-FileInodeChangeDate",
	"-ImageWidth", "-ImageHeight", "-ImageSize", "-Megapixels",
	"-Orientation",
	"-Make", "-Model", "-LensModel", "-Software",
	"-CreateDate", "-ModifyDate", "-DateTimeOriginal", "-CreationDate",
	"-ISO", "-Aperture", "-FNumber", "-FocalLength",
	"-ExposureTime", "-ShutterSpeed", "-ExposureMode", "-ExposureProgram",
	"-ExposureCompensation", "-Flash", "-MeteringMode", "-WhiteBalance",
	"-GPSLatitude", "-GPSLongitude", "-GPSAltitude", "-GPSAltitudeRef", "-GPSPosition",
	"-Description", "-UserComment", "-SamsungCaptureInfo",
}

// Extractor talks to a single long-lived exiftool process running in
// -stay_open mode, avoiding the per-file Perl startup cost.
type Extractor struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	mu     sync.Mutex    // serializes access: exiftool handles one batch at a time
	reader *bufio.Reader // reads stdout up to the {ready} sentinel
}

func New(exiftoolPath string) (*Extractor, error) {
	cmd := exec.Command(exiftoolPath, "-stay_open", "True", "-@", "-")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("exiftool stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("exiftool stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting exiftool -stay_open: %w", err)
	}

	return &Extractor{
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReaderSize(stdout, 64*1024),
	}, nil
}

// Extract runs exiftool on a single file via the persistent process and
// returns the parsed metadata or error. Only the tags ParseMetadata needs
// are requested, cutting the JSON payload by ~90%.
func (e *Extractor) Extract(ctx context.Context, path string) (classifier.CommonMetadata, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	args := make([]string, 0, 2+len(exiftoolTags)+2)
	args = append(args, "-json", "-n")
	args = append(args, exiftoolTags...)
	args = append(args, path, "-execute")
	for _, arg := range args {
		if _, err := fmt.Fprintln(e.stdin, arg); err != nil {
			return classifier.CommonMetadata{}, fmt.Errorf("writing to exiftool stdin: %w", err)
		}
	}

	var out bytes.Buffer
	for {
		line, err := e.reader.ReadString('\n')
		if err != nil {
			return classifier.CommonMetadata{}, fmt.Errorf("reading exiftool stdout: %w", err)
		}
		if trimmed := bytes.TrimRight([]byte(line), "\r\n"); string(trimmed) == readyToken {
			break
		}
		out.WriteString(line)
	}

	var arr []json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &arr); err != nil {
		return classifier.CommonMetadata{}, fmt.Errorf("exiftool output is not a JSON array: %w", err)
	}
	if len(arr) == 0 {
		return classifier.CommonMetadata{}, fmt.Errorf("exiftool returned an empty array for %s", path)
	}

	return classifier.ParseMetadata(filepath.Ext(path), arr[0])
}

// Close gracefully shuts down the persistent exiftool process. Call this
// exactly once when done, or you'll leak a lingering perl process.
func (e *Extractor) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	fmt.Fprintln(e.stdin, "-stay_open")
	fmt.Fprintln(e.stdin, "False")
	fmt.Fprintln(e.stdin, "-execute")
	e.stdin.Close()

	return e.cmd.Wait()
}
