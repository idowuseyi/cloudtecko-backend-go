package config_test

import (
	"testing"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
)

func TestDatabaseFromURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		url         string
		wantDialect string
		wantHost    string
		wantPort    int
		wantUser    string
		wantPass    string
		wantDB      string
		wantParams  map[string]string
		wantDropped []string
	}{
		{
			name:        "Minimal",
			url:         "postgres://user:pass@db.example.com:5432/appdb",
			wantDialect: config.DialectPostgres,
			wantHost:    "db.example.com",
			wantPort:    5432,
			wantUser:    "user",
			wantPass:    "pass",
			wantDB:      "appdb",
			// TLS is required by default: a URL almost always points at a remote db.
			wantParams: map[string]string{"sslmode": "require"},
		},
		{
			name:        "PostgresqlSchemeAndDefaultPort",
			url:         "postgresql://user:pass@db.example.com/appdb",
			wantDialect: config.DialectPostgres,
			wantHost:    "db.example.com",
			wantPort:    5432,
			wantUser:    "user",
			wantPass:    "pass",
			wantDB:      "appdb",
			wantParams:  map[string]string{"sslmode": "require"},
		},
		{
			name:        "ExplicitSSLModeWins",
			url:         "postgres://user:pass@localhost:5432/appdb?sslmode=disable",
			wantDialect: config.DialectPostgres,
			wantHost:    "localhost",
			wantPort:    5432,
			wantUser:    "user",
			wantPass:    "pass",
			wantDB:      "appdb",
			wantParams:  map[string]string{"sslmode": "disable"},
		},
		{
			// Neon hands out exactly this shape. channel_binding is a real libpq
			// parameter that lib/pq does not implement, so forwarding it makes the
			// server reject the connection as an unrecognized configuration
			// parameter. It must be dropped, and the drop must be reported.
			name:        "NeonStyleDropsUnsupportedParams",
			url:         "postgres://neondb_owner:secret@ep-cool-darkness.eu-central-1.aws.neon.tech/neondb?sslmode=require&channel_binding=require",
			wantDialect: config.DialectPostgres,
			wantHost:    "ep-cool-darkness.eu-central-1.aws.neon.tech",
			wantPort:    5432,
			wantUser:    "neondb_owner",
			wantPass:    "secret",
			wantDB:      "neondb",
			wantParams:  map[string]string{"sslmode": "require"},
			wantDropped: []string{"channel_binding"},
		},
		{
			// Aiven requires verify-full plus a CA bundle.
			name:        "AivenStyleKeepsTLSVerification",
			url:         "postgres://avnadmin:secret@pg-x.aivencloud.com:26257/defaultdb?sslmode=verify-full&sslrootcert=/etc/ssl/aiven-ca.pem",
			wantDialect: config.DialectPostgres,
			wantHost:    "pg-x.aivencloud.com",
			wantPort:    26257,
			wantUser:    "avnadmin",
			wantPass:    "secret",
			wantDB:      "defaultdb",
			wantParams: map[string]string{
				"sslmode":     "verify-full",
				"sslrootcert": "/etc/ssl/aiven-ca.pem",
			},
		},
		{
			// Supabase's transaction pooler: non-default port, dotted username.
			name:        "SupabasePoolerStyle",
			url:         "postgres://postgres.abcdefgh:secret@aws-0-eu-west-1.pooler.supabase.com:6543/postgres?sslmode=require",
			wantDialect: config.DialectPostgres,
			wantHost:    "aws-0-eu-west-1.pooler.supabase.com",
			wantPort:    6543,
			wantUser:    "postgres.abcdefgh",
			wantPass:    "secret",
			wantDB:      "postgres",
			wantParams:  map[string]string{"sslmode": "require"},
		},
		{
			// Percent-encoded credentials must survive intact, otherwise auth
			// fails in a way that looks like a wrong password.
			name:        "PercentEncodedCredentials",
			url:         "postgres://us%40er:p%40ss%3Aword%2F1@localhost:5432/appdb?sslmode=disable",
			wantDialect: config.DialectPostgres,
			wantHost:    "localhost",
			wantPort:    5432,
			wantUser:    "us@er",
			wantPass:    "p@ss:word/1",
			wantDB:      "appdb",
			wantParams:  map[string]string{"sslmode": "disable"},
		},
		{
			name:        "MySQLSchemeParsesButIsRejectedLater",
			url:         "mysql://user:pass@db.example.com/appdb",
			wantDialect: config.DialectMySQL,
			wantHost:    "db.example.com",
			wantPort:    3306,
			wantUser:    "user",
			wantPass:    "pass",
			wantDB:      "appdb",
			wantParams:  map[string]string{"sslmode": "require"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, dropped, err := config.DatabaseFromURL(tt.url)
			if err != nil {
				t.Fatalf("DatabaseFromURL() unexpected error: %v", err)
			}

			if got.Dialect != tt.wantDialect {
				t.Errorf("Dialect = %q, want %q", got.Dialect, tt.wantDialect)
			}
			if got.Host != tt.wantHost {
				t.Errorf("Host = %q, want %q", got.Host, tt.wantHost)
			}
			if got.Port != tt.wantPort {
				t.Errorf("Port = %d, want %d", got.Port, tt.wantPort)
			}
			if got.Username != tt.wantUser {
				t.Errorf("Username = %q, want %q", got.Username, tt.wantUser)
			}
			if got.Password != tt.wantPass {
				t.Errorf("Password = %q, want %q", got.Password, tt.wantPass)
			}
			if got.Database != tt.wantDB {
				t.Errorf("Database = %q, want %q", got.Database, tt.wantDB)
			}

			if len(got.AdditionalParams) != len(tt.wantParams) {
				t.Errorf("AdditionalParams = %v, want %v", got.AdditionalParams, tt.wantParams)
			}
			for k, want := range tt.wantParams {
				if got.AdditionalParams[k] != want {
					t.Errorf("AdditionalParams[%q] = %q, want %q", k, got.AdditionalParams[k], want)
				}
			}

			if len(dropped) != len(tt.wantDropped) {
				t.Fatalf("dropped = %v, want %v", dropped, tt.wantDropped)
			}
			for i, want := range tt.wantDropped {
				if dropped[i] != want {
					t.Errorf("dropped[%d] = %q, want %q", i, dropped[i], want)
				}
			}
		})
	}
}

func TestDatabaseFromURLErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
	}{
		{name: "NoScheme", url: "user:pass@localhost:5432/appdb"},
		{name: "UnsupportedScheme", url: "mongodb://user:pass@localhost:27017/appdb"},
		{name: "NoDatabaseName", url: "postgres://user:pass@localhost:5432"},
		{name: "NoDatabaseNameTrailingSlash", url: "postgres://user:pass@localhost:5432/"},
		{name: "NonNumericPort", url: "postgres://user:pass@localhost:notaport/appdb"},
		{name: "Empty", url: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, _, err := config.DatabaseFromURL(tt.url); err == nil {
				t.Fatalf("DatabaseFromURL(%q) expected an error, got nil", tt.url)
			}
		})
	}
}

// A parsed URL must survive the round trip into the libpq connection string that
// lib/pq actually consumes -- that is the only thing the driver ever sees.
func TestDatabaseFromURLProducesUsableConnectionString(t *testing.T) {
	t.Parallel()

	got, _, err := config.DatabaseFromURL("postgres://user:pass@db.example.com:5432/appdb?sslmode=require&channel_binding=require")
	if err != nil {
		t.Fatalf("DatabaseFromURL() unexpected error: %v", err)
	}

	want := "host=db.example.com port=5432 user=user password=pass dbname=appdb sslmode=require"
	if cs := got.ConnectionString(); cs != want {
		t.Errorf("ConnectionString() = %q, want %q", cs, want)
	}
}
