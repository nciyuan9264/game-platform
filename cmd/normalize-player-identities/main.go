package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
	"github.com/nciyuan9264/game-platform/internal/migration"
)

func main() {
	if err := runMain(context.Background(), os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func runMain(ctx context.Context, args []string) error {
	options, err := parseOptions(args)
	if err != nil {
		return err
	}
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		return errors.New("POSTGRES_DSN is required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	report, runErr := migration.NewRunner(db).Run(ctx, options)
	encoded, encodeErr := json.Marshal(report)
	if encodeErr != nil {
		return errors.Join(runErr, encodeErr)
	}
	fmt.Println(string(encoded))
	return runErr
}

func parseOptions(args []string) (migration.Options, error) {
	flags := flag.NewFlagSet("normalize-player-identities", flag.ContinueOnError)
	dryRun := flags.Bool("dry-run", false, "validate all rewrites without changing data")
	verifyOnly := flags.Bool("verify-only", false, "only verify canonical identities")
	gameType := flags.String("game-type", "", "limit processing to one game type")
	afterID := flags.Int64("after-id", 0, "resume after this game ID")
	batchSize := flags.Int("batch-size", 100, "games per database page")
	if err := flags.Parse(args); err != nil {
		return migration.Options{}, err
	}
	if *dryRun && *verifyOnly {
		return migration.Options{}, errors.New("--dry-run and --verify-only are mutually exclusive")
	}
	if *afterID < 0 || *batchSize <= 0 || *batchSize > 1000 {
		return migration.Options{}, errors.New("--after-id must be non-negative and --batch-size must be 1..1000")
	}
	switch *gameType {
	case "", "acquire", "davinci", "splendor":
	default:
		return migration.Options{}, errors.New("--game-type must be acquire, davinci, or splendor")
	}
	return migration.Options{
		DryRun:     *dryRun,
		VerifyOnly: *verifyOnly,
		GameType:   *gameType,
		AfterID:    *afterID,
		BatchSize:  *batchSize,
	}, nil
}
