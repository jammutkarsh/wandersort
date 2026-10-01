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

// extractTimeout bounds one file. exiftool reads a header, so a call this long
// is stuck (a parser loop, a cloud placeholder that never downloads).
const extractTimeout = 2 * time.Minute

// ErrProcess means the exiftool process failed (died, hung, broken pipes), not
// the file: the worker is dead and the file's metadata is unknown, not empty.
var ErrProcess = errors.New("exiftool process failed")

// ErrUnsafePath refuses a path with a line break: the -@ argument file is one
// argument per line, so the rest would become exiftool options.
var ErrUnsafePath = errors.New("path contains a line break and cannot be passed to exiftool")

// ErrNoOutput means exiftool answered with no metadata for the file (it could
// not open it): the tags are unknown, not empty. The worker stays usable.
var ErrNoOutput = errors.New("exiftool returned no metadata for the file")

// exiftoolTags lists every tag ParseMetadata reads (~90% smaller output than
// all tags). No -fast2: it drops GPS/CreationDate from QuickTime videos.
var exiftoolTags = []string{
	"-ExifToolVersion", "-SourceFile", "-Directory", "-FileName", "-FileSize",
	"-FilePermissions", "-FileType", "-FileTypeExtension", "-MIMEType",
	"-FileModifyDate", "-FileAccessDate", "-FileInodeChangeDate",
	"-ImageWidth", "-ImageHeight", "-ImageSize", "-Megapixels",
	"-Orientation",
	"-Make", "-Model", "-LensModel", "-Software",
	"-CreateDate", "-ModifyDate", "-DateTimeOriginal", "-CreationDate", "-MediaCreateDate",
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

	timeout time.Duration // per-file bound; see extractTimeout
}

// New starts one exiftool -stay_open process.
func New(exiftoolPath string) (*Extractor, error) {
	return newExtractor(exiftoolPath, extractTimeout)
}

func newExtractor(exiftoolPath string, timeout time.Duration) (*Extractor, error) {
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
		cmd:     cmd,
		stdin:   stdin,
		reader:  bufio.NewReaderSize(stdout, 64*1024),
		timeout: timeout,
	}, nil
}

// Extract runs exiftool on one file via the persistent process. Cancellation and
// the timeout kill the process (the only way to unblock a stdout read), leaving
// the worker dead (ErrProcess).
func (e *Extractor) Extract(ctx context.Context, path string) (classifier.CommonMetadata, error) {
	if strings.ContainsAny(path, "\r\n") {
		return classifier.CommonMetadata{}, ErrUnsafePath
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dead {
		return classifier.CommonMetadata{}, fmt.Errorf("%w: worker already stopped", ErrProcess)
	}

	ctx, cancel := context.WithTimeout(ctx, e.timeout)
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

	args := make([]string, 0, 4+len(exiftoolTags)+2)
	// -charset filename=utf8: read the path as UTF-8, not the Windows code page
	args = append(args, "-json", "-n", "-charset", "filename=utf8")
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
		return classifier.CommonMetadata{}, fmt.Errorf("%w: output is not a JSON array: %w", ErrNoOutput, err)
	}
	if len(arr) == 0 {
		return classifier.CommonMetadata{}, fmt.Errorf("%w: empty array", ErrNoOutput)
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
