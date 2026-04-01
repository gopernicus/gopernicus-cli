package generators

// mainTemplate produces the app entry point: main.go
// It initializes infrastructure (logger, database, event bus, cache, storage,
// email) and starts the server.
const mainTemplate = `package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	server "{{.ModulePath}}/app/server/config"
	"github.com/gopernicus/gopernicus/infrastructure/cache"
	"github.com/gopernicus/gopernicus/infrastructure/cache/memorycache"
{{- if .HasRedis}}
	"github.com/gopernicus/gopernicus/infrastructure/cache/rediscache"
	"github.com/gopernicus/gopernicus/infrastructure/database/kvstore/goredisdb"
{{- end}}
	"github.com/gopernicus/gopernicus/infrastructure/communications/emailer"
	"github.com/gopernicus/gopernicus/infrastructure/communications/emailer/stdoutemailer"
{{- if .HasSendGrid}}
	"github.com/gopernicus/gopernicus/infrastructure/communications/emailer/sendgridemailer"
{{- end}}
	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/infrastructure/events/memorybus"
{{- if .HasRedisStreams}}
	"github.com/gopernicus/gopernicus/infrastructure/events/goredisbus"
{{- end}}
{{- if .HasStorage}}
	"github.com/gopernicus/gopernicus/infrastructure/storage"
{{- end}}
{{- if .HasStorageDisk}}
	"github.com/gopernicus/gopernicus/infrastructure/storage/diskstorage"
{{- end}}
{{- if .HasStorageGCS}}
	"github.com/gopernicus/gopernicus/infrastructure/storage/gcs"
{{- end}}
{{- if .HasStorageS3}}
	"github.com/gopernicus/gopernicus/infrastructure/storage/s3"
{{- end}}
	"github.com/gopernicus/gopernicus/infrastructure/tracing/stdouttrace"
	"github.com/gopernicus/gopernicus/sdk/environment"
	"github.com/gopernicus/gopernicus/sdk/logger"
)

var (
	build   = "develop"
	AppName = "{{.AppNameUpper}}"
)

func main() {
	if err := environment.LoadEnv(); err != nil {
		fmt.Println("warning: .env file not loaded:", err)
	}

	var logCfg logger.Options
	if err := environment.ParseEnvTags(AppName, &logCfg); err != nil {
		fmt.Println("failed to parse logger config:", err)
		os.Exit(1)
	}
	log := logger.New(logCfg)

	ctx := context.Background()

	if err := run(ctx, log); err != nil {
		log.ErrorContext(ctx, "startup", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	// =========================================================================
	// Telemetry
	// =========================================================================

	provider, err := stdouttrace.NewSimple(AppName)
	if err != nil {
		return fmt.Errorf("creating telemetry provider: %w", err)
	}
	provider.RegisterGlobal()
	defer provider.Shutdown(ctx)
	log.InfoContext(ctx, "init", "service", "telemetry")

	// =========================================================================
	// Database
	// =========================================================================

	var pgCfg pgxdb.Options
	if err := environment.ParseEnvTags(AppName, &pgCfg); err != nil {
		return fmt.Errorf("parsing postgres config: %w", err)
	}
	pool, err := pgxdb.New(pgCfg)
	if err != nil {
		return fmt.Errorf("postgres connection: %w", err)
	}
	defer func() {
		log.InfoContext(ctx, "shutdown", "status", "closing database connection")
		pool.Close()
	}()
	log.InfoContext(ctx, "init", "service", "postgres")
{{- if .HasRedis}}

	// =========================================================================
	// Redis
	// =========================================================================

	rdb, err := configRedis(AppName)
	if err != nil {
		return err
	}
	defer rdb.Close()
	log.InfoContext(ctx, "init", "service", "redis")
{{- end}}

	// =========================================================================
	// Async Patterns
	// =========================================================================
	//
	// Three patterns are available, ordered by increasing durability:
	//
	//  1. AsyncPool (server.AsyncPool) — fire-and-forget goroutines.
	//     No persistence. Use for cache invalidation, non-critical fanout,
	//     or background work that is safe to lose on crash.
	//
	//  2. innerBus — in-process pub/sub via the memory event bus.
	//     Subscribers are registered in server.New(). Events are lost on restart.
	//     Use for domain events that trigger side effects (email, audit log)
	//     where the triggering request itself is the durability guarantee.
	//
	//  3. Redis Streams bus — durable cross-process delivery via Redis Streams.
	//     At-least-once delivery using XADD/XREADGROUP/XACK. Survives restarts
	//     (messages stay in the stream). Use when events must survive a crash.
	//     Select via {{.AppNameUpper}}_EVENT_BUS_BACKEND=redis-streams.
	//
	eventBus := configEventBus(AppName, log{{- if .HasRedis}}, rdb{{- end}})
	log.InfoContext(ctx, "init", "service", "event_bus")

	// =========================================================================
	// Cache
	// =========================================================================

	cacher := configCache(AppName{{- if .HasRedis}}, rdb{{- end}})
	log.InfoContext(ctx, "init", "service", "cache")
{{- if .HasStorage}}

	// =========================================================================
	// Storage
	// =========================================================================

	fileStorer, err := configStorage(AppName, log)
	if err != nil {
		return err
	}
	log.InfoContext(ctx, "init", "service", "storage")
{{- end}}

	// =========================================================================
	// Email
	// =========================================================================

	mailer, err := configEmail(AppName, log)
	if err != nil {
		return err
	}
	log.InfoContext(ctx, "init", "service", "email")

	// =========================================================================
	// Server
	// =========================================================================

	srv, err := server.New(ctx, log,
		server.Config{
			AppName: AppName,
			Build:   build,
		},
		server.Infrastructure{
			Pool:     pool,
			Provider: provider,
			EventBus: eventBus,
			Cache:    cacher,
{{- if .HasStorage}}
			Storage:  fileStorer,
{{- end}}
			Emailer:  mailer,
		},
	)
	if err != nil {
		return err
	}

	// =========================================================================
	// Start and Wait
	// =========================================================================

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- srv.Start(ctx)
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)

	case sig := <-shutdown:
		log.InfoContext(ctx, "shutdown", "status", "shutdown started", "signal", sig)
		defer log.InfoContext(ctx, "shutdown", "status", "shutdown complete", "signal", sig)

		ctx, cancel := context.WithTimeout(ctx, srv.HTTPServer.Config.ShutdownTimeout)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			return err
		}
	}

	return nil
}

// =========================================================================
// Config helpers
// =========================================================================
{{- if .HasRedis}}

// configRedis connects to Redis.
// Reads {{.AppNameUpper}}_REDIS_ADDR, {{.AppNameUpper}}_REDIS_PASSWORD, etc.
func configRedis(envPrefix string) (*goredisdb.Client, error) {
	var cfg goredisdb.Options
	if err := environment.ParseEnvTags(envPrefix, &cfg); err != nil {
		return nil, fmt.Errorf("parsing redis config: %w", err)
	}
	rdb, err := goredisdb.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("redis connection: %w", err)
	}
	return rdb, nil
}
{{- end}}

// configEventBus returns an event bus driven by {{.AppNameUpper}}_EVENT_BUS_BACKEND.
// Values:{{- if .HasRedisStreams}} "redis-streams" (default),{{- end}} "memory".
func configEventBus(envPrefix string, log *slog.Logger{{- if .HasRedis}}, rdb *goredisdb.Client{{- end}}) events.Bus {
{{- if .HasRedisStreams}}
	if os.Getenv(envPrefix+"_EVENT_BUS_BACKEND") != "memory" {
		var cfg goredisbus.Options
		if err := environment.ParseEnvTags(envPrefix, &cfg); err != nil {
			log.Error("failed to parse redis streams config, falling back to memory bus", "error", err)
			return memorybus.New(log)
		}
		return goredisbus.New(rdb, log, cfg)
	}
{{- end}}
	return memorybus.New(log)
}

// configCache returns a cache backend driven by {{.AppNameUpper}}_CACHE_BACKEND.
// Values: "redis" (requires Redis), "memory" (default).
func configCache(envPrefix string{{- if .HasRedis}}, rdb *goredisdb.Client{{- end}}) *cache.Cache {
	backend := os.Getenv(envPrefix + "_CACHE_BACKEND")
{{- if .HasRedis}}
	if backend == "redis" {
		return cache.New(rediscache.New(rdb))
	}
{{- end}}
	return cache.New(memorycache.New(memorycache.Config{}))
}
{{- if .HasStorage}}

// configStorage returns a file storage backend driven by {{.AppNameUpper}}_STORAGE_BACKEND.
// Values: {{- if .HasStorageGCS}} "gcs",{{- end}}{{- if .HasStorageS3}} "s3",{{- end}}{{- if .HasStorageDisk}} "disk" (default){{- end}}.
func configStorage(envPrefix string, log *slog.Logger) (*storage.FileStorer, error) {
	backend := os.Getenv(envPrefix + "_STORAGE_BACKEND")
	var client storage.Client
	switch backend {
{{- if .HasStorageGCS}}
	case "gcs":
		c, err := gcs.New(
			os.Getenv(envPrefix+"_GCS_BUCKET"),
			os.Getenv(envPrefix+"_GCS_PROJECT_ID"),
			os.Getenv(envPrefix+"_GCS_SERVICE_ACCOUNT_KEY"),
		)
		if err != nil {
			return nil, fmt.Errorf("gcs client: %w", err)
		}
		client = c
{{- end}}
{{- if .HasStorageS3}}
	case "s3":
		client = s3.New(
			os.Getenv(envPrefix+"_S3_BUCKET"),
			os.Getenv(envPrefix+"_S3_REGION"),
			os.Getenv(envPrefix+"_S3_ACCESS_KEY_ID"),
			os.Getenv(envPrefix+"_S3_SECRET_ACCESS_KEY"),
			os.Getenv(envPrefix+"_S3_ENDPOINT"),
		)
{{- end}}
{{- if .HasStorageDisk}}
	default:
		client = diskstorage.New(os.Getenv(envPrefix + "_STORAGE_DISK_PATH"))
{{- else}}
	default:
		return nil, fmt.Errorf("unsupported storage backend: %q", backend)
{{- end}}
	}
	return storage.New(client, storage.WithLogger(log)), nil
}
{{- end}}

// configEmail returns an email backend driven by {{.AppNameUpper}}_EMAIL_BACKEND.
// Values: {{- if .HasSendGrid}}"sendgrid" (default),{{- end}} "stdout" (logs emails, never delivers).
func configEmail(envPrefix string, log *slog.Logger) (*emailer.Emailer, error) {
	defaultFrom := os.Getenv(envPrefix + "_EMAIL_FROM")
{{- if .HasSendGrid}}
	if os.Getenv(envPrefix+"_EMAIL_BACKEND") == "sendgrid" {
		client := sendgridemailer.New(
			os.Getenv(envPrefix+"_SENDGRID_API_KEY"),
			defaultFrom,
			os.Getenv(envPrefix+"_EMAIL_FROM_NAME"),
		)
		return emailer.New(client, defaultFrom, emailer.WithLogger(log))
	}
{{- end}}
	return emailer.New(stdoutemailer.New(log), defaultFrom, emailer.WithLogger(log))
}
`

