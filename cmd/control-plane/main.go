package main

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/kitex/pkg/transmeta"
	kitexutils "github.com/cloudwego/kitex/pkg/utils"
	"github.com/cloudwego/kitex/server"
	authkitex "github.com/nciyuan9264/game-platform/internal/authclient/kitex"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/control"
	"github.com/nciyuan9264/game-platform/internal/coordination"
	"github.com/nciyuan9264/game-platform/internal/observability"
	"github.com/nciyuan9264/game-platform/internal/platform/node"
	"github.com/nciyuan9264/game-platform/internal/plugins"
	"github.com/nciyuan9264/game-platform/internal/servicediscovery"
	"github.com/nciyuan9264/game-platform/internal/store"
	"github.com/nciyuan9264/game-platform/internal/transport"
	platformcontrolservice "github.com/nciyuan9264/game-platform/kitex_gen/pam/platform/control/v1/platformcontrolservice"
)

func main() {
	ctx := context.Background()
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
	authClient, err := authkitex.New(
		observability.Env("AUTH_RPC_ADDR", "auth-rpc:19090"),
		observability.EnvDuration("AUTH_RPC_TIMEOUT", 2*time.Second),
	)
	if err != nil {
		panic(err)
	}
	nodeID := observability.Env("NODE_ID", "control-plane-1")
	go node.Heartbeat(ctx, st, 10*time.Second, func() contracts.NodeInfo {
		return contracts.NodeInfo{NodeID: nodeID, Type: "control-plane", Addr: observability.Env("PUBLIC_ADDR", "control-plane:8080"), Version: "dev", Status: "ready"}
	})
	schedulerPolicy := observability.Env("ROOM_SCHEDULER", control.SchedulerLoadAware)
	pendingWindow := observability.EnvDuration("ROOM_SCHEDULER_PENDING_WINDOW", 2*time.Minute)
	games := plugins.Registry()
	srv := control.NewServerWithConfig(st, coord, control.Config{
		SchedulerPolicy: schedulerPolicy,
		PendingWindow:   pendingWindow,
		Games:           games,
		RoomCommands:    router,
		AdminAuth:       authClient,
	})
	srv.StartDailyCleanup(ctx)
	rpcAddr := observability.Env("CONTROL_RPC_ADDR", ":18091")
	serverOptions, psm, err := servicediscovery.ServerOptions("game.platform.control")
	if err != nil {
		panic(err)
	}
	serverOptions = append(serverOptions,
		server.WithServiceAddr(kitexutils.NewNetAddr("tcp", rpcAddr)),
		server.WithMetaHandler(transmeta.ServerTTHeaderHandler),
	)
	rpcServer := platformcontrolservice.NewServer(control.NewRPCHandler(srv), serverOptions...)
	go func() {
		observability.Info("control-plane rpc listening", "addr", rpcAddr, "psm", psm)
		if err := rpcServer.Run(); err != nil {
			panic(err)
		}
	}()
	addr := observability.Env("SERVER_ADDR", ":8080")
	observability.Info("control-plane listening", "addr", addr, "roomScheduler", schedulerPolicy, "roomSchedulerPendingWindow", pendingWindow)
	if err := http.ListenAndServe(addr, srv.Routes()); err != nil {
		panic(err)
	}
}
