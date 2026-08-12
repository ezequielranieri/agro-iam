// Command api is the HTTP entrypoint for the agro-iam platform.
//
// Startup order: config -> postgres (fail fast) -> redis (best effort) ->
// wire services -> serve :8080. The server shuts down cleanly on SIGINT/SIGTERM.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ezequielranieri/agro-iam/internal/application/services"
	apphttp "github.com/ezequielranieri/agro-iam/internal/http"
	"github.com/ezequielranieri/agro-iam/internal/infrastructure/auth"
	"github.com/ezequielranieri/agro-iam/internal/infrastructure/postgres"
	"github.com/ezequielranieri/agro-iam/internal/infrastructure/redis"
)

const (
	accessTokenTTL  = 15 * time.Minute
	refreshTokenTTL = 30 * 24 * time.Hour // 30 days
)

// config aggregates environment variables. Deliberately plain os.Getenv â€”
// no external config library (per project decision).
type config struct {
	databaseURL string
	redisAddr   string
	jwtSecret   string
	httpAddr    string
}

func loadConfig() config {
	return config{
		databaseURL: os.Getenv("DATABASE_URL"),
		redisAddr:   os.Getenv("REDIS_ADDR"),
		jwtSecret:   os.Getenv("JWT_SECRET"),
		httpAddr:    os.Getenv("HTTP_ADDR"),
	}
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("fatal", "error", err.Error())
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := loadConfig()

	// Postgres is a hard dependency: without it nothing can be served.
	// Fail fast with a clear fatal instead of limping along.
	if cfg.databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is not set (see .env.example)")
	}
	pool, err := postgres.NewPool(ctx, cfg.databaseURL)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	log.Info("postgres connected")

	// Redis is not required for slice 0 â€” log a warning but keep serving.
	rdb, err := redis.NewClient(ctx, cfg.redisAddr)
	if err != nil {
		log.Warn("redis unavailable, continuing without it", "addr", cfg.redisAddr, "error", err.Error())
	} else {
		defer func() { _ = rdb.Close() }()
		log.Info("redis connected", "addr", cfg.redisAddr)
	}

	if cfg.jwtSecret == "" || cfg.jwtSecret == "change-me" {
		return fmt.Errorf("JWT_SECRET must be set to a real secret (see .env.example)")
	}

	tokenManager, err := auth.NewTokenManager(cfg.jwtSecret, accessTokenTTL)
	if err != nil {
		return err
	}

	// Wire ports -> implementations (dependency injection at the composition root).
	userRepo := postgres.NewUserRepo(pool)
	tenantRepo := postgres.NewTenantRepo(pool)
	refreshStore := auth.NewRefreshTokenStore(pool)
	lotRepo := postgres.NewLotRepo(pool)
	campaignRepo := postgres.NewCampaignRepo(pool)
	applicationRepo := postgres.NewApplicationRepo(pool)
	userRoleRepo := postgres.NewUserRoleRepo(pool)

	// Audit: tamper-evident chained trail (slice 3). The repo runs every write
	// inside WithTenant; the service is fail-open by contract.
	auditRepo := postgres.NewAuditRepo(pool)
	auditService := services.NewAuditService(auditRepo, log)

	// Rate limiter: Redis-backed with in-memory fallback. rdb may be nil when
	// Redis is unavailable — NewRateLimiter falls back per-process (fail-open).
	rateLimiter := redis.NewRateLimiter(rdb, log)

	// Breach signals (DECISIONS 2.15): slog is ALWAYS registered, then audit.
	// The fan-out is the single emission path shared by every service and the
	// middleware (R2/R3). Order = registration: slog first, audit second.
	slogSink := services.NewSlogSink(log)
	auditSink := services.NewAuditSink(auditService, log)
	signals := services.NewFanOut(log, slogSink, auditSink)

	lotService := services.NewLotService(lotRepo, signals)
	campaignService := services.NewCampaignService(campaignRepo, signals)
	applicationService := services.NewApplicationService(applicationRepo, signals)

	// Provisioning: the Argon2id hasher is shared with the auth service so
	// hashes created here verify there and vice versa (one hashing policy).
	hasher := auth.NewPasswordHasher()
	userService := services.NewUserService(userRepo, userRoleRepo, hasher, signals)

	authService := services.NewAuthService(
		userRepo,
		tenantRepo,
		tokenManager,
		hasher,
		userRoleRepo,
		refreshStore,
		signals,
		accessTokenTTL,
		refreshTokenTTL,
	)

	srv := &http.Server{
		Addr:         cfg.httpAddr,
		Handler:      apphttp.NewServer(authService, tokenManager, lotService, campaignService, applicationService, userService, auditService, rateLimiter, signals, log).Routes(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	go func() {
		log.Info("http server listening", "addr", cfg.httpAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("http server error", "error", err.Error())
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
