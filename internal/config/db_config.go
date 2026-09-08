package config

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/rs/zerolog/log"
)

// Supported SQL dialects. Only DialectPostgres is implemented today; DialectMySQL
// exists so the seam in internal/persistence has a name to reject, and so a mysql://
// DATABASE_URL fails with an explanation instead of a parse error.
// See docs/database-dialects.md for what adding MySQL would actually involve.
const (
	DialectPostgres = "postgres"
	DialectMySQL    = "mysql"
)

// The DatabaseMigrationTable name is baked into the binary
// This setting should always be in sync with dbconfig.yml, sqlboiler.toml and the live database (e.g. to be able to test producation dumps locally)
const DatabaseMigrationTable = "migrations"

// The DatabaseMigrationFolder (folder with all *.sql migrations).
// This settings should always be in sync with dbconfig.yaml and Dockerfile (the final app stage).
// It's expected that the migrations folder lives at the root of this project or right next to the app binary.
var DatabaseMigrationFolder = filepath.Join(util.GetProjectRootDir(), "/migrations")

type Database struct {
	// Dialect selects the driver in internal/persistence. Defaults to DialectPostgres.
	Dialect          string
	Host             string
	Port             int
	Username         string
	Password         string `json:"-"` // sensitive
	Database         string
	AdditionalParams map[string]string `json:"additionalParams,omitempty"` // Optional additional connection parameters mapped into the connection string
	MaxOpenConns     int
	MaxIdleConns     int
	ConnMaxLifetime  time.Duration
	// PingOnConnect verifies the connection while the DI graph is being built.
	// Upstream did this unconditionally, which means a database that is briefly
	// unreachable takes the whole process down at construction time -- awkward on
	// a scale-to-zero platform where every cold start pays that cost.
	PingOnConnect bool
	PingTimeout   time.Duration
}

// ConnectionString generates a libpq keyword/value connection string to be passed to
// sql.Open or equivalents. This is Postgres-specific by construction; a MySQL driver
// would need its own DSN builder (see docs/database-dialects.md).
func (c Database) ConnectionString() string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "host=%s port=%d user=%s password=%s dbname=%s", c.Host, c.Port, c.Username, c.Password, c.Database)

	if _, ok := c.AdditionalParams["sslmode"]; !ok {
		builder.WriteString(" sslmode=disable")
	}

	if len(c.AdditionalParams) > 0 {
		params := make([]string, 0, len(c.AdditionalParams))
		for param := range c.AdditionalParams {
			params = append(params, param)
		}

		sort.Strings(params)

		for _, param := range params {
			fmt.Fprintf(&builder, " %s=%s", param, c.AdditionalParams[param])
		}
	}

	return builder.String()
}

// databaseConfigFromEnv resolves the database the application connects to.
//
// Precedence:
//  1. DATABASE_URL, if set -- the form every managed provider hands out.
//  2. the discrete PG* variables, which is what docker-compose supplies.
//
// Pool settings always come from the DB_* variables, since they are a property of
// this process rather than of the database being addressed.
func databaseConfigFromEnv() Database {
	dbConfig := baseDatabaseConfigFromEnv("DATABASE_URL")

	dbConfig.MaxOpenConns = util.GetEnvAsInt("DB_MAX_OPEN_CONNS", runtime.NumCPU()*2)
	dbConfig.MaxIdleConns = util.GetEnvAsInt("DB_MAX_IDLE_CONNS", 1)
	dbConfig.ConnMaxLifetime = time.Second * time.Duration(util.GetEnvAsInt("DB_CONN_MAX_LIFETIME_SEC", 60))
	dbConfig.PingOnConnect = util.GetEnvAsBool("DB_PING_ON_CONNECT", true)
	dbConfig.PingTimeout = time.Second * time.Duration(util.GetEnvAsInt("DB_PING_TIMEOUT_SEC", 10))

	return dbConfig
}

// migrationDatabaseConfigFromEnv resolves the database that migrations are applied to.
//
// Managed providers commonly expose two endpoints: a pooled one for application
// traffic and a direct one for DDL. Running migrations through a transaction pooler
// is unreliable, so MIGRATION_DATABASE_URL lets the two be separated. When it is
// unset the application database is used, which is the correct behaviour for the
// local Docker Postgres.
func migrationDatabaseConfigFromEnv(appDB Database) Database {
	if util.GetEnv("MIGRATION_DATABASE_URL", "") == "" {
		return appDB
	}

	dbConfig := baseDatabaseConfigFromEnv("MIGRATION_DATABASE_URL")

	// Migrations are a short, single-connection workload.
	dbConfig.MaxOpenConns = 1
	dbConfig.MaxIdleConns = 1
	dbConfig.ConnMaxLifetime = appDB.ConnMaxLifetime
	dbConfig.PingOnConnect = appDB.PingOnConnect
	dbConfig.PingTimeout = appDB.PingTimeout

	return dbConfig
}

// baseDatabaseConfigFromEnv reads connection details (no pool settings) from the
// named URL variable, falling back to the discrete PG* variables when it is unset.
func baseDatabaseConfigFromEnv(urlEnvKey string) Database {
	if rawURL := util.GetEnv(urlEnvKey, ""); rawURL != "" {
		dbConfig, dropped, err := DatabaseFromURL(rawURL)
		if err != nil {
			// Panic rather than silently falling back to the PG* defaults, which
			// would point a misconfigured production deployment at "postgres:5432".
			log.Panic().Err(err).Str("envKey", urlEnvKey).Msg("Failed to parse database URL")
		}

		if len(dropped) > 0 {
			log.Warn().
				Str("envKey", urlEnvKey).
				Strs("params", dropped).
				Msg("Dropped connection parameters that the lib/pq driver cannot handle; the server would reject them as unrecognized configuration parameters")
		}

		return dbConfig
	}

	return Database{
		Dialect:          util.GetEnv("DB_DIALECT", DialectPostgres),
		Host:             util.GetEnv("PGHOST", "postgres"),
		Port:             util.GetEnvAsInt("PGPORT", 5432),
		Database:         util.GetEnv("PGDATABASE", "development"),
		Username:         util.GetEnv("PGUSER", "dbuser"),
		Password:         util.GetEnv("PGPASSWORD", ""),
		AdditionalParams: additionalParamsFromEnv(),
	}
}

// additionalParamsFromEnv maps the optional libpq environment variables onto the
// connection string. Upstream only wired up PGSSLMODE, which made TLS verification
// against a managed provider (Aiven requires sslmode=verify-full plus a CA bundle)
// impossible to configure from the environment.
func additionalParamsFromEnv() map[string]string {
	params := map[string]string{
		"sslmode": util.GetEnv("PGSSLMODE", "disable"),
	}

	for envKey, param := range map[string]string{
		"PGSSLROOTCERT":     "sslrootcert",
		"PGSSLCERT":         "sslcert",
		"PGSSLKEY":          "sslkey",
		"PGCONNECT_TIMEOUT": "connect_timeout",
		"PGAPPNAME":         "application_name",
	} {
		if val := util.GetEnv(envKey, ""); val != "" {
			params[param] = val
		}
	}

	return params
}