// serverTemplate produces the server wiring: app/server/config/server.go
// This file handles DI, middleware stack, and route registration.
const serverTemplate = `package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gopernicus/gopernicus/bridge/transit/httpmid"
	"github.com/gopernicus/gopernicus/telemetry"
{{- if .HasAuthentication}}
	"github.com/gopernicus/gopernicus/core/auth/authentication"
{{- end}}
{{- if .HasAuthorization}}
	"github.com/gopernicus/gopernicus/core/auth/authorization"
{{- end}}
	"github.com/gopernicus/gopernicus/infrastructure/cache"
	"github.com/gopernicus/gopernicus/infrastructure/communications/emailer"
{{- if .HasAuthentication}}
	"github.com/gopernicus/gopernicus/infrastructure/cryptids/bcrypt"
	"github.com/gopernicus/gopernicus/infrastructure/cryptids/golangjwt"
{{- end}}
	"github.com/gopernicus/gopernicus/infrastructure/database/postgres/pgxdb"
	"github.com/gopernicus/gopernicus/infrastructure/events"
	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter"
	"github.com/gopernicus/gopernicus/infrastructure/ratelimiter/memorylimiter"
{{- if .HasStorage}}
	"github.com/gopernicus/gopernicus/infrastructure/storage"
{{- end}}
	"github.com/gopernicus/gopernicus/sdk/async"
	"github.com/gopernicus/gopernicus/sdk/environment"
	"github.com/gopernicus/gopernicus/sdk/web"
{{- if or .HasAuthentication .HasAuthorization}}

{{- if .HasAuthentication}}
	"{{.ModulePath}}/bridge/repositories/authreposbridge"
{{- end}}
{{- if .HasAuthorization}}
	"{{.ModulePath}}/bridge/repositories/rebacreposbridge"
{{- end}}
{{- if .HasTenancy}}
	"{{.ModulePath}}/bridge/repositories/tenancyreposbridge"
{{- end}}
{{- if .HasAuthentication}}
	"{{.ModulePath}}/core/repositories/auth"
{{- end}}
{{- if .HasAuthorization}}
	"{{.ModulePath}}/core/repositories/rebac"
{{- end}}
{{- if .HasTenancy}}
	"{{.ModulePath}}/core/repositories/tenancy"
{{- end}}
{{- if .HasAuthentication}}
	authenticationsatisfiers "{{.ModulePath}}/core/auth/authentication/satisfiers"
{{- end}}
{{- if .HasAuthorization}}
	authorizationsatisfiers "{{.ModulePath}}/core/auth/authorization/satisfiers"
{{- end}}
{{- end}}
)

// Server holds all dependencies and provides methods to start/stop the API.
type Server struct {
	Log        *slog.Logger
	Pool       *pgxdb.Pool
	Provider   *telemetry.Provider
	Handler    *web.WebHandler
	HTTPServer *web.WebServer
	EventBus   events.Bus
	AsyncPool  *async.Pool
{{- if .HasStorage}}
	Storage    *storage.FileStorer
{{- end}}
	Emailer    *emailer.Emailer
}

// Config holds application metadata.
type Config struct {
	AppName string
	Build   string
}

// Infrastructure holds external dependencies that must be injected.
// These are resources that tests need to swap (testcontainers, mocks).
type Infrastructure struct {
	Pool     *pgxdb.Pool         // required: database connection
	Provider *telemetry.Provider // optional: nil disables tracing
	EventBus events.Bus          // optional: nil disables async events
	Cache    *cache.Cache        // optional: nil disables caching
{{- if .HasStorage}}
	Storage  *storage.FileStorer // optional: nil disables file storage
{{- end}}
	Emailer  *emailer.Emailer    // optional: nil disables email sending
}

// New creates a new Server with all dependencies initialized.
// Configuration is read from environment variables using the AppName prefix.
// Infrastructure must be provided as it represents external dependencies.
func New(ctx context.Context, log *slog.Logger, cfg Config, infra Infrastructure) (*Server, error) {
	log.InfoContext(ctx, "startup", "build", cfg.Build)

	bus := infra.EventBus
	cacher := infra.Cache

	// =========================================================================
	// Async Pool
	// =========================================================================

	// Shared bounded goroutine pool for fire-and-forget tasks (cache invalidation, etc.).
	// Initialized here so it can be passed to downstream services and drained during shutdown.
	asyncPool := async.NewPool(
		async.WithMaxConcurrency(50),
		async.WithDropOnFull(true),
		async.WithLogger(log),
	)
	log.InfoContext(ctx, "init", "service", "async_pool")

	// =========================================================================
	// Rate Limiter
	// =========================================================================

	rateLimiter := ratelimiter.New(memorylimiter.New(), ratelimiter.NewDefaultResolver(), ratelimiter.WithLogger(log))
	log.InfoContext(ctx, "init", "service", "rate_limiter")

	// =========================================================================
	// Repositories
	// =========================================================================
{{- if .HasAuthentication}}

	authRepos := auth.NewRepositories(log, infra.Pool, cacher, bus)
	log.InfoContext(ctx, "init", "service", "auth_repos")
{{- end}}
{{- if .HasAuthorization}}

	rebacRepos := rebac.NewRepositories(log, infra.Pool, cacher, bus)
	log.InfoContext(ctx, "init", "service", "rebac_repos")
{{- end}}
{{- if .HasTenancy}}

	tenancyRepos := tenancy.NewRepositories(log, infra.Pool, cacher, bus)
	log.InfoContext(ctx, "init", "service", "tenancy_repos")
{{- end}}
{{- if not (or .HasAuthentication .HasAuthorization .HasTenancy)}}

	// TODO: Wire your domain repositories here.
	// Example:
	//   repos := mydomain.NewRepositories(log, infra.Pool, cacher, bus)

	_, _ = cacher, bus // remove once used
{{- end}}
{{- if .HasAuthorization}}

	// =========================================================================
	// Authorization
	// =========================================================================

	// Compose schema from all domains (auth schema defined in bridge packages).
	authorizationSchema := authorization.NewSchema(
{{- if .HasAuthentication}}
		authreposbridge.AuthSchema(),
{{- end}}
		rebacreposbridge.AuthSchema(),
	)

	// Build store chain: repo store → cache store.
	authorizationStore := authorization.NewCacheStore(
		authorizationsatisfiers.NewAuthorizationStoreSatisfier(rebacRepos.RebacRelationship),
		cacher,
	)

	var authorizationCfg authorization.Config
	if err := environment.ParseEnvTags(cfg.AppName, &authorizationCfg); err != nil {
		return nil, fmt.Errorf("parsing authorization config: %w", err)
	}
	authorizer := authorization.NewAuthorizer(authorizationStore, authorizationSchema, authorizationCfg)
	log.InfoContext(ctx, "init", "service", "authorization")
{{- end}}
{{- if .HasAuthentication}}

	// =========================================================================
	// Authentication
	// =========================================================================

	var bcryptOpts bcrypt.Options
	if err := environment.ParseEnvTags(cfg.AppName, &bcryptOpts); err != nil {
		return nil, fmt.Errorf("parsing bcrypt config: %w", err)
	}
	hasher := bcrypt.New(bcryptOpts)

	var jwtOpts golangjwt.Options
	if err := environment.ParseEnvTags(cfg.AppName, &jwtOpts); err != nil {
		return nil, fmt.Errorf("parsing jwt config: %w", err)
	}
	signer, err := golangjwt.New(jwtOpts)
	if err != nil {
		return nil, fmt.Errorf("creating jwt signer: %w", err)
	}

	var authCfg authentication.Config
	if err := environment.ParseEnvTags(cfg.AppName, &authCfg); err != nil {
		return nil, fmt.Errorf("parsing authentication config: %w", err)
	}

	authenticator := authentication.NewAuthenticator(
		"authentication",
		authentication.NewRepositories(
			authenticationsatisfiers.NewUserSatisfier(authRepos.User),
			authenticationsatisfiers.NewPasswordSatisfier(authRepos.UserPassword),
			authenticationsatisfiers.NewSessionSatisfier(authRepos.Session),
			authenticationsatisfiers.NewVerificationTokenSatisfier(authRepos.VerificationToken),
			authenticationsatisfiers.NewVerificationCodeSatisfier(authRepos.VerificationCode),
		),
		hasher, signer, bus, authCfg,
	)
	log.InfoContext(ctx, "init", "service", "authentication")
{{- end}}

	// =========================================================================
	// Web Handler
	// =========================================================================

	webHandler := web.NewWebHandler(
		web.WithLogging(log),
	)

	// Global middleware stack — applied to every request.
	webHandler.Use(
		httpmid.Panics(log),
		httpmid.TelemetryMiddleware(infra.Provider.Tracer()),
		httpmid.TrustProxies(0),
		httpmid.ClientInfo(),
		httpmid.Logger(log),
	)

	// =========================================================================
	// Routes
	// =========================================================================

	// Health check
	webHandler.Handle("GET", "/healthz", func(w http.ResponseWriter, r *http.Request) {
		web.RespondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	api := webHandler.Group("/api/v1")
{{- if .HasAuthentication}}

	authBridges := authreposbridge.NewBridges(log, authRepos, rateLimiter, authenticator{{- if .HasAuthorization}}, authorizer{{- end}})
	authBridges.AddHttpRoutes(api)
{{- end}}
{{- if .HasAuthorization}}

	rebacBridges := rebacreposbridge.NewBridges(log, rebacRepos, rateLimiter{{- if .HasAuthentication}}, authenticator, authorizer{{- end}})
	rebacBridges.AddHttpRoutes(api)
{{- end}}
{{- if .HasTenancy}}

	tenancyBridges := tenancyreposbridge.NewBridges(log, tenancyRepos, rateLimiter{{- if .HasAuthentication}}, authenticator{{- end}}{{- if .HasAuthorization}}, authorizer{{- end}})
	tenancyBridges.AddHttpRoutes(api)
{{- end}}
{{- if not (or .HasAuthentication .HasAuthorization .HasTenancy)}}

	// TODO: Register your domain routes here.
	// Example:
	//   bridges := mydomainreposbridge.NewBridges(log, repos, rateLimiter)
	//   bridges.AddHttpRoutes(api)

	_, _ = rateLimiter, api
{{- end}}

	log.InfoContext(ctx, "init", "service", "routes")

	// =========================================================================
	// Cases (use case operations)
	// =========================================================================
	//
	// Wire use cases here. Cases orchestrate business logic across repositories,
	// authorization, and events. They are hand-written, not generated.
	//
	// Example:
	//   myCase := mycase.New(log, bus)
	//   myBridge := mycasebridge.New(log, myCase, rateLimiter)
	//   cases := api.Group("/cases")
	//   myBridge.AddHttpRoutes(cases)

	// =========================================================================
	// OpenAPI
	// =========================================================================

	webHandler.ServeOpenAPI("/openapi.json", web.OpenAPIInfo{
		Title:   "{{.ProjectName}} API",
		Version: "1.0.0",
	},
{{- if .HasAuthentication}}
		authBridges.OpenAPISpec(),
{{- end}}
{{- if .HasAuthorization}}
		rebacBridges.OpenAPISpec(),
{{- end}}
	)
	log.InfoContext(ctx, "init", "service", "openapi", "path", "/openapi.json")

	// =========================================================================
	// HTTP Server
	// =========================================================================

	var serverCfg web.ServerConfig
	if err := environment.ParseEnvTags(cfg.AppName, &serverCfg); err != nil {
		return nil, fmt.Errorf("parsing server config: %w", err)
	}
	httpServer := web.NewServer(serverCfg, web.WithHandler(webHandler))
	log.InfoContext(ctx, "init", "service", "http_server", "host", serverCfg.Address())

	return &Server{
		Log:        log,
		Pool:       infra.Pool,
		Provider:   infra.Provider,
		Handler:    webHandler,
		HTTPServer: httpServer,
		EventBus:   bus,
		AsyncPool:  asyncPool,
{{- if .HasStorage}}
		Storage:    infra.Storage,
{{- end}}
		Emailer:    infra.Emailer,
	}, nil
}

// Start begins listening for HTTP requests. This is a blocking call.
func (s *Server) Start(ctx context.Context) error {
	s.Log.InfoContext(ctx, "startup", "status", "api router started", "host", s.HTTPServer.Config.Address())
	return s.HTTPServer.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.Log.InfoContext(ctx, "shutdown", "status", "shutdown started")

	// 1. Stop accepting new traffic first — waits for all handlers to return.
	if err := s.HTTPServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("http server shutdown: %w", err)
	}

	// 2. Drain the async pool now that no new tasks can be submitted.
	if err := s.AsyncPool.Close(ctx); err != nil {
		s.Log.WarnContext(ctx, "shutdown", "component", "async_pool", "error", err)
	}

	// 3. Close the event bus.
	if s.EventBus != nil {
		if err := s.EventBus.Close(ctx); err != nil {
			s.Log.WarnContext(ctx, "shutdown", "component", "events", "error", err)
		}
	}

	// 4. Flush telemetry spans.
	if s.Provider != nil {
		if err := s.Provider.Shutdown(ctx); err != nil {
			s.Log.WarnContext(ctx, "shutdown", "component", "telemetry", "error", err)
		}
	}

	s.Log.InfoContext(ctx, "shutdown", "status", "shutdown complete")
	return nil
}
`

