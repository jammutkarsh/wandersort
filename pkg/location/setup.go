package location

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
	"github.com/jammutkarsh/wandersort/pkg/utils"
)

// Setup downloads the location database and its metadata if they do not exist
func Setup(ctx context.Context, log logger.Logger, dbPath string) error {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("create dir %q: %w", dbPath, err)
	}

	if _, err := os.Stat(dbPath); err == nil {
		log.Info("location db found", "path", dbPath)
		return nil
	}

	log.Info("Downloading location database…", logger.UserKey, true, "url", db.LocationDownloadBaseURL+"/"+db.LocationDBFileName)

	if err := utils.DownloadFile(ctx, dbPath, db.LocationDownloadBaseURL+"/"+db.LocationDBFileName); err != nil {
		return fmt.Errorf("download %s: %w", db.LocationDBFileName, err)
	}

	metaPath := filepath.Join(filepath.Dir(dbPath), db.LocationMetaFileName)
	if err := utils.DownloadFile(ctx, metaPath, db.LocationDownloadBaseURL+"/"+db.LocationMetaFileName); err != nil {
		log.Warn("location db: could not download metadata (non-fatal)", "file", db.LocationMetaFileName, "error", err)
	}

	return nil
}
