package router

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/api"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/handlers"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/handlers/constants"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/middleware"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/router/templates"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/auth"
	"github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo/v4"
	echoMiddleware "github.com/labstack/echo/v4/middleware"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"

	// #nosec G108 - pprof handlers (conditionally made available via http.DefaultServeMux)
	"net/http/pprof"
)

func Init(s *api.Server) error {
	s.Echo = echo.New()

	viewsRenderer := &echoRenderer{
		templates: map[templates.ViewTemplate]*template.Template{},
	}

	files, err := os.ReadDir(s.Config.Echo.WebTemplatesViewsBaseDirAbs)
	if err != nil {
		return fmt.Errorf("failed to read views templates dir: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		templateName := file.Name()
		t, err := template.New(templateName).ParseGlob(filepath.Join(s.Config.Echo.WebTemplatesViewsBaseDirAbs, templateName))
		if err != nil {
			return fmt.Errorf("failed to parse template file: %w", err)
		}

		viewsRenderer.templates[templates.ViewTemplate(templateName)] = t
	}

	s.Echo.Renderer = viewsRenderer

	s.Echo.Debug = s.Config.Echo.Debug
	s.Echo.HideBanner = true
	s.Echo.Logger.SetOutput(&echoLogger{level: s.Config.Logger.RequestLevel, log: log.With().Str("component", "echo").Logger()})
	echo.NotFoundHandler = NotFoundHandler(s.Config)

	s.Echo.HTTPErrorHandler = HTTPErrorHandlerWithConfig(HTTPErrorHandlerConfig{
		HideInternalServerErrorDetails: s.Config.Echo.HideInternalServerErrorDetails,
	})

	// ---
	// General middleware
	if s.Config.Management.EnableMetrics {
		s.Echo.Use(echoprometheus.NewMiddleware(""))
		err := s.Metrics.RegisterMetrics(context.Background())
		if err != nil {
			log.Err(err).Msg("Failed to register metrics")
			return err
		}
	} else {
		log.Warn().Msg("Disabling metrics middleware due to environment config")
	}

	if s.Config.Echo.EnableTrailingSlashMiddleware {
		s.Echo.Pre(echoMiddleware.RemoveTrailingSlash())
	} else {
		log.Warn().Msg("Disabling trailing slash middleware due to environment config")
	}

	if s.Config.Echo.EnableRecoverMiddleware {
		s.Echo.Use(echoMiddleware.RecoverWithConfig(echoMiddleware.RecoverConfig{
			LogErrorFunc: middleware.LogErrorFuncWithRequestInfo,
		}))
	} else {
		log.Warn().Msg("Disabling recover middleware due to environment config")
	}

	if s.Config.Echo.EnableSecureMiddleware {
		s.Echo.Use(echoMiddleware.SecureWithConfig(echoMiddleware.SecureConfig{
			Skipper:               echoMiddleware.DefaultSecureConfig.Skipper,
			XSSProtection:         s.Config.Echo.SecureMiddleware.XSSProtection,
			ContentTypeNosniff:    s.Config.Echo.SecureMiddleware.ContentTypeNosniff,
			XFrameOptions:         s.Config.Echo.SecureMiddleware.XFrameOptions,
			HSTSMaxAge:            s.Config.Echo.SecureMiddleware.HSTSMaxAge,
			HSTSExcludeSubdomains: s.Config.Echo.SecureMiddleware.HSTSExcludeSubdomains,
			ContentSecurityPolicy: s.Config.Echo.SecureMiddleware.ContentSecurityPolicy,
			CSPReportOnly:         s.Config.Echo.SecureMiddleware.CSPReportOnly,
			HSTSPreloadEnabled:    s.Config.Echo.SecureMiddleware.HSTSPreloadEnabled,
			ReferrerPolicy:        s.Config.Echo.SecureMiddleware.ReferrerPolicy,
		}))
	} else {
		log.Warn().Msg("Disabling secure middleware due to environment config")
	}

	if s.Config.Echo.EnableRequestIDMiddleware {
		s.Echo.Use(echoMiddleware.RequestID())
	} else {
		log.Warn().Msg("Disabling request ID middleware due to environment config")
	}

	if s.Config.Echo.EnableLoggerMiddleware {
		s.Echo.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
			Level:             s.Config.Logger.RequestLevel,
			LogRequestBody:    s.Config.Logger.LogRequestBody,
			LogRequestHeader:  s.Config.Logger.LogRequestHeader,
			LogRequestQuery:   s.Config.Logger.LogRequestQuery,
			LogResponseBody:   s.Config.Logger.LogResponseBody,
			LogResponseHeader: s.Config.Logger.LogResponseHeader,
			LogCaller:         s.Config.Logger.LogCaller,
			RequestBodyLogSkipper: func(req *http.Request) bool {
				// We skip all body logging for auth endpoints as these might contain credentials
				if strings.HasPrefix(req.URL.Path, "/api/v1/auth") {
					return true
				}

				return middleware.DefaultRequestBodyLogSkipper(req)
			},
			ResponseBodyLogSkipper: func(req *http.Request, res *echo.Response) bool {
				// We skip all body logging for auth endpoints as these might contain credentials
				if strings.HasPrefix(req.URL.Path, "/api/v1/auth") {
					return true
				}

				return middleware.DefaultResponseBodyLogSkipper(req, res)
			},
			Skipper: func(c echo.Context) bool {
				// We skip logging of readiness and liveness endpoints
				switch c.Path() {
				case "/-/ready", "/-/healthy":
					return true
				}
				return false
			},
		}))
	} else {
		log.Warn().Msg("Disabling logger middleware due to environment config")
	}

	// echo's zero-config CORS() is AllowOrigins ["*"]. Require the origins to be
	// named explicitly instead: an empty list means CORS stays off rather than
	// silently allowing every origin.
	if s.Config.Echo.EnableCORSMiddleware && len(s.Config.Echo.CORSAllowOrigins) > 0 {
		s.Echo.Use(echoMiddleware.CORSWithConfig(echoMiddleware.CORSConfig{
			AllowOrigins:     s.Config.Echo.CORSAllowOrigins,
			AllowMethods:     s.Config.Echo.CORSAllowMethods,
			AllowHeaders:     s.Config.Echo.CORSAllowHeaders,
			AllowCredentials: s.Config.Echo.CORSAllowCredentials,
			MaxAge:           s.Config.Echo.CORSMaxAgeSeconds,
		}))

		// "*" with credentials is rejected by browsers and, if a proxy reflects the
		// origin, becomes a genuine cross-origin read of authenticated responses.
		for _, origin := range s.Config.Echo.CORSAllowOrigins {
			if origin == "*" && s.Config.Echo.CORSAllowCredentials {
				log.Warn().Msg("CORS is configured with wildcard origin AND credentials, which is unsafe")
			}
		}
	} else {
		log.Warn().Msg("CORS middleware disabled (set SERVER_ECHO_CORS_ALLOW_ORIGINS to enable)")
	}

	if s.Config.Echo.EnableBodyLimitMiddleware {
		s.Echo.Use(echoMiddleware.BodyLimit(s.Config.Echo.BodyLimit))
	} else {
		log.Warn().Msg("Disabling body limit middleware due to environment config")
	}

	if s.Config.Echo.EnableCacheControlMiddleware {
		s.Echo.Use(middleware.CacheControl())
	} else {
		log.Warn().Msg("Disabling cache control middleware due to environment config")
	}

	if err := attachPprofHandlers(s); err != nil {
		return err
	}

	// Add your custom / additional middlewares here.
	// see https://echo.labstack.com/middleware

	// Per-IP rate limiting for the auth endpoints. login, register and
	// forgot-password are unauthenticated and were completely unthrottled, so
	// password guessing and account enumeration had no cost at all.
	//
	// The identifier is echo's RealIP, which honours X-Forwarded-For. That is
	// correct behind Render's or Vercel's proxy; if this server is ever exposed
	// directly, set a trusted-proxy IPExtractor or the header is spoofable.
	authMiddlewares := []echo.MiddlewareFunc{}

	if s.Config.Echo.EnableRateLimitMiddleware {
		authMiddlewares = append(authMiddlewares, echoMiddleware.RateLimiterWithConfig(echoMiddleware.RateLimiterConfig{
			Store: echoMiddleware.NewRateLimiterMemoryStoreWithConfig(echoMiddleware.RateLimiterMemoryStoreConfig{
				Rate:      rate.Limit(s.Config.Echo.RateLimitRate),
				Burst:     s.Config.Echo.RateLimitBurst,
				ExpiresIn: s.Config.Echo.RateLimitExpiresIn,
			}),
		}))
	} else {
		log.Warn().Msg("Disabling rate limit middleware due to environment config")
	}

	// ---
	// Initialize our general groups and set middleware to use above them
	s.Router = &api.Router{
		Routes: nil, // will be populated by handlers.AttachAllRoutes(s)

		// Unsecured base group available at /**
		Root: s.Echo.Group(""),

		// Management endpoints, uncacheable, secured by key auth (query param), available at /-/**
		Management: s.Echo.Group("/-", echoMiddleware.KeyAuthWithConfig(echoMiddleware.KeyAuthConfig{
			KeyLookup: "query:mgmt-secret",
			Validator: func(key string, _ echo.Context) (bool, error) {
				return key == s.Config.Management.Secret, nil
			},
			Skipper: func(c echo.Context) bool {
				//nolint:gocritic
				switch c.Path() {
				case "/-/ready":
					return true
				}
				return false
			},
		}), middleware.NoCache()),

		// OAuth2, unsecured or secured by bearer auth, available at /api/v1/auth/**
		APIV1Auth: s.Echo.Group("/api/v1/auth", append(authMiddlewares, middleware.AuthWithConfig(middleware.AuthConfig{
			S:    s,
			Mode: middleware.AuthModeRequired,
			Skipper: func(c echo.Context) bool {
				switch c.Path() {
				case "/api/v1/auth/forgot-password",
					"/api/v1/auth/forgot-password/complete",
					"/api/v1/auth/login",
					"/api/v1/auth/refresh",
					"/api/v1/auth/register",
					fmt.Sprintf("/api/v1/auth/register/:%s", constants.RegistrationTokenParam):
					return true
				}
				return false
			},
		}))...),
		WellKnown: s.Echo.Group("/.well-known"),

		// Your other endpoints, typically secured by bearer auth, available at /api/v1/**
		APIV1Push: s.Echo.Group("/api/v1/push", middleware.Auth(s)),

		// Public content, available at /api/v1/** with no authentication.
		APIV1Content: s.Echo.Group("/api/v1"),

		// Admin surface at /api/v1/admin/**.
		//
		// Scopes lists "admin" and NOTHING ELSE. CheckUserScopes is OR, not AND:
		// adding auth.ScopeApp here would make every authenticated user an admin,
		// since every user carries the app scope. An empty Scopes slice is worse
		// still -- it allows everyone. This is the single most dangerous line in
		// the router.
		APIV1Admin: s.Echo.Group("/api/v1/admin", middleware.AuthWithConfig(middleware.AuthConfig{
			S:      s,
			Mode:   middleware.AuthModeRequired,
			Scopes: []string{auth.ScopeAdmin.String()},
		})),
	}

	// ---
	// Finally attach our handlers
	handlers.AttachAllRoutes(s)

	if s.Config.Management.EnableMetrics {
		// Registered on the Management group, not the root echo. Upstream attached
		// this directly to s.Echo, which put it outside the mgmt-secret key auth and
		// made every metric (including per-route traffic) world-readable whenever
		// metrics were switched on.
		log.Info().Msg("Metrics enabled and available under /-/metrics (requires mgmt-secret)")
		s.Router.Management.GET("/metrics", echoprometheus.NewHandler())
	}

	return nil
}

