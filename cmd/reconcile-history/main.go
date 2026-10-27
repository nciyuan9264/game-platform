package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/nciyuan9264/game-platform/internal/historyrebuild"
	"github.com/nciyuan9264/game-platform/internal/observability"
	"github.com/nciyuan9264/game-platform/internal/plugins/fugitive"
	"github.com/nciyuan9264/game-platform/internal/store"
	"github.com/nciyuan9264/game-platform/internal/transport"
)

func main() {
	if err := runMain(context.Background(), os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func runMain(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("reconcile-history", flag.ContinueOnError)
	roomID := flags.String("room-id", "", "room whose history should be rebuilt")
	matchID := flags.String(
		"match-id",
		"",
		"match to rebuild; defaults to the room's current or latest match",
	)
	dryRun := flags.Bool(
		"dry-run",
		true,
		"validate and print the recovery plan without writing",
	)
	apply := flags.Bool(
		"apply",
		false,
		"write the validated history using idempotent store operations",
	)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*roomID) == "" {
		return errors.New("--room-id is required")
	}
	if !*apply && !*dryRun {
		return errors.New("writes require --apply")
	}

	postgres, err := store.NewPostgresStore(ctx, observability.Env(
		"POSTGRES_DSN",
		"postgres://game:game@localhost:5432/game_platform?sslmode=disable",
	))
	if err != nil {
		return fmt.Errorf("open postgres: %w", err)
	}
	st := store.NewStore(nil, postgres)
	defer st.Close()

	router, err := transport.NewRouter(observability.Env(
		"NATS_URL",
		"nats://localhost:4222",
	))
	if err != nil {
		return fmt.Errorf("open NATS: %w", err)
	}
	defer router.Close()

	rebuilder := historyrebuild.New(
		st,
		router,
		fugitive.Definition(),
		"fugitive-v1",
	)
	result, err := rebuilder.Run(ctx, historyrebuild.Config{
		RoomID:  strings.TrimSpace(*roomID),
		MatchID: strings.TrimSpace(*matchID),
		Apply:   *apply,
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
