package config

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/mailer/transport"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/push/provider"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog"
	"golang.org/x/text/language"
)

type EchoServer struct {
	Debug                          bool
	ListenAddress                  string
	HideInternalServerErrorDetails bool
	BaseURL                        string
	EnableCORSMiddleware           bool
	EnableLoggerMiddleware         bool
	EnableRecoverMiddleware        bool
	EnableRequestIDMiddleware      bool
	EnableTrailingSlashMiddleware  bool
	EnableSecureMiddleware         bool
	EnableCacheControlMiddleware   bool
	SecureMiddleware               EchoServerSecureMiddleware
	WebTemplatesViewsBaseDirAbs    string

	// CORS. Upstream used echo's zero-config CORS(), which is AllowOrigins ["*"],
	// enabled by default and with no way to narrow it from the environment.
	CORSAllowOrigins     []string
	CORSAllowMethods     []string
	CORSAllowHeaders     []string
	CORSAllowCredentials bool
	CORSMaxAgeSeconds    int

	// Request body cap. Echo's default is unlimited.
	EnableBodyLimitMiddleware bool
	BodyLimit                 string

	// Per-IP rate limiting, applied to the unauthenticated auth endpoints.
	EnableRateLimitMiddleware bool
	RateLimitRate             float64
	RateLimitBurst            int
	RateLimitExpiresIn        time.Duration

	// http.Server timeouts. Without these the server is Slowloris-exposed.
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int

	// Serve /swagger.yml publicly. The full API spec was previously always
	// readable by anyone, with no way to turn it off.
	EnablePublicSwagger bool
}

type PprofServer struct {
	Enable                      bool
	EnableManagementKeyAuth     bool
	RuntimeBlockProfileRate     int
	RuntimeMutexProfileFraction int
}

// EchoServerSecureMiddleware represents a subset of echo's secure middleware config relevant to the app server.
// https://github.com/labstack/echo/blob/master/middleware/secure.go
type EchoServerSecureMiddleware struct {
	XSSProtection         string
	ContentTypeNosniff    string
	XFrameOptions         string
	HSTSMaxAge            int
	HSTSExcludeSubdomains bool
	ContentSecurityPolicy string
	CSPReportOnly         bool
	HSTSPreloadEnabled    bool
	ReferrerPolicy        string
}

type AuthServer struct {
	AccessTokenValidity                time.Duration
	PasswordResetTokenValidity         time.Duration
	PasswordResetTokenDebounceDuration time.Duration
	PasswordResetTokenReuseDuration    time.Duration
	DefaultUserScopes                  []string
	LastAuthenticatedAtThreshold       time.Duration
	RegistrationRequiresConfirmation   bool
	ConfirmationTokenValidity          time.Duration
	ConfirmationTokenDebounceDuration  time.Duration
}

type PathsServer struct {
	APIBaseDirAbs               string
	MntBaseDirAbs               string
	AppleAppSiteAssociationFile string
	AndroidAssetlinksFile       string
}

type ManagementServer struct {
	Secret                  string `json:"-"` // sensitive
	ReadinessTimeout        time.Duration
	LivenessTimeout         time.Duration
	ProbeWriteablePathsAbs  []string
	ProbeWriteableTouchfile string
	EnableMetrics           bool
}

type FrontendServer struct {
	BaseURL               string
	PasswordResetEndpoint string
}

type LoggerServer struct {
	Level              zerolog.Level
	RequestLevel       zerolog.Level
	LogRequestBody     bool
	LogRequestHeader   bool
	LogRequestQuery    bool
	LogResponseBody    bool
	LogResponseHeader  bool
	LogCaller          bool
	PrettyPrintConsole bool
}

type I18n struct {
	DefaultLanguage language.Tag
	BundleDirAbs    string
}

type Server struct {
	Database Database
	// MigrationDatabase is the connection used to APPLY migrations. It equals
	// Database unless MIGRATION_DATABASE_URL is set, which managed providers need
	// when application traffic goes through a pooled endpoint but DDL must not.
	MigrationDatabase Database
	Echo              EchoServer
	Pprof             PprofServer
	Paths             PathsServer
	Auth              AuthServer
	Management        ManagementServer
	Mailer            Mailer
	SMTP              transport.SMTPMailTransportConfig
	Frontend          FrontendServer
	Logger            LoggerServer
	Push              PushService
	FCMConfig         provider.FCMConfig
	I18n              I18n
}

