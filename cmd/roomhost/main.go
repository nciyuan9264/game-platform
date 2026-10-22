package main

import (
	"context"
	"os/signal"
	"syscall"
	"time"

	"github.com/nciyuan9264/game-platform-server/internal/contracts"
	"github.com/nciyuan9264/game-platform-server/internal/coordination"
	"github.com/nciyuan9264/game-platform-server/internal/observability"
	"github.com/nciyuan9264/game-platform-server/internal/plugins"
	"github.com/nciyuan9264/game-platform-server/internal/roomhost"
	"github.com/nciyuan9264/game-platform-server/internal/store"
	"github.com/nciyuan9264/game-platform-server/internal/transport"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	nodeID := observability.Env("NODE_ID", "roomhost-1")
	st, err := store.Open(ctx, store.Config{
		RedisAddr:     observability.Env("REDIS_ADDR", "localhost:6379"),
		RedisPassword: observability.Env("REDIS_PASSWORD", ""),
		RedisDB:       observability.EnvInt("REDIS_DB", 0),
		PostgresDSN:   observability.Env("POSTGRES_DSN", "postgres://game:game@localhost:5432/game_platform?sslmode=disable"),
	})
	if err != nil {
		panic(err)
	}
	defer st.Close()
	coord, err := coordination.Open(ctx, observability.Env("ETCD_ENDPOINTS", "localhost:2379"))
	if err != nil {
		panic(err)
	}
	defer coord.Close()
	router, err := transport.NewRouter(observability.Env("NATS_URL", "nats://localhost:4222"))
	if err != nil {
		panic(err)
	}
	defer router.Close()
	if err := router.EnsureCommandStream(); err != nil {
		panic(err)
	}
	games := plugins.Registry()
	host := roomhost.NewHost(nodeID, st, coord, router, games)
	if err := host.Start(ctx); err != nil {
		panic(err)
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	heartbeat := func() {
		_ = st.UpsertNode(ctx, contracts.NodeInfo{
			NodeID:       nodeID,
			Type:         contracts.NodeTypeRoomHost,
			Addr:         observability.Env("PUBLIC_ADDR", nodeID+":9000"),
			Version:      "dev",
			GameVersions: games.Versions(),
			Status:       "ready",
			ActiveRooms:  host.ActiveRooms(),
		}, 10*time.Second)
	}
	heartbeat()
	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := host.Shutdown(shutdownCtx); err != nil {
				observability.Warn("roomhost graceful shutdown incomplete", "nodeID", nodeID, "error", err)
			}
			return
		case <-ticker.C:
			heartbeat()
		}
	}
}
