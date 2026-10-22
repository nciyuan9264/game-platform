package main

import (
	"context"
	"log"

	"github.com/nciyuan9264/game-platform-server/internal/observability"
	"github.com/nciyuan9264/game-platform-server/internal/store"
)

func main() {
	ctx := context.Background()
	postgres, err := store.NewPostgresStore(
		ctx,
		observability.Env(
			"POSTGRES_DSN",
			"postgres://game:game@localhost:5432/game_platform?sslmode=disable",
		),
	)
	if err != nil {
		log.Fatalf("open postgres: %v", err)
	}
	defer postgres.Close()

	if err := postgres.Migrate(ctx); err != nil {
		log.Fatalf("migrate schema: %v", err)
	}
	log.Print("game platform schema migration completed")
}
