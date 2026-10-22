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
	platformservice "github.com/nciyuan9264/game-platform-client/kitex_gen/pam/game/platform/v1/gameplatformservice"
	"github.com/nciyuan9264/game-platform-server/internal/controlclient"
	"github.com/nciyuan9264/game-platform-server/internal/health"
	"github.com/nciyuan9264/game-platform-server/internal/observability"
	"github.com/nciyuan9264/game-platform-server/internal/plugins"
	platformrpc "github.com/nciyuan9264/game-platform-server/internal/rpc"
	"github.com/nciyuan9264/game-platform-server/internal/servicediscovery"
	"github.com/nciyuan9264/game-platform-server/internal/store"
	"github.com/nciyuan9264/game-platform-server/internal/telemetry"
)

const defaultPSM = "game.platform.rpc"

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

	ctx := context.Background()
	st, err := store.Open(ctx, store.Config{
		RedisAddr:     observability.Env("REDIS_ADDR", "localhost:6379"),
		RedisPassword: observability.Env("REDIS_PASSWORD", ""),
		RedisDB:       observability.EnvInt("REDIS_DB", 0),
		PostgresDSN: observability.Env(
			"POSTGRES_DSN",
			"postgres://game:game@localhost:5432/game_platform?sslmode=disable",
		),
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close store: %w", err))
		}
	}()

	control, err := controlclient.New(
		observability.Env("CONTROL_RPC_ADDR", "localhost:18091"),
		observability.EnvDuration("CONTROL_RPC_TIMEOUT", 3*time.Second),
	)
	if err != nil {
		return fmt.Errorf("initialize control client: %w", err)
	}
	handler := platformrpc.NewHandler(st, control, plugins.Registry())
	readiness := health.NewChecker(2*time.Second, health.Check{Name: "store", Ping: st.Ping})

	healthAddr := observability.Env("GAME_PLATFORM_HEALTH_ADDR", ":18093")
	healthListener, err := net.Listen("tcp", healthAddr)
	if err != nil {
		return fmt.Errorf("listen for health checks on %s: %w", healthAddr, err)
	}
	defer healthListener.Close()
	healthServer := &http.Server{
		Handler:           readiness.Handler(),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       30 * time.Second,
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
	rpcAddr := observability.Env("GAME_PLATFORM_RPC_ADDR", ":18092")
	server.RegisterStartHook(func() {
		readiness.SetServing(true)
	})
	server.RegisterShutdownHook(func() {
		readiness.SetServing(false)
	})
	serverOptions = append(serverOptions,
		server.WithServiceAddr(kitexutils.NewNetAddr("tcp", rpcAddr)),
		server.WithMetaHandler(transmeta.ServerTTHeaderHandler),
		server.WithSuite(tracing.NewServerSuite()),
		server.WithMiddleware(endpoint.Chain(
			telemetry.AccessLogMiddleware(psm),
			platformrpc.HTTPResponseMiddleware,
			platformrpc.UserAuthMiddleware,
		)),
	)
	rpcServer := platformservice.NewServer(handler, serverOptions...)

	healthErr := make(chan error, 1)
	go func() {
		if err := healthServer.Serve(healthListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			healthErr <- err
		}
	}()
	log.Printf("game platform health checks listening on %s", healthAddr)
	log.Printf("game platform rpc psm=%s listening on %s", psm, rpcAddr)

	rpcErr := make(chan error, 1)
	go func() {
		rpcErr <- rpcServer.Run()
	}()
	select {
	case err := <-rpcErr:
		return err
	case err := <-healthErr:
		readiness.SetServing(false)
		return errors.Join(fmt.Errorf("serve health checks: %w", err), rpcServer.Stop())
	}
}
