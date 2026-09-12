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
	RefreshTokenValidity               time.Duration
	PasswordResetTokenValidity         time.Duration
	PasswordResetTokenDebounceDuration time.Duration
	DefaultUserScopes                  []string
	LastAuthenticatedAtThreshold       time.Duration
	RegistrationRequiresConfirmation   bool
	ConfirmationTokenValidity          time.Duration
	ConfirmationTokenDebounceDuration  time.Duration

	// PasswordMinLength must stay in sync with the minLength in
	// api/definitions/auth.yml. The spec gives API consumers a machine-readable
	// contract; this drives the server-side blocklist checks, which the spec
	// cannot express.
	PasswordMinLength int
	PasswordMaxLength int
	// PasswordBlockedTerms are context-specific values a password must not
	// contain, on top of the user's own username. NIST calls out the service name
	// explicitly.
	PasswordBlockedTerms []string

	// LoginMaxFailedAttempts is the number of CONSECUTIVE failures an account
	// tolerates before it is throttled; 0 disables throttling.
	LoginMaxFailedAttempts int
	// LoginFailureWindow is both how long the throttle lasts and how long a
	// failure counts toward the total.
	LoginFailureWindow time.Duration

	// InvitationTokenValidity bounds how long an approved applicant has to accept.
	// Longer than a confirmation token because a human admin's approval may land
	// while the applicant is away, but still finite: an unaccepted invitation is
	// a standing capability to create an account.
	InvitationTokenValidity time.Duration
}

// Community configures the contact form and the membership waitlist.
type Community struct {
	// ContactThrottle is the per-email window inside which a second submission is
	// silently dropped. Silently, because saying "you submitted too recently"
	// would confirm that an address has used the form.
	ContactThrottle time.Duration

	// ContactRetention and ApplicationRetention set purge_after on insert. Both
	// tables hold names, email addresses and free text, which are personal data
	// under the NDPA 2023, so the schema refuses a row without an expiry.
	ContactRetention     time.Duration
	ApplicationRetention time.Duration
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
	// MembershipInvitationEndpoint is the frontend page that collects a password
	// from an approved applicant. The invitation link must point at the frontend
	// rather than the API, because accepting requires choosing a password and a
	// bare API endpoint has nowhere to type one.
	MembershipInvitationEndpoint string
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
	Storage           Storage
	Community         Community
	Payment           Payment
}

// Payment configures the payment provider.
type Payment struct {
	// UseMockProvider keeps money flows in memory. Default TRUE so a developer
	// without provider credentials still gets a working server.
	UseMockProvider bool
	// MockSecretKey signs mock webhook bodies, so tests exercise the real
	// signature-verification path rather than bypassing it.
	MockSecretKey string

	SecretKey      string
	BaseURL        string
	RequestTimeout time.Duration

	// CallbackURL is where the provider returns the payer after checkout. It is
	// a nice page, never a fulfilment trigger: the redirect is fully
	// attacker-controlled and proves nothing.
	CallbackURL string
}

