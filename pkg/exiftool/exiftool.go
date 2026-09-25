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
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jammutkarsh/wandersort/pkg/classifier"
)

const readyToken = "{ready}"

// extractTimeout bounds one file. exiftool reads a header, not the file, so a
// healthy call takes milliseconds even on a slow disk; one that runs this long
// is stuck (a malformed file looping the parser, a cloud placeholder that never
// downloads) and would otherwise hold its worker, and the whole scan, forever.
// A var so a test can shorten it.
var extractTimeout = 2 * time.Minute

// ErrProcess means the exiftool process itself failed — it died, hung past
// extractTimeout, or its pipes broke — rather than the file being unreadable
// to it. The worker is dead after this and the pool replaces it; the file's
// metadata is unknown, not empty.
var ErrProcess = errors.New("exiftool process failed")

// ErrUnsafePath means the path cannot be passed to exiftool at all. Its
// argument file (-@) is one argument per line, so a newline in a filename
// would end the path early and turn the rest of the name into exiftool
// options — and exiftool writes files (-all= -overwrite_original).
var ErrUnsafePath = errors.New("path contains a line break and cannot be passed to exiftool")

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
	dead   bool          // killed or broken; the pool replaces it before reuse
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
//
// ctx cancellation and extractTimeout both kill the process: a read blocked
// on its stdout cannot be interrupted any other way. That leaves the worker
// dead (ErrProcess), and the pool starts a fresh one for the next file.
func (e *Extractor) Extract(ctx context.Context, path string) (classifier.CommonMetadata, error) {
	if strings.ContainsAny(path, "\r\n") {
		return classifier.CommonMetadata{}, ErrUnsafePath
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dead {
		return classifier.CommonMetadata{}, fmt.Errorf("%w: worker already stopped", ErrProcess)
	}

	ctx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { e.cmd.Process.Kill() })
	// A kill that raced a successful answer still took the process down:
	// the answer stands, but the worker must not be used again.
	defer func() {
		if !stop() {
			e.dead = true
		}
	}()
	fail := func(what string, err error) (classifier.CommonMetadata, error) {
		e.dead = true
		if ctx.Err() != nil {
			err = ctx.Err() // the kill caused the pipe error; say why it was killed
		}
		return classifier.CommonMetadata{}, fmt.Errorf("%w: %s: %w", ErrProcess, what, err)
	}

	args := make([]string, 0, 2+len(exiftoolTags)+2)
	args = append(args, "-json", "-n")
	args = append(args, exiftoolTags...)
	args = append(args, path, "-execute")
	for _, arg := range args {
		if _, err := fmt.Fprintln(e.stdin, arg); err != nil {
			return fail("writing to exiftool stdin", err)
		}
	}

	var out bytes.Buffer
	for {
		line, err := e.reader.ReadString('\n')
		if err != nil {
			return fail("reading exiftool stdout", err)
		}
		if trimmed := bytes.TrimRight([]byte(line), "\r\n"); string(trimmed) == readyToken {
			break
		}
		out.WriteString(line)
	}

	// Tags are whatever the camera wrote: bytes that aren't UTF-8, or a
	// repeated name, cost that tag at most — never the whole file.
	var arr []jsontext.Value
	if err := json.Unmarshal(out.Bytes(), &arr,
		jsontext.AllowInvalidUTF8(true), jsontext.AllowDuplicateNames(true)); err != nil {
		return classifier.CommonMetadata{}, fmt.Errorf("exiftool output is not a JSON array: %w", err)
	}
	if len(arr) == 0 {
		return classifier.CommonMetadata{}, fmt.Errorf("exiftool returned an empty array for %s", path)
	}

	return classifier.ParseMetadata(filepath.Ext(path), arr[0])
}

// Dead reports whether the process was killed or broke; the pool checks it
// before handing the worker out again.
func (e *Extractor) Dead() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.dead
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

	err := e.cmd.Wait()
	if e.dead {
		return nil // killed on purpose; "signal: killed" is not a shutdown failure
	}
	return err
}