// envExampleTemplate produces a .env.example with sensible defaults.
const envExampleTemplate = `# {{.AppNameUpper}} Configuration
# Copy this file to .env and fill in your values:
#   cp .env.example .env

# ── Database ──────────────────────────────────────────────────────────────────
{{.AppNameUpper}}_DB_DATABASE_URL=postgres://postgres:postgres@localhost:5432/{{.ProjectName}}?sslmode=disable
{{.AppNameUpper}}_DB_MAX_CONNS=25
{{.AppNameUpper}}_DB_MIN_CONNS=5

# ── Server ────────────────────────────────────────────────────────────────────
{{.AppNameUpper}}_HOST=0.0.0.0
{{.AppNameUpper}}_PORT=3000
{{.AppNameUpper}}_SHUTDOWN_TIMEOUT=5s

# ── Logging ───────────────────────────────────────────────────────────────────
{{.AppNameUpper}}_LOG_LEVEL=DEBUG
{{.AppNameUpper}}_LOG_FORMAT=text
{{- if .HasAuthentication}}

# ── Authentication ────────────────────────────────────────────────────────────
{{.AppNameUpper}}_JWT_SECRET=change-me-to-at-least-32-bytes!!
{{.AppNameUpper}}_BCRYPT_COST=10
{{- end}}
{{- if .HasRedis}}

# ── Redis ─────────────────────────────────────────────────────────────────────
{{.AppNameUpper}}_REDIS_ADDR=localhost:6379
{{.AppNameUpper}}_REDIS_PASSWORD=
{{.AppNameUpper}}_REDIS_POOL_SIZE=10
{{- end}}

# ── Cache ─────────────────────────────────────────────────────────────────────
# Values: {{if .HasRedis}}redis (default), {{end}}memory
{{.AppNameUpper}}_CACHE_BACKEND={{if .HasRedis}}redis{{else}}memory{{end}}
{{- if .HasStorage}}

# ── File Storage ──────────────────────────────────────────────────────────────
# Switch at runtime — all configured backends are scaffolded below.
# Values:{{if .HasStorageDisk}} disk,{{end}}{{if .HasStorageGCS}} gcs,{{end}}{{if .HasStorageS3}} s3{{end}}
{{.AppNameUpper}}_STORAGE_BACKEND={{if .HasStorageDisk}}disk{{else if .HasStorageGCS}}gcs{{else}}s3{{end}}
{{if .HasStorageDisk}}# Disk backend:
{{.AppNameUpper}}_STORAGE_DISK_PATH=./data/uploads
{{end}}{{if .HasStorageGCS}}# GCS backend:
{{.AppNameUpper}}_GCS_BUCKET=
{{.AppNameUpper}}_GCS_PROJECT_ID=
{{.AppNameUpper}}_GCS_SERVICE_ACCOUNT_KEY=
{{end}}{{if .HasStorageS3}}# S3 backend:
{{.AppNameUpper}}_S3_BUCKET=
{{.AppNameUpper}}_S3_REGION=us-east-1
{{.AppNameUpper}}_S3_ACCESS_KEY_ID=
{{.AppNameUpper}}_S3_SECRET_ACCESS_KEY=
{{.AppNameUpper}}_S3_ENDPOINT=
{{end}}{{- end}}

# ── Email ─────────────────────────────────────────────────────────────────────
# Values: {{if .HasSendGrid}}sendgrid (default), {{end}}stdout (logs emails, never delivers)
{{.AppNameUpper}}_EMAIL_BACKEND={{if .HasSendGrid}}sendgrid{{else}}stdout{{end}}
{{.AppNameUpper}}_EMAIL_FROM=noreply@example.com
{{.AppNameUpper}}_EMAIL_FROM_NAME={{.ProjectName}}
{{- if .HasSendGrid}}
{{.AppNameUpper}}_SENDGRID_API_KEY=
{{- end}}
{{- if .HasRedisStreams}}

# ── Event Bus ─────────────────────────────────────────────────────────────────
# Values: redis-streams (default), memory
{{.AppNameUpper}}_EVENT_BUS_BACKEND=redis-streams
{{.AppNameUpper}}_EVENT_BUS_STREAM_PREFIX=events:
{{.AppNameUpper}}_EVENT_BUS_CONSUMER_GROUP={{.ProjectName}}
{{.AppNameUpper}}_EVENT_BUS_WORKERS=4
{{.AppNameUpper}}_EVENT_BUS_BATCH_SIZE=10
{{.AppNameUpper}}_EVENT_BUS_BLOCK_TIMEOUT=5s
{{- end}}
`

