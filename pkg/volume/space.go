package volume

import (
	"context"
	"fmt"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

// CheckOutputSpace warns once when the output volume can't hold the library.
// Best-effort: never a failure.
func CheckOutputSpace(ctx context.Context, database *db.DB, log logger.Logger, outputDir string) {
	// one file per content hash: duplicates are never copied
	var librarySize int64
	if err := database.SQL.GetContext(ctx, &librarySize,
		`SELECT COALESCE(SUM(size), 0) FROM (
			SELECT MIN(fr.file_size) AS size FROM file_registry fr
			JOIN file_metadata fm ON fm.file_id = fr.id
			GROUP BY fm.file_hash)`); err != nil {
		log.Error("Failed to size the library", "error", err)
		return
	}

	free, err := FreeBytes(outputDir)
	if err != nil {
		log.Warn("Cannot check output volume free space", "path", outputDir, "error", err)
		return
	}

	if uint64(librarySize) > free {
		msg := fmt.Sprintf("Output volume may be too small: organizing the library needs up to %s, but only %s is free at %s",
			HumanBytes(uint64(librarySize)), HumanBytes(free), outputDir)
		log.Warn(msg, logger.UserKey, true)
	}
}

// FreeBytes returns the bytes available to the current user on the volume
// containing path.
func FreeBytes(path string) (uint64, error) {
	free, _, err := Space(path)
	return free, err
}

// minReserve and reserveShare size the room a transfer leaves free: 1 GiB or 1%
// of the volume, whichever is more. A full disk breaks the database's next
// write.
const (
	minReserve   = 1 << 30
	reserveShare = 100 // 1/100 of the volume
)

// TransferNeeds is the free space a transfer of fileBytes needs: the files, the
// backup (2 × dbBytes: plain and compressed copies briefly coexist) and the
// reserve.
func TransferNeeds(fileBytes, dbBytes, total uint64) uint64 {
	return fileBytes + 2*dbBytes + max(minReserve, total/reserveShare)
}

// HumanBytes renders n as a short base-1024 size, e.g. "1.5 GiB"
func HumanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