// attachPprofHandlers conditionally mounts the /debug/pprof endpoints.
//
// Split out of Init both to keep that function under the cyclomatic complexity
// limit and because this block carries its own security rule: pprof must never
// be served unauthenticated outside debug.
func attachPprofHandlers(s *api.Server) error {
	if !s.Config.Pprof.Enable {
		return nil
	}

	pprofAuthMiddleware := middleware.Noop()

	// Refuse to serve pprof unauthenticated outside debug. /debug/pprof exposes
	// heap and goroutine dumps, which routinely contain tokens and credentials.
	if !s.Config.Pprof.EnableManagementKeyAuth && !s.Config.Echo.Debug {
		return fmt.Errorf("refusing to start: SERVER_PPROF_ENABLE=true with SERVER_PPROF_ENABLE_MANAGEMENT_KEY_AUTH=false exposes heap and goroutine dumps unauthenticated (allowed only with SERVER_ECHO_DEBUG=true)")
	}

	if s.Config.Pprof.EnableManagementKeyAuth {
		pprofAuthMiddleware = echoMiddleware.KeyAuthWithConfig(echoMiddleware.KeyAuthConfig{
			KeyLookup: "query:mgmt-secret",
			Validator: func(key string, _ echo.Context) (bool, error) {
				return key == s.Config.Management.Secret, nil
			},
		})
	}

	// A private mux, not http.DefaultServeMux: the global mux is shared with any
	// dependency that registers on it, so wrapping it can surface handlers we
	// never intended to publish.
	pprofMux := http.NewServeMux()
	pprofMux.HandleFunc("/debug/pprof/", pprof.Index)
	pprofMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	pprofMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	pprofMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	pprofMux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	s.Echo.GET("/debug/pprof", echo.WrapHandler(http.HandlerFunc(pprof.Index)), pprofAuthMiddleware)
	s.Echo.Any("/debug/pprof/*", echo.WrapHandler(pprofMux), pprofAuthMiddleware)

	log.Warn().Bool("EnableManagementKeyAuth", s.Config.Pprof.EnableManagementKeyAuth).Msg("Pprof http handlers are available at /debug/pprof")

	if s.Config.Pprof.RuntimeBlockProfileRate != 0 {
		runtime.SetBlockProfileRate(s.Config.Pprof.RuntimeBlockProfileRate)
		log.Warn().Int("RuntimeBlockProfileRate", s.Config.Pprof.RuntimeBlockProfileRate).Msg("Pprof runtime.SetBlockProfileRate")
	}

	if s.Config.Pprof.RuntimeMutexProfileFraction != 0 {
		runtime.SetMutexProfileFraction(s.Config.Pprof.RuntimeMutexProfileFraction)
		log.Warn().Int("RuntimeMutexProfileFraction", s.Config.Pprof.RuntimeMutexProfileFraction).Msg("Pprof runtime.SetMutexProfileFraction")
	}

	return nil
}