// dockerComposeTemplate produces a docker-compose.yml for local development.
// Written to workshop/dev/docker-compose.yml.
const dockerComposeTemplate = `name: {{.ProjectName}}

services:
  postgres:
    image: postgres:17-alpine
    restart: unless-stopped
    command: postgres -c ssl=off
    environment:
      POSTGRES_DB: {{.ProjectName}}
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: postgres
      PGDATA: /data/postgres
    ports:
      - "5432:5432"
    volumes:
      - ./data/postgres-data:/data/postgres
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      timeout: 5s
      retries: 5
      start_period: 10s
{{- if .HasRedis}}

  redis:
    image: redis:7-alpine
    restart: unless-stopped
    command: redis-server --appendonly yes
    ports:
      - "6379:6379"
    volumes:
      - ./data/redis-data:/data
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 5s
      retries: 5
      start_period: 5s
{{- end}}
{{- if .HasTelemetry}}

  jaeger:
    image: jaegertracing/all-in-one:1.54
    restart: unless-stopped
    ports:
      - "16686:16686" # Jaeger UI
      - "4317:4317"   # OTLP gRPC
      - "4318:4318"   # OTLP HTTP
    environment:
      - COLLECTOR_OTLP_ENABLED=true
{{- end}}
`

// makefileTemplate produces a Makefile with standard development targets.
const makefileTemplate = `BINARY    := {{.ProjectName}}
COMPOSE   := docker compose -f workshop/dev/docker-compose.yml
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
IMAGE     := {{.ProjectName}}
IMAGE_TAG := $(IMAGE):$(VERSION)

# ── Application ──────────────────────────────────────────────────────────────

.PHONY: dev
dev: dev-up ## Start development server with hot reload (air)
	air

.PHONY: run
run: ## Start the application server
	go run ./app/server

.PHONY: build
build: ## Build the server binary
	go build -o bin/$(BINARY) ./app/server

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin/

.PHONY: fmt
fmt: ## Format Go source files
	go fmt ./...

.PHONY: tidy
tidy: ## Tidy and verify Go modules
	go mod tidy && go mod verify

# ── Docker Build ─────────────────────────────────────────────────────────────

.PHONY: build-docker
build-docker: ## Build the Docker image
	docker build \
		-f workshop/docker/dockerfile.{{.ProjectName}} \
		-t $(IMAGE_TAG) \
		-t $(IMAGE):latest \
		--build-arg BUILD_REF=$(VERSION) \
		--build-arg BUILD_DATE=$(shell date -u +"%Y-%m-%dT%H:%M:%SZ") \
		.

.PHONY: run-docker
run-docker: ## Run the API in a Docker container
	@docker stop $(BINARY) 2>/dev/null || true
	@docker rm $(BINARY) 2>/dev/null || true
	docker run -d \
		--name $(BINARY) \
		-p 3000:3000 \
		--env-file .env \
		$(IMAGE):latest

.PHONY: stop-docker
stop-docker: ## Stop and remove the Docker container
	@docker stop $(BINARY) 2>/dev/null || true
	@docker rm $(BINARY) 2>/dev/null || true

.PHONY: logs-docker
logs-docker: ## Tail the Docker container logs
	docker logs -f $(BINARY)

# ── Development Infrastructure ───────────────────────────────────────────────

.PHONY: dev-up
dev-up: ## Start development infrastructure (postgres{{- if .HasRedis}}, redis{{- end}}{{- if .HasTelemetry}}, jaeger{{- end}})
	$(COMPOSE) up -d

.PHONY: dev-down
dev-down: ## Stop development infrastructure
	$(COMPOSE) down

.PHONY: dev-logs
dev-logs: ## Tail logs from all dev services
	$(COMPOSE) logs -f

.PHONY: dev-ps
dev-ps: ## Show status of dev services
	$(COMPOSE) ps

.PHONY: dev-reset
dev-reset: ## Nuclear reset: stop, wipe volumes, restart
	$(COMPOSE) down -v
	$(COMPOSE) up -d

.PHONY: dev-psql
dev-psql: ## Open a psql shell in the dev database
	$(COMPOSE) exec postgres psql -U postgres -d {{.ProjectName}}

# ── Tests ─────────────────────────────────────────────────────────────────────

.PHONY: test
test: ## Run unit tests
	go test ./...

.PHONY: test-integration
test-integration: ## Run integration tests (requires running DB)
	go test -tags=integration ./...

.PHONY: test-e2e
test-e2e: ## Run end-to-end tests (requires running server)
	go test -tags=e2e ./workshop/testing/e2e/...

# ── Help ──────────────────────────────────────────────────────────────────────

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
`

