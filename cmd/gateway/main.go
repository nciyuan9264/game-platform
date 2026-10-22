package main

import (
	"context"
	"net/http"
	"time"

	"github.com/nciyuan9264/game-platform/internal/authclient"
	authkitex "github.com/nciyuan9264/game-platform/internal/authclient/kitex"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/coordination"
	"github.com/nciyuan9264/game-platform/internal/gateway"
	"github.com/nciyuan9264/game-platform/internal/observability"
	"github.com/nciyuan9264/game-platform/internal/platform/node"
	"github.com/nciyuan9264/game-platform/internal/store"
	"github.com/nciyuan9264/game-platform/internal/transport"
)

func main() {
	ctx := context.Background()
	nodeID := observability.Env("NODE_ID", "gateway-1")
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
	rpcAddr := observability.Env("AUTH_RPC_ADDR", "")
	if rpcAddr == "" {
		panic("AUTH_RPC_ADDR is required")
	}
	authClient, err := authkitex.New(
		rpcAddr,
		observability.EnvDuration("AUTH_RPC_TIMEOUT", 2*time.Second),
	)
	if err != nil {
		panic(err)
	}
	tokenVerifier := authclient.NewVerifier(authClient, authClient)
	srv := gateway.NewServer(nodeID, tokenVerifier, st, router, coord)
	if err := srv.Start(ctx); err != nil {
		panic(err)
	}
	go node.Heartbeat(ctx, st, 10*time.Second, func() contracts.NodeInfo {
		return contracts.NodeInfo{NodeID: nodeID, Type: contracts.NodeTypeGateway, Addr: observability.Env("PUBLIC_ADDR", nodeID+":8080"), Version: "dev", Status: "ready", ActiveSessions: srv.ActiveSessions()}
	})
	addr := observability.Env("SERVER_ADDR", ":8080")
	observability.Info("gateway listening", "nodeID", nodeID, "addr", addr)
	if err := http.ListenAndServe(addr, srv.Routes()); err != nil {
		panic(err)
	}
}