// DefaultServiceConfigFromEnv returns the server config as parsed from environment variables
// and their respective defaults defined below.
// We don't expect that ENV_VARs change while we are running our application or our tests
// (and it would be a bad thing to do anyways with parallel testing).
// Do NOT use os.Setenv / os.Unsetenv in tests utilizing DefaultServiceConfigFromEnv()!
func DefaultServiceConfigFromEnv() Server {
	// An `.env.local` file in your project root can override the currently set ENV variables.
	//
	// We never automatically apply `.env.local` when running "go test" as these ENV variables
	// may be sensitive (e.g. secrets to external APIs) and applying them modifies the process
	// global "os.Env" state (it should be applied via t.SetEnv instead).
	//
	// If you need dotenv ENV variables available in a test, do that explicitly within that
	// test before executing DefaultServiceConfigFromEnv (or test.WithTestServer).
	// See /internal/test/helper_dot_env.go: test.DotEnvLoadLocalOrSkipTest(t)
	//
	// DOTENV_DISABLE exists because this load *overrides* real ENV variables (it
	// uses os.Setenv). Once a developer puts managed-database credentials in
	// .env.local for the no-Docker workflow, `docker compose up` would otherwise
	// silently point the containerised app at the cloud database instead of the
	// container's Postgres -- a data-loss path, not just a surprise.
	// docker-compose.yml sets it, so the compose stack always uses its own ENV.
	if !testing.Testing() && !util.GetEnvAsBool("DOTENV_DISABLE", false) {
		DotEnvTryLoad(filepath.Join(util.GetProjectRootDir(), ".env.local"), os.Setenv)
	}

	databaseConfig := databaseConfigFromEnv()

	return Server{
		Database:          databaseConfig,
		MigrationDatabase: migrationDatabaseConfigFromEnv(databaseConfig),
		Echo: EchoServer{
			Debug:                          util.GetEnvAsBool("SERVER_ECHO_DEBUG", false),
			ListenAddress:                  listenAddressFromEnv(),
			HideInternalServerErrorDetails: util.GetEnvAsBool("SERVER_ECHO_HIDE_INTERNAL_SERVER_ERROR_DETAILS", true),
			BaseURL:                        util.GetEnv("SERVER_ECHO_BASE_URL", "http://localhost:8080"),
			EnableCORSMiddleware:           util.GetEnvAsBool("SERVER_ECHO_ENABLE_CORS_MIDDLEWARE", true),
			EnableLoggerMiddleware:         util.GetEnvAsBool("SERVER_ECHO_ENABLE_LOGGER_MIDDLEWARE", true),
			EnableRecoverMiddleware:        util.GetEnvAsBool("SERVER_ECHO_ENABLE_RECOVER_MIDDLEWARE", true),
			EnableRequestIDMiddleware:      util.GetEnvAsBool("SERVER_ECHO_ENABLE_REQUEST_ID_MIDDLEWARE", true),
			EnableTrailingSlashMiddleware:  util.GetEnvAsBool("SERVER_ECHO_ENABLE_TRAILING_SLASH_MIDDLEWARE", true),
			EnableSecureMiddleware:         util.GetEnvAsBool("SERVER_ECHO_ENABLE_SECURE_MIDDLEWARE", true),
			EnableCacheControlMiddleware:   util.GetEnvAsBool("SERVER_ECHO_ENABLE_CACHE_CONTROL_MIDDLEWARE", true),
			// see https://echo.labstack.com/middleware/secure
			// see https://github.com/labstack/echo/blob/master/middleware/secure.go
			SecureMiddleware: EchoServerSecureMiddleware{
				XSSProtection:         util.GetEnv("SERVER_ECHO_SECURE_MIDDLEWARE_XSS_PROTECTION", "1; mode=block"),
				ContentTypeNosniff:    util.GetEnv("SERVER_ECHO_SECURE_MIDDLEWARE_CONTENT_TYPE_NOSNIFF", "nosniff"),
				XFrameOptions:         util.GetEnv("SERVER_ECHO_SECURE_MIDDLEWARE_X_FRAME_OPTIONS", "SAMEORIGIN"),
				HSTSMaxAge:            util.GetEnvAsInt("SERVER_ECHO_SECURE_MIDDLEWARE_HSTS_MAX_AGE", 31536000),
				HSTSExcludeSubdomains: util.GetEnvAsBool("SERVER_ECHO_SECURE_MIDDLEWARE_HSTS_EXCLUDE_SUBDOMAINS", false),
				ContentSecurityPolicy: util.GetEnv("SERVER_ECHO_SECURE_MIDDLEWARE_CONTENT_SECURITY_POLICY", ""),
				CSPReportOnly:         util.GetEnvAsBool("SERVER_ECHO_SECURE_MIDDLEWARE_CSP_REPORT_ONLY", false),
				HSTSPreloadEnabled:    util.GetEnvAsBool("SERVER_ECHO_SECURE_MIDDLEWARE_HSTS_PRELOAD_ENABLED", false),
				ReferrerPolicy:        util.GetEnv("SERVER_ECHO_SECURE_MIDDLEWARE_REFERRER_POLICY", "strict-origin-when-cross-origin"),
			},
			WebTemplatesViewsBaseDirAbs: util.GetEnv("SERVER_ECHO_WEB_TEMPLATES_VIEWS_BASE_DIR_ABS", filepath.Join(util.GetProjectRootDir(), "/web/templates/views")),

			// Empty by default: an API that does not know its callers should not
			// advertise itself to every origin. Set explicitly in deployment.
			CORSAllowOrigins:     util.GetEnvAsStringArrTrimmed("SERVER_ECHO_CORS_ALLOW_ORIGINS", []string{}),
			CORSAllowMethods:     util.GetEnvAsStringArrTrimmed("SERVER_ECHO_CORS_ALLOW_METHODS", []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPatch, http.MethodPost, http.MethodDelete}),
			CORSAllowHeaders:     util.GetEnvAsStringArrTrimmed("SERVER_ECHO_CORS_ALLOW_HEADERS", []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAccept, echo.HeaderAuthorization}),
			CORSAllowCredentials: util.GetEnvAsBool("SERVER_ECHO_CORS_ALLOW_CREDENTIALS", false),
			CORSMaxAgeSeconds:    util.GetEnvAsInt("SERVER_ECHO_CORS_MAX_AGE_SECONDS", 0),

			EnableBodyLimitMiddleware: util.GetEnvAsBool("SERVER_ECHO_ENABLE_BODY_LIMIT_MIDDLEWARE", true),
			BodyLimit:                 util.GetEnv("SERVER_ECHO_BODY_LIMIT", "1M"),

			EnableRateLimitMiddleware: util.GetEnvAsBool("SERVER_ECHO_ENABLE_RATE_LIMIT_MIDDLEWARE", true),
			RateLimitRate:             float64(util.GetEnvAsInt("SERVER_ECHO_RATE_LIMIT_REQUESTS_PER_SECOND", 10)),
			RateLimitBurst:            util.GetEnvAsInt("SERVER_ECHO_RATE_LIMIT_BURST", 20),
			RateLimitExpiresIn:        time.Second * time.Duration(util.GetEnvAsInt("SERVER_ECHO_RATE_LIMIT_EXPIRES_IN_SECONDS", 180)),

			ReadTimeout:       time.Second * time.Duration(util.GetEnvAsInt("SERVER_ECHO_READ_TIMEOUT_SECONDS", 30)),
			ReadHeaderTimeout: time.Second * time.Duration(util.GetEnvAsInt("SERVER_ECHO_READ_HEADER_TIMEOUT_SECONDS", 10)),
			WriteTimeout:      time.Second * time.Duration(util.GetEnvAsInt("SERVER_ECHO_WRITE_TIMEOUT_SECONDS", 60)),
			IdleTimeout:       time.Second * time.Duration(util.GetEnvAsInt("SERVER_ECHO_IDLE_TIMEOUT_SECONDS", 120)),
			MaxHeaderBytes:    util.GetEnvAsInt("SERVER_ECHO_MAX_HEADER_BYTES", 1<<20),

			EnablePublicSwagger: util.GetEnvAsBool("SERVER_ECHO_ENABLE_PUBLIC_SWAGGER", true),
		},
		Pprof: PprofServer{
			// https://golang.org/pkg/net/http/pprof/
			Enable:                      util.GetEnvAsBool("SERVER_PPROF_ENABLE", false),
			EnableManagementKeyAuth:     util.GetEnvAsBool("SERVER_PPROF_ENABLE_MANAGEMENT_KEY_AUTH", true),
			RuntimeBlockProfileRate:     util.GetEnvAsInt("SERVER_PPROF_RUNTIME_BLOCK_PROFILE_RATE", 0),
			RuntimeMutexProfileFraction: util.GetEnvAsInt("SERVER_PPROF_RUNTIME_MUTEX_PROFILE_FRACTION", 0),
		},
		Paths: PathsServer{
			// Please ALWAYS work with ABSOLUTE (ABS) paths from ENV_VARS (however you may resolve a project-relative to absolute for the default value)
			APIBaseDirAbs:               util.GetEnv("SERVER_PATHS_API_BASE_DIR_ABS", filepath.Join(util.GetProjectRootDir(), "/api")),        // /app/api (swagger.yml)
			MntBaseDirAbs:               util.GetEnv("SERVER_PATHS_MNT_BASE_DIR_ABS", filepath.Join(util.GetProjectRootDir(), "/assets/mnt")), // /app/assets/mnt (user-generated content)
			AppleAppSiteAssociationFile: util.GetEnv("SERVER_PATHS_APPLE_APP_SITE_ASSOCIATION_FILE", ""),
			AndroidAssetlinksFile:       util.GetEnv("SERVER_PATHS_ANDROID_ASSETLINKS_FILE", ""),
		},
		Auth: AuthServer{
			AccessTokenValidity:                time.Second * time.Duration(util.GetEnvAsInt("SERVER_AUTH_ACCESS_TOKEN_VALIDITY", 86400)),
			PasswordResetTokenValidity:         time.Second * time.Duration(util.GetEnvAsInt("SERVER_AUTH_PASSWORD_RESET_TOKEN_VALIDITY", 900)),
			PasswordResetTokenDebounceDuration: time.Second * time.Duration(util.GetEnvAsInt("SERVER_AUTH_PASSWORD_RESET_TOKEN_DEBOUNCE_DURATION_SECONDS", 60)),
			PasswordResetTokenReuseDuration:    time.Second * time.Duration(util.GetEnvAsInt("SERVER_AUTH_PASSWORD_RESET_TOKEN_REUSE_DURATION_SECONDS", 0)),
			DefaultUserScopes:                  util.GetEnvAsStringArr("SERVER_AUTH_DEFAULT_USER_SCOPES", []string{"app"}),
			LastAuthenticatedAtThreshold:       time.Second * time.Duration(util.GetEnvAsInt("SERVER_AUTH_LAST_AUTHENTICATED_AT_THRESHOLD", 900)),
			RegistrationRequiresConfirmation:   util.GetEnvAsBool("SERVER_AUTH_REGISTRATION_REQUIRES_CONFIRMATION", false),
			ConfirmationTokenValidity:          time.Second * time.Duration(util.GetEnvAsInt("SERVER_AUTH_CONFIRMATION_TOKEN_VALIDITY_SECONDS", 86400)),
			ConfirmationTokenDebounceDuration:  time.Second * time.Duration(util.GetEnvAsInt("SERVER_AUTH_CONFIRMATION_TOKEN_DEBOUNCE_DURATION_SECONDS", 60)),
		},
		Management: ManagementServer{
			Secret:           util.GetMgmtSecret("SERVER_MANAGEMENT_SECRET"),
			ReadinessTimeout: time.Second * time.Duration(util.GetEnvAsInt("SERVER_MANAGEMENT_READINESS_TIMEOUT_SEC", 4)),
			LivenessTimeout:  time.Second * time.Duration(util.GetEnvAsInt("SERVER_MANAGEMENT_LIVENESS_TIMEOUT_SEC", 9)),
			ProbeWriteablePathsAbs: util.GetEnvAsStringArr("SERVER_MANAGEMENT_PROBE_WRITEABLE_PATHS_ABS", []string{
				filepath.Join(util.GetProjectRootDir(), "/assets/mnt")}, ","),
			ProbeWriteableTouchfile: util.GetEnv("SERVER_MANAGEMENT_PROBE_WRITEABLE_TOUCHFILE", ".healthy"),
			EnableMetrics:           util.GetEnvAsBool("SERVER_MANAGEMENT_ENABLE_METRICS", false),
		},
		Mailer: Mailer{
			DefaultSender:               util.GetEnv("SERVER_MAILER_DEFAULT_SENDER", "noreply@cloudtecko.com"),
			Send:                        util.GetEnvAsBool("SERVER_MAILER_SEND", true),
			WebTemplatesEmailBaseDirAbs: util.GetEnv("SERVER_MAILER_WEB_TEMPLATES_EMAIL_BASE_DIR_ABS", filepath.Join(util.GetProjectRootDir(), "/web/templates/email")), // /app/web/templates/email
			Transporter:                 util.GetEnvEnum("SERVER_MAILER_TRANSPORTER", MailerTransporterMock.String(), []string{MailerTransporterSMTP.String(), MailerTransporterMock.String()}),
		},
		SMTP: transport.SMTPMailTransportConfig{
			Host:       util.GetEnv("SERVER_SMTP_HOST", "mailhog"),
			Port:       util.GetEnvAsInt("SERVER_SMTP_PORT", 1025),
			Username:   util.GetEnv("SERVER_SMTP_USERNAME", ""),
			Password:   util.GetEnv("SERVER_SMTP_PASSWORD", ""),
			AuthType:   transport.SMTPAuthTypeFromString(util.GetEnv("SERVER_SMTP_AUTH_TYPE", transport.SMTPAuthTypeNone.String())),
			Encryption: transport.SMTPEncryption(util.GetEnvEnum("SERVER_SMTP_ENCRYPTION", transport.SMTPEncryptionNone.String(), []string{transport.SMTPEncryptionNone.String(), transport.SMTPEncryptionTLS.String(), transport.SMTPEncryptionStartTLS.String()})),
			TLSConfig:  nil,
		},
		Frontend: FrontendServer{
			BaseURL:               util.GetEnv("SERVER_FRONTEND_BASE_URL", "http://localhost:3000"),
			PasswordResetEndpoint: util.GetEnv("SERVER_FRONTEND_PASSWORD_RESET_ENDPOINT", "/set-new-password"),
		},
		Logger: LoggerServer{
			Level:              util.LogLevelFromString(util.GetEnv("SERVER_LOGGER_LEVEL", zerolog.DebugLevel.String())),
			RequestLevel:       util.LogLevelFromString(util.GetEnv("SERVER_LOGGER_REQUEST_LEVEL", zerolog.DebugLevel.String())),
			LogRequestBody:     util.GetEnvAsBool("SERVER_LOGGER_LOG_REQUEST_BODY", false),
			LogRequestHeader:   util.GetEnvAsBool("SERVER_LOGGER_LOG_REQUEST_HEADER", false),
			LogRequestQuery:    util.GetEnvAsBool("SERVER_LOGGER_LOG_REQUEST_QUERY", false),
			LogResponseBody:    util.GetEnvAsBool("SERVER_LOGGER_LOG_RESPONSE_BODY", false),
			LogResponseHeader:  util.GetEnvAsBool("SERVER_LOGGER_LOG_RESPONSE_HEADER", false),
			LogCaller:          util.GetEnvAsBool("SERVER_LOGGER_LOG_CALLER", false),
			PrettyPrintConsole: util.GetEnvAsBool("SERVER_LOGGER_PRETTY_PRINT_CONSOLE", false),
		},
		Push: PushService{
			UseFCMProvider:  util.GetEnvAsBool("SERVER_PUSH_USE_FCM", false),
			UseMockProvider: util.GetEnvAsBool("SERVER_PUSH_USE_MOCK", true),
		},
		FCMConfig: provider.FCMConfig{
			GoogleApplicationCredentials: util.GetEnv("GOOGLE_APPLICATION_CREDENTIALS", ""),
			ProjectID:                    util.GetEnv("SERVER_FCM_PROJECT_ID", "no-fcm-project-id-set"),
			ValidateOnly:                 util.GetEnvAsBool("SERVER_FCM_VALIDATE_ONLY", true),
		},
		I18n: I18n{
			DefaultLanguage: util.GetEnvAsLanguageTag("SERVER_I18N_DEFAULT_LANGUAGE", language.English),
			BundleDirAbs:    util.GetEnv("SERVER_I18N_BUNDLE_DIR_ABS", filepath.Join(util.GetProjectRootDir(), "/web/i18n")), // /app/web/i18n
		},
	}
}

// listenAddressFromEnv resolves the address the HTTP server binds to.
//
// SERVER_ECHO_LISTEN_ADDRESS wins when set. Otherwise PORT is honoured, because
// Render, Vercel, Cloud Run, Railway and Heroku all inject it and expect the
// process to bind to it; a service that ignores PORT simply never becomes
// reachable, and the platform reports an opaque health-check timeout.
func listenAddressFromEnv() string {
	if addr := util.GetEnv("SERVER_ECHO_LISTEN_ADDRESS", ""); addr != "" {
		return addr
	}

	if port := util.GetEnv("PORT", ""); port != "" {
		return ":" + port
	}

	return ":8080"
}