// documentationREADMETemplate produces the workshop/documentation/README.md index.
const documentationREADMETemplate = `# {{.ProjectName}} Documentation

Project documentation for {{.ProjectName}}, built with [gopernicus](https://github.com/gopernicus/gopernicus).

## Architecture

- [Overview](architecture/overview.md) — System architecture and layer responsibilities
- [Design Philosophy](architecture/design-philosophy.md) — Principles and trade-offs

## Guides

- [Adding a New Entity](guides/adding-new-entity.md) — From SQL table to full CRUD
- [Adding a Use Case](guides/adding-use-case.md) — Complex operations beyond CRUD
- [Adding Auth to an Entity](guides/adding-auth-to-entity.md) — ReBAC authorization setup

## Code Generation

- [Query Annotations](generators/query-annotations.md) — @func, @filter, @order, @fields, etc.
- [bridge.yml Reference](generators/bridge-yml.md) — Routes, middleware, auth schema
- [Generated File Map](generators/generated-file-map.md) — Which files are generated vs bootstrap

## Infrastructure

- [Database](infrastructure/database.md) — PostgreSQL, migrations, transactions
- [Events](infrastructure/events.md) — Domain events and the outbox pattern
- [Caching](infrastructure/caching.md) — Cache-aside with invalidation

## Deployment

- [Docker](deployment/docker.md) — Building and running containers
- [Environment Variables](deployment/environment.md) — Configuration reference
`

