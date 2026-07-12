package admin

import (
	"context"
	"fmt"

	"github.com/jammutkarsh/wandersort/pkg/db"
	"github.com/jammutkarsh/wandersort/pkg/logger"
)

func RunReset(ctx context.Context, log logger.Logger, dbPath string) (*ResetResponse, error) {
	appDB, err := db.New(ctx, dbPath, db.AppDB, log)
	if err != nil {
		return nil, fmt.Errorf("open app db: %w", err)
	}
	defer appDB.Close()

	resetSvc := NewService(log, NewRepository(appDB))
	resp, err := resetSvc.Reset(ctx)
	if err != nil {
		return nil, fmt.Errorf("reset: %w", err)
	}

	if err := appDB.Optimize(ctx); err != nil {
		log.Warn("database optimization after reset failed", "error", err)
	}

	return &resp, nil
}