// Storage configures the object store behind the media pipeline.
type Storage struct {
	// UseMockProvider keeps uploads in memory. Default TRUE so a developer with
	// no object-store credentials still gets a working server, matching how the
	// push service already behaves.
	UseMockProvider bool

	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	Region    string
	UseSSL    bool

	// PublicBaseURL must be a CDN or bucket custom domain, never this API's own
	// origin: serving user-supplied files same-origin turns any stored-content
	// flaw into a same-origin scripting problem against the API itself.
	PublicBaseURL string

	// PresignTTL bounds how long an upload URL stays valid. Short is safer -- the
	// URL is a bearer capability to write into the bucket.
	PresignTTL time.Duration

	// MaxUploadBytes caps what the pipeline will ACCEPT at completion. A
	// presigned PUT cannot enforce a size limit, so an oversized object is
	// detected after the fact, rejected, and deleted.
	MaxUploadBytes int64

	// AllowedContentTypes is an allow-list checked against the SNIFFED type, not
	// the declared one. SVG is deliberately absent: it is an XSS vector that
	// passes every MIME check because it genuinely is image/svg+xml.
	AllowedContentTypes []string

	// QuarantinePrefix and PublicPrefix separate unverified uploads from promoted
	// ones, so a bucket policy can keep the quarantine prefix unreadable.
	QuarantinePrefix string
	PublicPrefix     string
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
			AccessTokenValidity: time.Second * time.Duration(util.GetEnvAsInt("SERVER_AUTH_ACCESS_TOKEN_VALIDITY", 86400)),
			// Refresh tokens previously had no expiry column at all, so a stolen
			// one was valid forever. 30 days.
			RefreshTokenValidity:               time.Second * time.Duration(util.GetEnvAsInt("SERVER_AUTH_REFRESH_TOKEN_VALIDITY", 2592000)),
			PasswordResetTokenValidity:         time.Second * time.Duration(util.GetEnvAsInt("SERVER_AUTH_PASSWORD_RESET_TOKEN_VALIDITY", 900)),
			PasswordResetTokenDebounceDuration: time.Second * time.Duration(util.GetEnvAsInt("SERVER_AUTH_PASSWORD_RESET_TOKEN_DEBOUNCE_DURATION_SECONDS", 60)),
			DefaultUserScopes:                  util.GetEnvAsStringArr("SERVER_AUTH_DEFAULT_USER_SCOPES", []string{"app"}),
			LastAuthenticatedAtThreshold:       time.Second * time.Duration(util.GetEnvAsInt("SERVER_AUTH_LAST_AUTHENTICATED_AT_THRESHOLD", 900)),
			RegistrationRequiresConfirmation:   util.GetEnvAsBool("SERVER_AUTH_REGISTRATION_REQUIRES_CONFIRMATION", false),
			ConfirmationTokenValidity:          time.Second * time.Duration(util.GetEnvAsInt("SERVER_AUTH_CONFIRMATION_TOKEN_VALIDITY_SECONDS", 86400)),
			ConfirmationTokenDebounceDuration:  time.Second * time.Duration(util.GetEnvAsInt("SERVER_AUTH_CONFIRMATION_TOKEN_DEBOUNCE_DURATION_SECONDS", 60)),

			// 15 characters: NIST SP 800-63B Rev 4 requires 15 when a password is
			// the only authentication factor, which it is here (no MFA).
			PasswordMinLength:    util.GetEnvAsInt("SERVER_AUTH_PASSWORD_MIN_LENGTH", 15),
			PasswordMaxLength:    util.GetEnvAsInt("SERVER_AUTH_PASSWORD_MAX_LENGTH", 500),
			PasswordBlockedTerms: util.GetEnvAsStringArrTrimmed("SERVER_AUTH_PASSWORD_BLOCKED_TERMS", []string{"cloudtecko"}),

			// 10 attempts per 15 minutes. Well inside NIST's ceiling of 100
			// consecutive failures, and short enough that the lockout cannot be
			// used to keep a user out for long.
			LoginMaxFailedAttempts: util.GetEnvAsInt("SERVER_AUTH_LOGIN_MAX_FAILED_ATTEMPTS", 10),
			LoginFailureWindow:     time.Second * time.Duration(util.GetEnvAsInt("SERVER_AUTH_LOGIN_FAILURE_WINDOW_SECONDS", 900)),
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
			BaseURL:                      util.GetEnv("SERVER_FRONTEND_BASE_URL", "http://localhost:3000"),
			PasswordResetEndpoint:        util.GetEnv("SERVER_FRONTEND_PASSWORD_RESET_ENDPOINT", "/set-new-password"),
			MembershipInvitationEndpoint: util.GetEnv("SERVER_FRONTEND_MEMBERSHIP_INVITATION_ENDPOINT", "/membership/accept"),
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
		Storage: Storage{
			UseMockProvider: util.GetEnvAsBool("SERVER_STORAGE_USE_MOCK", true),
			Endpoint:        util.GetEnv("SERVER_STORAGE_ENDPOINT", ""),
			AccessKey:       util.GetEnv("SERVER_STORAGE_ACCESS_KEY", ""),
			SecretKey:       util.GetEnv("SERVER_STORAGE_SECRET_KEY", ""),
			Bucket:          util.GetEnv("SERVER_STORAGE_BUCKET", ""),
			Region:          util.GetEnv("SERVER_STORAGE_REGION", "auto"),
			UseSSL:          util.GetEnvAsBool("SERVER_STORAGE_USE_SSL", true),
			PublicBaseURL:   util.GetEnv("SERVER_STORAGE_PUBLIC_BASE_URL", "http://localhost:8080/mock-media"),
			PresignTTL:      time.Second * time.Duration(util.GetEnvAsInt("SERVER_STORAGE_PRESIGN_TTL_SECONDS", 900)),
			MaxUploadBytes:  int64(util.GetEnvAsInt("SERVER_STORAGE_MAX_UPLOAD_BYTES", 10*1024*1024)),
			AllowedContentTypes: util.GetEnvAsStringArr("SERVER_STORAGE_ALLOWED_CONTENT_TYPES", []string{
				"image/jpeg",
				"image/png",
				"image/webp",
				"image/gif",
				"application/pdf",
			}, ","),
			QuarantinePrefix: util.GetEnv("SERVER_STORAGE_QUARANTINE_PREFIX", "quarantine"),
			PublicPrefix:     util.GetEnv("SERVER_STORAGE_PUBLIC_PREFIX", "public"),
		},
		Payment: Payment{
			UseMockProvider: util.GetEnvAsBool("SERVER_PAYMENT_USE_MOCK", true),
			MockSecretKey:   util.GetEnv("SERVER_PAYMENT_MOCK_SECRET_KEY", "mock-payment-secret"),
			SecretKey:       util.GetEnv("SERVER_PAYMENT_SECRET_KEY", ""),
			BaseURL:         util.GetEnv("SERVER_PAYMENT_BASE_URL", "https://api.paystack.co"),
			RequestTimeout:  time.Second * time.Duration(util.GetEnvAsInt("SERVER_PAYMENT_REQUEST_TIMEOUT_SECONDS", 30)),
			CallbackURL:     util.GetEnv("SERVER_PAYMENT_CALLBACK_URL", "http://localhost:3000/payment/complete"),
		},
		Community: Community{
			ContactThrottle:      time.Second * time.Duration(util.GetEnvAsInt("SERVER_COMMUNITY_CONTACT_THROTTLE_SECONDS", 300)),
			ContactRetention:     time.Second * time.Duration(util.GetEnvAsInt("SERVER_COMMUNITY_CONTACT_RETENTION_SECONDS", 365*24*60*60)),
			ApplicationRetention: time.Second * time.Duration(util.GetEnvAsInt("SERVER_COMMUNITY_APPLICATION_RETENTION_SECONDS", 2*365*24*60*60)),
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
