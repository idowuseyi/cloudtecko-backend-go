// Package persistence owns the *sql.DB handed to the rest of the application.
//
// It is the single seam between the config layer and a concrete SQL driver.
// Only Postgres is implemented; see mysql.go and docs/database-dialects.md for
// what supporting a second dialect would actually require.
package persistence

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
)

// ErrUnsupportedDialect is returned when the configured dialect has no driver.
var ErrUnsupportedDialect = fmt.Errorf("unsupported database dialect")

// NewDB opens, configures and (optionally) verifies a database connection for the
// configured dialect.
func NewDB(ctx context.Context, cfg config.Database) (*sql.DB, error) {
	// An empty dialect means the config predates DB_DIALECT, or was built by a
	// test that only filled in connection details. Postgres is the only dialect
	// this codebase has ever had, so it is the right default.
	dialect := cfg.Dialect
	if dialect == "" {
		dialect = config.DialectPostgres
	}

	switch dialect {
	case config.DialectPostgres:
		return newPostgresDB(ctx, cfg)
	case config.DialectMySQL:
		return nil, newMySQLDB(cfg)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedDialect, dialect)
	}
}

// applyPoolSettings applies the connection-pool limits shared by every dialect.
func applyPoolSettings(db *sql.DB, cfg config.Database) {
	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}
}