// documentationArchOverviewTemplate produces the architecture overview stub.
const documentationArchOverviewTemplate = `# Architecture Overview

{{.ProjectName}} follows a hexagonal (ports & adapters) architecture generated by gopernicus.

## Layers

| Layer | Location | Responsibility |
|-------|----------|----------------|
| App | ` + "`app/server/`" + ` | Server bootstrap, dependency wiring, configuration |
| Bridge | ` + "`bridge/`" + ` | HTTP handlers, request/response mapping, middleware |
| Core | ` + "`core/`" + ` | Domain logic: repositories, cases, events |
| Infrastructure | ` + "`(gopernicus module)`" + ` | Database, cache, email, storage adapters |
| SDK | ` + "`(gopernicus module)`" + ` | Shared utilities: errors, validation, web framework |

## Data Flow

` + "```" + `
HTTP Request
  → Bridge (parse, validate, authenticate, authorize)
    → Core Repository or Case (business logic)
      → Store (SQL execution via pgx)
    ← Domain types returned
  ← Bridge (serialize response)
HTTP Response
` + "```" + `

## Key Conventions

- **Accept interfaces, return structs** — dependency injection at every boundary
- **queries.sql** is the source of truth for data access; **bridge.yml** for HTTP config
- **generated.go** files are always overwritten; all other files are yours to customize
- **Storer interface** in repository.go uses markers for regeneration; custom methods go above the markers
`

// documentationDeployDockerTemplate produces the deployment/docker.md stub.
const documentationDeployDockerTemplate = `# Docker

## Building

` + "```bash" + `
make build-docker
` + "```" + `

This builds a multi-stage Docker image from ` + "`workshop/docker/dockerfile.{{.ProjectName}}`" + `.

- **Stage 1**: Compiles the Go binary with ` + "`CGO_ENABLED=0`" + ` for a static binary
- **Stage 2**: Copies the binary into an Alpine runtime image with a non-root user

## Running

` + "```bash" + `
make run-docker      # Start container (reads .env for config)
make stop-docker     # Stop and remove container
make logs-docker     # Tail container logs
` + "```" + `

The container exposes port 3000 and includes a healthcheck on ` + "`/healthz`" + `.

## Environment Variables

Pass environment variables via ` + "`--env-file .env`" + ` or individual ` + "`-e`" + ` flags.
See ` + "`.env.example`" + ` for the full list of configuration options.
`

