package pipeline

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jammutkarsh/wandersort/pkg/core/workflow"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/path"
	sm "github.com/jammutkarsh/wandersort/pkg/statusmanager"
)

// Service orchestrates both scan submission and status streaming.
type Service struct {
	pipeline *workflow.Workflow
	repo     *Repository
	logger   logger.Logger
	path     *path.Resolver
}

func NewService(log logger.Logger, workflow *workflow.Workflow, repo *Repository) *Service {
	return &Service{pipeline: workflow, repo: repo, logger: log, path: path.New()}
}

func (s *Service) StartScan(paths []string) (uuid.UUID, []string, error) {
	effectivePaths, err := s.prepareScanRoots(paths)
	if err != nil {
		return uuid.Nil, nil, err
	}

	sessionID, err := s.pipeline.SubmitScan(effectivePaths)
	if err != nil {
		return uuid.Nil, nil, err
	}

	return sessionID, effectivePaths, nil
}

// prepareScanRoots turns the incoming request into the final list of
// directories that will actually be scanned.
//
// Stage 1: resolve each path to a canonical absolute directory.
// Stage 2: remove exact duplicates after canonicalization.
// Stage 3: sort lexicographically — this guarantees every descendant of a
//
//	path appears contiguously after it in the slice.
//
// Stage 4: single-pass prune: compare each candidate only against the last
//
//	accepted root. Because paths are sorted, if a candidate is a child
//	of *any* accepted root it must be a child of the most-recently
//	accepted one, so one comparison suffices — O(N) instead of O(N²).
func (s *Service) prepareScanRoots(paths []string) ([]string, error) {
	canonicalSet := make(map[string]struct{}, len(paths))

	for _, p := range paths {
		cleaned := filepath.Clean(p)
		resolved, err := s.path.RealPath(cleaned)
		if err != nil {
			s.logger.Warn("Invalid root path", "path", p, "error", err)
			return nil, err
		}

		isDir, err := s.path.IsDirectory(resolved)
		if err != nil {
			s.logger.Warn("Cannot stat root path", "path", resolved, "error", err)
			return nil, err
		}
		if !isDir {
			s.logger.Warn("Root path is not a directory", "path", resolved)
			return nil, fmt.Errorf("path is not a directory: %s", resolved)
		}

		canonicalSet[resolved] = struct{}{}
	}

	canonicalPaths := make([]string, 0, len(canonicalSet))
	for p := range canonicalSet {
		canonicalPaths = append(canonicalPaths, p)
	}
	// Paths will be lexicographically sorted, so any child path follows its parent.
	// This makes the single-pass prune in the next step possible.
	sort.Strings(canonicalPaths)

	// Single-pass prune: O(N).
	// After lex sort, any descendant of an accepted root immediately follows
	// it (or follows another descendant of it). Comparing against only the
	// last accepted root is therefore sufficient.
	effectivePaths := make([]string, 0, len(canonicalPaths))
	for _, candidate := range canonicalPaths {
		lenEffective := len(effectivePaths)
		if lenEffective == 0 || !isChildPath(effectivePaths[lenEffective-1], candidate) {
			effectivePaths = append(effectivePaths, candidate)
		}
	}

	return effectivePaths, nil
}

// isChildPath reports whether candidate is strictly nested below parent.
// Both paths must already be canonical (filepath.Clean + RealPath).
func isChildPath(parent, candidate string) bool {
	// Equal paths are not a parent–child relationship.
	if parent == candidate {
		return false
	}
	// Append the separator so "/foo" doesn't falsely match "/foobar".
	return strings.HasPrefix(candidate, parent+string(filepath.Separator))
}

func (s *Service) SubscribeStatus() chan sm.WorkflowStatus {
	return s.pipeline.StatusStream()
}

func (s *Service) UnsubscribeStatus(ch chan sm.WorkflowStatus) {
	s.pipeline.UnsubscribeStatus(ch)
}

func (s *Service) GetFileCount(ctx context.Context) (FileCountResponse, error) {
	return s.repo.GetFileCount(ctx)
}
