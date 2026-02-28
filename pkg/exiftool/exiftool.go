package exiftool

import (
	"context"
	"os/exec"
	"path/filepath"
	"sync"
)

// Extractor runs exiftool as a subprocess and parses its JSON output.
// Multiple workers execute concurrently; each worker owns one subprocess call.
type Extractor struct {
	cfg Config
}

// New returns a ready-to-use Extractor.
func New(cfg Config) *Extractor {
	cfg.setDefaults()
	return &Extractor{cfg: cfg}
}

// Extract runs exiftool on a single file and returns the parsed Result.
func (e *Extractor) Extract(ctx context.Context, path string) Result {
	var cmdCtx context.Context
	var cancel context.CancelFunc

	if e.cfg.Timeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, e.cfg.Timeout)
		defer cancel()
	} else {
		cmdCtx = ctx
	}

	// -json: output as JSON array; -n: numeric values (no unit strings)
	raw, err := exec.CommandContext(cmdCtx, "exiftool", "-json", "-n", path).Output()
	if err != nil {
		return Result{SourceFile: path, Err: err}
	}

	first, err := extractFirst(raw)
	if err != nil {
		return Result{SourceFile: path, Err: err}
	}

	ext := filepath.Ext(path)
	common, err := dispatch(ext, first)
	if err != nil {
		return Result{SourceFile: path, Err: err}
	}

	return Result{SourceFile: path, Common: common}
}

// ExtractBatch fans the paths out across e.cfg.Workers goroutines.
// Results are returned in the same order as the input paths.
func (e *Extractor) ExtractBatch(ctx context.Context, paths []string) []Result {
	results := make([]Result, len(paths))

	// Pre-fill the jobs channel and close it so workers stop naturally.
	jobs := make(chan int, len(paths))
	for i := range paths {
		jobs <- i
	}
	close(jobs)

	var wg sync.WaitGroup
	for range e.cfg.Workers {
		wg.Go(func() {
			for idx := range jobs {
				if ctx.Err() != nil {
					results[idx] = Result{SourceFile: paths[idx], Err: ctx.Err()}
					continue
				}
				results[idx] = e.Extract(ctx, paths[idx])
			}
		})
	}
	wg.Wait()

	return results
}