// dockerfileTemplate produces a multi-stage Dockerfile for production builds.
// Written to workshop/docker/dockerfile.<project-name>.
const dockerfileTemplate = `# Production Dockerfile for {{.ProjectName}}

# ============================================
# Stage 1: Build Go Binary
# ============================================
FROM golang:1.26 AS go-build
ENV CGO_ENABLED=0
ARG BUILD_REF

COPY . /applications
WORKDIR /applications/app/server
RUN go build -ldflags "-X main.build=${BUILD_REF}"

# ============================================
# Stage 2: Production Runtime
# ============================================
FROM alpine:3.21 AS runner
ARG BUILD_DATE
ARG BUILD_REF

RUN apk --no-cache add ca-certificates

RUN addgroup -g 1000 -S appsuser && \
    adduser -u 1000 -h /applications -G appsuser -S appsuser

WORKDIR /applications

COPY --from=go-build --chown=appsuser:appsuser /applications/app/server/server /applications/server

USER appsuser

EXPOSE 3000

CMD ["./server"]

LABEL org.opencontainers.image.created="${BUILD_DATE}" \
    org.opencontainers.image.title="{{.ProjectName}}" \
    org.opencontainers.image.revision="${BUILD_REF}"
`

// emailsTemplate produces app/server/emails/emails.go — the app-level email
// configuration: layouts, content templates, and branding.
const emailsTemplate = `// Package emails provides app-level email configuration, templates, and branding.
// These override the infrastructure defaults with your project's copy and design.
package emails

import (
	"embed"

	"github.com/gopernicus/gopernicus/infrastructure/communications/emailer"
{{- if .HasAuthentication}}
	authbridge "github.com/gopernicus/gopernicus/bridge/auth/authentication"
{{- end}}
)

// Layouts contains app-specific email layout templates (layouts/transactional.html, etc.).
// These override the generic infrastructure layouts with your branding.
//
//go:embed layouts/*
var Layouts embed.FS

// Templates contains app-level email content templates organised by domain.
// These override the framework defaults — edit them to match your app's copy and style.
//
//go:embed templates/*
var Templates embed.FS

// NewBranding returns the branding configuration for this app.
// Customize Name, LogoURL, Address, and SocialLinks for your production environment.
// Templates access branding via .Brand.Name, .Brand.Tagline, .Brand.LogoURL, etc.
func NewBranding() *emailer.Branding {
	return &emailer.Branding{
		Name:    "{{.ProjectName}}",
		Tagline: "Your Platform",
		// LogoURL: "https://example.com/logo.png",
		// Address: "123 Main St, City, State 12345",
		// SocialLinks: []emailer.SocialLink{
		// 	{Name: "Twitter", URL: "https://twitter.com/yourapp"},
		// },
	}
}

// Options returns all emailer options for app-level email setup.
// Pass these to emailer.New() in main.go:
//
//	mailer, err := emailer.New(log, client, defaultFrom, emails.Options()...)
func Options() []emailer.Option {
	return []emailer.Option{
{{- if .HasAuthentication}}
		// Authentication emails — framework defaults at LayerCore, app overrides at LayerApp.
		emailer.WithContentTemplates("authentication", authbridge.AuthTemplates(), emailer.LayerCore),
		emailer.WithContentTemplates("authentication", Templates, emailer.LayerApp),
{{- end}}

		// Branded layouts override the infrastructure defaults.
		emailer.WithLayouts(Layouts, "layouts", emailer.LayerApp),

		emailer.WithBranding(NewBranding()),
	}
}
`

// emailLayoutHTMLRaw is the content written as-is to
// app/server/emails/layouts/transactional.html.
// It contains emailer template syntax ({{define}}, {{.Brand.*}}, {{.Content}})
// and must NOT be processed through text/template.
const emailLayoutHTMLRaw = `{{define "layout:transactional"}}
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <meta http-equiv="X-UA-Compatible" content="IE=edge">
    <title>{{.Subject}}</title>
</head>
<body style="margin: 0; padding: 0; background-color: #f4f4f4; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;">
    <table role="presentation" cellspacing="0" cellpadding="0" border="0" width="100%" style="background-color: #f4f4f4;">
        <tr>
            <td align="center" style="padding: 20px 0;">
                <table role="presentation" cellspacing="0" cellpadding="0" border="0" width="600" style="max-width: 600px; background-color: #ffffff; border-radius: 8px; box-shadow: 0 2px 8px rgba(0,0,0,0.1);">

                    <!-- Header -->
                    <tr>
                        <td style="background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); padding: 30px; text-align: center; border-radius: 8px 8px 0 0;">
                            {{if .Brand.LogoURL}}
                            <img src="{{.Brand.LogoURL}}" alt="{{.Brand.Name}}" style="max-height: 40px; margin-bottom: 12px;">
                            {{end}}
                            <h1 style="margin: 0; color: #ffffff; font-size: 24px; font-weight: 700;">
                                {{if .Brand.Name}}{{.Brand.Name}}{{else}}Gopernicus{{end}}
                            </h1>
                            {{if .Brand.Tagline}}
                            <p style="margin: 8px 0 0 0; color: #e0e7ff; font-size: 14px;">{{.Brand.Tagline}}</p>
                            {{end}}
                        </td>
                    </tr>

                    <!-- Content -->
                    <tr>
                        <td style="padding: 40px 30px;">
                            {{.Content}}
                        </td>
                    </tr>

                    <!-- Footer -->
                    <tr>
                        <td style="background-color: #1f2937; padding: 24px 30px; text-align: center; border-radius: 0 0 8px 8px;">
                            {{if .Brand.SocialLinks}}
                            <table role="presentation" cellspacing="0" cellpadding="0" border="0" style="margin: 0 auto 16px auto;">
                                <tr>
                                    {{range $i, $link := .Brand.SocialLinks}}
                                    {{if $i}}<td style="padding: 0 8px; color: #4b5563;">|</td>{{end}}
                                    <td style="padding: 0 8px;">
                                        <a href="{{$link.URL}}" style="color: #9ca3af; text-decoration: none; font-size: 14px;">{{$link.Name}}</a>
                                    </td>
                                    {{end}}
                                </tr>
                            </table>
                            {{end}}
                            <p style="margin: 0; color: #6b7280; font-size: 12px;">
                                {{if .Brand.Address}}{{.Brand.Address}}<br>{{end}}
                                This is an automated message. Please do not reply directly to this email.
                            </p>
                        </td>
                    </tr>

                </table>
            </td>
        </tr>
    </table>
</body>
</html>
{{end}}
`

