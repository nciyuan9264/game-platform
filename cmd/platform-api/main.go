package main

import (
	"context"
	"net/http"
	"time"

	"github.com/nciyuan9264/game-platform-server/internal/authapi"
	authkitex "github.com/nciyuan9264/game-platform-server/internal/authclient/kitex"
	"github.com/nciyuan9264/game-platform-server/internal/controlclient"
	"github.com/nciyuan9264/game-platform-server/internal/observability"
	"github.com/nciyuan9264/game-platform-server/internal/platformapi"
	"github.com/nciyuan9264/game-platform-server/internal/store"
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

	timeout := observability.EnvDuration("RPC_TIMEOUT", 3*time.Second)
	authClient, err := authkitex.New(
		observability.Env("AUTH_RPC_ADDR", "localhost:19090"),
		timeout,
	)
	if err != nil {
		panic(err)
	}
	controlClient, err := controlclient.New(
		observability.Env("CONTROL_RPC_ADDR", "localhost:18091"),
		timeout,
	)
	if err != nil {
		panic(err)
	}

	server := platformapi.New(st, authClient, controlClient, authapi.Config{
		CookieDomain:    observability.Env("COOKIE_DOMAIN", ""),
		CookieSecure:    observability.Env("COOKIE_SECURE", "false") == "true",
		AvatarMaxSizeMB: int64(observability.EnvInt("AVATAR_MAX_SIZE_MB", 5)),
		ExposeDevCode:   observability.Env("AUTH_DEV_CODE_RESPONSE", "false") == "true",
	})
	addr := observability.Env("SERVER_ADDR", ":8080")
	observability.Info("platform-api listening", "addr", addr)
	if err := http.ListenAndServe(addr, server.Routes()); err != nil {
		panic(err)
	}
}
