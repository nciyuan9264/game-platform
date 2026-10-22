package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/nciyuan9264/game-platform-server/internal/migration"
	"github.com/nciyuan9264/game-platform-server/internal/store"
)

func main() {
	var (
		dryRun        = flag.Bool("dry-run", false, "count source rows without writing")
		verify        = flag.Bool("verify", false, "verify target counts against source")
		gameTypeValue = flag.String("game-type", "", "comma-separated game types; empty means all")
	)
	flag.Parse()

	sourceDSN := os.Getenv("LEGACY_GAME_POSTGRES_DSN")
	targetDSN := os.Getenv("POSTGRES_DSN")
	if sourceDSN == "" || targetDSN == "" {
		log.Fatal("LEGACY_GAME_POSTGRES_DSN and POSTGRES_DSN are required")
	}
	ctx := context.Background()
	target, err := store.NewPostgresStore(ctx, targetDSN)
	if err != nil {
		log.Fatalf("open target database: %v", err)
	}
	if err := target.Migrate(ctx); err != nil {
		log.Fatalf("initialize target schema: %v", err)
	}
	_ = target.Close()

	var gameTypes []string
	if value := strings.TrimSpace(*gameTypeValue); value != "" {
		gameTypes = strings.Split(value, ",")
	}
	result, err := migration.CopyGameHistory(ctx, sourceDSN, targetDSN, migration.GameHistoryOptions{
		GameTypes: gameTypes,
		DryRun:    *dryRun,
		Verify:    *verify,
	})
	if err != nil {
		log.Fatal(err)
	}
	mode := "migrated"
	if *dryRun {
		mode = "dry-run"
	}
	for _, count := range result.Counts {
		fmt.Printf(
			"GAME_HISTORY_%s game_type=%s games=%d players=%d events=%d\n",
			strings.ToUpper(strings.ReplaceAll(mode, "-", "_")),
			count.GameType,
			count.Games,
			count.Players,
			count.Events,
		)
	}
}