// emailLayoutTXTRaw is the content written as-is to
// app/server/emails/layouts/transactional.txt.
const emailLayoutTXTRaw = `{{define "layout:transactional.text"}}
{{if .Brand.Name}}{{.Brand.Name}}{{else}}Gopernicus{{end}}
{{if .Brand.Tagline}}{{.Brand.Tagline}}{{end}}
================================================================================

{{.Content}}

--------------------------------------------------------------------------------
{{if .Brand.Address}}{{.Brand.Address}}{{end}}
This is an automated message. Please do not reply directly to this email.
{{end}}
`

// authVerificationHTMLRaw is written to templates/authentication/verification.html.
// App-level override for the verification code email — edit to match your copy and style.
const authVerificationHTMLRaw = `{{define "authentication:verification"}}
<h2 style="margin: 0 0 16px 0; color: #1f2937; font-size: 22px;">Verify your email</h2>
{{if .DisplayName}}<p style="color: #374151;">Hi {{.DisplayName}},</p>{{end}}
<p style="color: #374151;">Welcome to {{.Brand.Name}}! Use the code below to verify your email address.</p>

<div style="text-align: center; margin: 32px 0;">
  <p style="font-size: 36px; font-weight: 700; letter-spacing: 8px; color: #1f2937; margin: 0;">{{.Code}}</p>
</div>

<p style="font-size: 14px; color: #6b7280;">This code expires in {{.ExpiresIn}}.</p>

<p style="margin-top: 24px; padding-top: 24px; border-top: 1px solid #e5e7eb; font-size: 12px; color: #9ca3af;">
  Didn't sign up? You can safely ignore this email.
</p>
{{end}}
`

// authVerificationTXTRaw is written to templates/authentication/verification.txt.
const authVerificationTXTRaw = `{{define "authentication:verification.text"}}
Verify your email
{{if .DisplayName}}
Hi {{.DisplayName}},
{{end}}
Welcome to {{.Brand.Name}}! Your verification code is:

{{.Code}}

This code expires in {{.ExpiresIn}}.

Didn't sign up? You can safely ignore this email.
{{end}}
`

// authPasswordResetHTMLRaw is written to templates/authentication/password_reset.html.
const authPasswordResetHTMLRaw = `{{define "authentication:password_reset"}}
<h2 style="margin: 0 0 16px 0; color: #1f2937; font-size: 22px;">Reset your password</h2>
{{if .DisplayName}}<p style="color: #374151;">Hi {{.DisplayName}},</p>{{end}}
<p style="color: #374151;">We received a request to reset your password. Use the token below to complete the reset.</p>

<div style="background: #f9fafb; border: 1px solid #e5e7eb; border-radius: 6px; padding: 16px; margin: 24px 0; word-break: break-all; font-family: monospace; font-size: 14px; color: #1f2937;">
  {{.Token}}
</div>

<p style="font-size: 14px; color: #6b7280;">This token expires in {{.ExpiresIn}}.</p>

<p style="margin-top: 24px; padding-top: 24px; border-top: 1px solid #e5e7eb; font-size: 12px; color: #9ca3af;">
  Didn't request a password reset? You can safely ignore this email.
</p>
{{end}}
`

// authPasswordResetTXTRaw is written to templates/authentication/password_reset.txt.
const authPasswordResetTXTRaw = `{{define "authentication:password_reset.text"}}
Reset your password
{{if .DisplayName}}
Hi {{.DisplayName}},
{{end}}
We received a request to reset your password. Use the token below to complete the reset:

{{.Token}}

This token expires in {{.ExpiresIn}}.

Didn't request a password reset? You can safely ignore this email.
{{end}}
`

// authOAuthLinkHTMLRaw is written to templates/authentication/oauth_link_verification.html.
const authOAuthLinkHTMLRaw = `{{define "authentication:oauth_link_verification"}}
<h2 style="margin: 0 0 16px 0; color: #1f2937; font-size: 22px;">Verify account link</h2>
{{if .DisplayName}}<p style="color: #374151;">Hi {{.DisplayName}},</p>{{end}}
<p style="color: #374151;">
  Someone is trying to link a <strong>{{.Provider}}</strong> account to your {{.Brand.Name}} account.
  Use the code below to confirm this action.
</p>

<div style="text-align: center; margin: 32px 0;">
  <p style="font-size: 36px; font-weight: 700; letter-spacing: 8px; color: #1f2937; margin: 0;">{{.Code}}</p>
</div>

<p style="font-size: 14px; color: #6b7280;">This code expires in {{.ExpiresIn}}.</p>

<p style="margin-top: 24px; padding-top: 24px; border-top: 1px solid #e5e7eb; font-size: 12px; color: #9ca3af;">
  Didn't initiate this? You can safely ignore this email — no changes will be made to your account.
</p>
{{end}}
`

// authOAuthLinkTXTRaw is written to templates/authentication/oauth_link_verification.txt.
const authOAuthLinkTXTRaw = `{{define "authentication:oauth_link_verification.text"}}
Verify account link
{{if .DisplayName}}
Hi {{.DisplayName}},
{{end}}
Someone is trying to link a {{.Provider}} account to your {{.Brand.Name}} account.
Use the code below to confirm this action:

{{.Code}}

This code expires in {{.ExpiresIn}}.

Didn't initiate this? You can safely ignore this email — no changes will be made to your account.
{{end}}
`

// airTomlRaw is the default air.toml configuration for hot-reload during development.
const airTomlRaw = `root = "."
tmp_dir = "tmp"

[build]
  args_bin = []
  bin = "./tmp/main"
  cmd = "go build -o ./tmp/main ./app/server"
  delay = 1000
  exclude_dir = ["assets", "tmp", "vendor", "node_modules", ".git", "workshop", "aesthetics/ts"]
  exclude_file = []
  exclude_regex = ["_test.go"]
  exclude_unchanged = false
  follow_symlink = false
  full_bin = ""
  include_dir = []
  include_ext = ["go", "tpl", "tmpl", "html"]
  include_file = []
  kill_delay = "0s"
  log = "build-errors.log"
  poll = false
  poll_interval = 0
  post_cmd = []
  pre_cmd = []
  rerun = false
  rerun_delay = 500
  send_interrupt = false
  stop_on_error = false

[color]
  app = ""
  build = "yellow"
  main = "magenta"
  runner = "green"
  watcher = "cyan"

[log]
  main_only = false
  time = false

[misc]
  clean_on_exit = false

[screen]
  clear_on_rebuild = false
  keep_scroll = true
`
