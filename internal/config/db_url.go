package config

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Managed Postgres providers (Neon, Aiven, Supabase, Render, RDS, ...) hand out a
// single URL rather than the discrete PG* variables the template was built around.
// This file turns such a URL into a Database config so the rest of the codebase --
// including ConnectionString(), which lib/pq consumes -- keeps working unchanged.

// defaultURLSSLMode is applied when a DATABASE_URL carries no sslmode of its own.
// Unlike the PG* path (which defaults to "disable" for the local Docker Postgres),
// a URL almost always points at a remote, managed database, so requiring TLS is the
// only safe default.
const defaultURLSSLMode = "require"

// libpqSafeParams are the query parameters lib/pq either understands itself or can
// safely forward to the server as a runtime parameter.
//
// This allowlist is not paranoia. lib/pq forwards every option it does not
// recognise to Postgres in the startup packet, and the server rejects unknown ones
// with "unrecognized configuration parameter". Neon, for example, appends
// "channel_binding=require" to the connection strings it shows you -- a real libpq
// client parameter that lib/pq does not implement -- so passing a provider URL
// through verbatim fails at connect time with a confusing server-side error.
var libpqSafeParams = map[string]bool{
	"application_name":          true,
	"binary_parameters":         true,
	"client_encoding":           true,
	"connect_timeout":           true,
	"datestyle":                 true,
	"fallback_application_name": true,
	"krbspn":                    true,
	"krbsrvname":                true,
	"options":                   true,
	"search_path":               true,
	"sslcert":                   true,
	"sslinline":                 true,
	"sslkey":                    true,
	"sslmode":                   true,
	"sslrootcert":               true,
	"sslsni":                    true,
	"timezone":                  true,
}

// DatabaseFromURL parses a database URL (e.g. the DATABASE_URL handed out by Neon,
// Aiven, Supabase or Render) into a Database config.
//
// Pool settings are not derived from the URL; the caller applies those separately.
// Any query parameter lib/pq cannot handle is dropped and reported in the returned
// slice so the caller can log it rather than fail opaquely at connect time.
func DatabaseFromURL(rawURL string) (Database, []string, error) {
	var dropped []string

	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		// Deliberately not wrapping: url.Parse errors embed the raw URL, which
		// contains the password.
		return Database{}, nil, fmt.Errorf("failed to parse database URL: invalid URL syntax")
	}

	dialect, err := dialectFromURLScheme(u.Scheme)
	if err != nil {
		return Database{}, nil, err
	}

	port := defaultPortForDialect(dialect)
	if raw := u.Port(); raw != "" {
		parsed, convErr := strconv.Atoi(raw)
		if convErr != nil {
			return Database{}, nil, fmt.Errorf("failed to parse database URL: invalid port %q", raw)
		}
		port = parsed
	}

	name := strings.TrimPrefix(u.Path, "/")
	if name == "" {
		return Database{}, nil, fmt.Errorf("failed to parse database URL: no database name in path")
	}

	dbConfig := Database{
		Dialect:          dialect,
		Host:             u.Hostname(),
		Port:             port,
		Database:         name,
		AdditionalParams: map[string]string{},
	}

	if u.User != nil {
		dbConfig.Username = u.User.Username()
		if pw, ok := u.User.Password(); ok {
			dbConfig.Password = pw
		}
	}

	for key, values := range u.Query() {
		if len(values) == 0 {
			continue
		}

		lower := strings.ToLower(key)
		if !libpqSafeParams[lower] {
			dropped = append(dropped, lower)
			continue
		}

		dbConfig.AdditionalParams[lower] = values[0]
	}

	sort.Strings(dropped)

	if _, ok := dbConfig.AdditionalParams["sslmode"]; !ok {
		dbConfig.AdditionalParams["sslmode"] = defaultURLSSLMode
	}

	return dbConfig, dropped, nil
}

func dialectFromURLScheme(scheme string) (string, error) {
	switch strings.ToLower(scheme) {
	case "postgres", "postgresql":
		return DialectPostgres, nil
	case "mysql":
		// Accepted so the error surfaces from persistence.NewDB with the full
		// "what MySQL would require" explanation, rather than here.
		return DialectMySQL, nil
	case "":
		return "", fmt.Errorf("failed to parse database URL: missing scheme (expected postgres://)")
	default:
		return "", fmt.Errorf("failed to parse database URL: unsupported scheme %q", scheme)
	}
}

func defaultPortForDialect(dialect string) int {
	if dialect == DialectMySQL {
		return 3306
	}

	return 5432
}
