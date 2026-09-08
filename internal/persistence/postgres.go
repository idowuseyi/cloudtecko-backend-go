package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
)

// postgresDriverName is the driver registered by github.com/lib/pq, which is
// blank-imported in internal/api/server.go and cmd/*.
const postgresDriverName = "postgres"

func newPostgresDB(ctx context.Context, cfg config.Database) (*sql.DB, error) {
	db, err := sql.Open(postgresDriverName, cfg.ConnectionString())
	if err != nil {
		return nil, fmt.Errorf("failed to open db connection: %w", err)
	}

	applyPoolSettings(db, cfg)

	if !cfg.PingOnConnect {
		return db, nil
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.PingTimeout)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {
		// Close the handle we just opened; the caller only cleans up on success.
		_ = db.Close()

		return nil, fmt.Errorf("failed to ping DB: %w", err)
	}

	return db, nil
}
