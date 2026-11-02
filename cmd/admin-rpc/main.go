package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/cloudwego/kitex/pkg/endpoint"
	"github.com/cloudwego/kitex/pkg/transmeta"
	kitexutils "github.com/cloudwego/kitex/pkg/utils"
	"github.com/cloudwego/kitex/server"
	"github.com/kitex-contrib/obs-opentelemetry/tracing"
	"github.com/nciyuan9264/game-platform/internal/adminanalytics"
	"github.com/nciyuan9264/game-platform/internal/adminrpc"
	"github.com/nciyuan9264/game-platform/internal/health"
	"github.com/nciyuan9264/game-platform/internal/observability"
	"github.com/nciyuan9264/game-platform/internal/servicediscovery"
	"github.com/nciyuan9264/game-platform/internal/telemetry"
	adminservice "github.com/nciyuan9264/game-platform/kitex_gen/pam/game/admin/v1/gameadminservice"
)

const defaultPSM = "game.admin.rpc"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() (runErr error) {
	telemetryProvider := telemetry.Init(defaultPSM, os.Getenv("PAM_ENV"))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := telemetryProvider.Shutdown(ctx); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("shutdown telemetry: %w", err))
		}
	}()

	analytics, err := adminanalytics.Open(context.Background(), adminanalytics.Config{
		GamePostgresDSN: observability.Env(
			"POSTGRES_DSN",
			"postgres://game:game@localhost:5432/game_platform?sslmode=disable",
		),
		AuthPostgresDSN: observability.Env(
			"AUTH_POSTGRES_DSN",
			"postgres://auth:auth@localhost:5432/auth?sslmode=disable",
		),
		CacheTTL: observability.EnvDuration("ADMIN_ANALYTICS_CACHE_TTL", time.Minute),
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := analytics.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close analytics databases: %w", err))
		}
	}()

	readiness := health.NewChecker(2*time.Second,
		health.Check{Name: "game-postgres", Ping: analytics.PingGame},
		health.Check{Name: "auth-postgres", Ping: analytics.PingAuth},
	)
	healthAddr := observability.Env("GAME_ADMIN_HEALTH_ADDR", ":18095")
	healthListener, err := net.Listen("tcp", healthAddr)
	if err != nil {
		return fmt.Errorf("listen for health checks on %s: %w", healthAddr, err)
	}
	defer healthListener.Close()
	healthServer := &http.Server{
		Handler: readiness.Handler(), ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 30 * time.Second,
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := healthServer.Shutdown(shutdownCtx); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("shutdown health server: %w", err))
		}
	}()

	serverOptions, psm, err := servicediscovery.ServerOptions(defaultPSM)
	if err != nil {
		return fmt.Errorf("initialize service registry: %w", err)
	}
	rpcAddr := observability.Env("GAME_ADMIN_RPC_ADDR", ":18094")
	server.RegisterStartHook(func() { readiness.SetServing(true) })
	server.RegisterShutdownHook(func() { readiness.SetServing(false) })
	serverOptions = append(serverOptions,
		server.WithServiceAddr(kitexutils.NewNetAddr("tcp", rpcAddr)),
		server.WithMetaHandler(transmeta.ServerTTHeaderHandler),
		server.WithSuite(tracing.NewServerSuite()),
		server.WithMiddleware(endpoint.Chain(
			telemetry.AccessLogMiddleware(psm),
			adminrpc.HTTPResponseMiddleware,
			adminrpc.AdminAuthMiddleware,
		)),
	)
	rpcServer := adminservice.NewServer(adminrpc.NewHandler(analytics), serverOptions...)

	healthErr := make(chan error, 1)
	go func() {
		if err := healthServer.Serve(healthListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			healthErr <- err
		}
	}()
	log.Printf("game admin health checks listening on %s", healthAddr)
	log.Printf("game admin rpc psm=%s listening on %s", psm, rpcAddr)

	rpcErr := make(chan error, 1)
	go func() { rpcErr <- rpcServer.Run() }()
	select {
	case err := <-rpcErr:
		return err
	case err := <-healthErr:
		readiness.SetServing(false)
		return errors.Join(fmt.Errorf("serve health checks: %w", err), rpcServer.Stop())
	}
}
