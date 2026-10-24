package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/observability"
	"github.com/nciyuan9264/game-platform/internal/store"
)

type releaseStore interface {
	ListNodes(context.Context, string) ([]contracts.NodeInfo, error)
	GetGameCatalogEntry(context.Context, string) (contracts.GameCatalogEntry, error)
	ListGameCatalog(context.Context) ([]contracts.GameCatalogEntry, error)
	RegisterGameCatalogEntry(
		context.Context,
		contracts.GameCatalogEntry,
		string,
		string,
	) (contracts.GameCatalogEntry, bool, error)
	SetActiveGameVersion(
		context.Context,
		string,
		string,
		string,
		string,
		string,
		string,
	) (contracts.GameCatalogEntry, error)
	DisableGameCatalogEntry(
		context.Context,
		string,
		string,
		string,
	) (contracts.GameCatalogEntry, bool, error)
}

type commandOutput struct {
	Action  string                       `json:"action"`
	Changed bool                         `json:"changed"`
	Entry   *contracts.GameCatalogEntry  `json:"entry,omitempty"`
	Catalog []contracts.GameCatalogEntry `json:"catalog,omitempty"`
	Nodes   []contracts.NodeInfo         `json:"nodes,omitempty"`
}

func main() {
	if err := runMain(context.Background(), os.Args[1:], os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func runMain(ctx context.Context, args []string, stdout io.Writer) error {
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
	defer st.Close()
	return run(ctx, args, st, stdout, releaseActor())
}

func run(
	ctx context.Context,
	args []string,
	st releaseStore,
	stdout io.Writer,
	actor string,
) error {
	if len(args) == 0 {
		return errors.New("command is required: register, promote, rollback, disable, or status")
	}

	switch args[0] {
	case "register":
		return runRegister(ctx, args[1:], st, stdout, actor)
	case "promote":
		return runVersionChange(
			ctx,
			args[1:],
			st,
			stdout,
			actor,
			contracts.GameCatalogActionPromote,
		)
	case "rollback":
		return runVersionChange(
			ctx,
			args[1:],
			st,
			stdout,
			actor,
			contracts.GameCatalogActionRollback,
		)
	case "disable":
		return runDisable(ctx, args[1:], st, stdout, actor)
	case "status":
		return runStatus(ctx, args[1:], st, stdout)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runRegister(
	ctx context.Context,
	args []string,
	st releaseStore,
	stdout io.Writer,
	actor string,
) error {
	flags := flag.NewFlagSet("register", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	gameType := flags.String("game", "", "game type")
	version := flags.String("version", "", "engine version")
	maxPlayers := flags.Int("default-max-players", 0, "default maximum players")
	replaySchema := flags.Int("replay-schema-version", 0, "replay schema version")
	artifact := flags.String("artifact", "", "immutable artifact identifier")
	isDefault := flags.Bool("default", false, "register as the default game")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*gameType) == "" ||
		strings.TrimSpace(*version) == "" ||
		*maxPlayers <= 0 ||
		*replaySchema <= 0 {
		return errors.New(
			"register requires --game, --version, --default-max-players, and --replay-schema-version",
		)
	}
	if _, err := requireReadyVersion(ctx, st, *gameType, *version); err != nil {
		return err
	}
	entry, created, err := st.RegisterGameCatalogEntry(ctx, contracts.GameCatalogEntry{
		GameType:            strings.TrimSpace(*gameType),
		ActiveEngineVersion: strings.TrimSpace(*version),
		DefaultMaxPlayers:   *maxPlayers,
		ReplaySchemaVersion: *replaySchema,
		EnabledForNewRooms:  true,
		IsDefault:           *isDefault,
	}, *artifact, actor)
	if err != nil {
		return err
	}
	return writeOutput(stdout, commandOutput{
		Action:  contracts.GameCatalogActionRegister,
		Changed: created,
		Entry:   &entry,
	})
}

func runVersionChange(
	ctx context.Context,
	args []string,
	st releaseStore,
	stdout io.Writer,
	actor,
	action string,
) error {
	flags := flag.NewFlagSet(action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	gameType := flags.String("game", "", "game type")
	version := flags.String("version", "", "target engine version")
	expected := flags.String("expected-version", "", "expected active engine version")
	artifact := flags.String("artifact", "", "immutable artifact identifier")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*gameType) == "" ||
		strings.TrimSpace(*version) == "" ||
		strings.TrimSpace(*expected) == "" {
		return fmt.Errorf(
			"%s requires --game, --version, and --expected-version",
			action,
		)
	}
	if _, err := requireReadyVersion(ctx, st, *gameType, *version); err != nil {
		return err
	}
	entry, err := st.SetActiveGameVersion(
		ctx,
		strings.TrimSpace(*gameType),
		strings.TrimSpace(*version),
		strings.TrimSpace(*expected),
		action,
		*artifact,
		actor,
	)
	if err != nil {
		return err
	}
	return writeOutput(stdout, commandOutput{
		Action:  action,
		Changed: true,
		Entry:   &entry,
	})
}

func runDisable(
	ctx context.Context,
	args []string,
	st releaseStore,
	stdout io.Writer,
	actor string,
) error {
	flags := flag.NewFlagSet("disable", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	gameType := flags.String("game", "", "game type")
	artifact := flags.String("artifact", "", "immutable artifact identifier")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*gameType) == "" {
		return errors.New("disable requires --game")
	}
	entry, changed, err := st.DisableGameCatalogEntry(
		ctx,
		strings.TrimSpace(*gameType),
		*artifact,
		actor,
	)
	if err != nil {
		return err
	}
	return writeOutput(stdout, commandOutput{
		Action:  contracts.GameCatalogActionDisable,
		Changed: changed,
		Entry:   &entry,
	})
}

func runStatus(
	ctx context.Context,
	args []string,
	st releaseStore,
	stdout io.Writer,
) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	gameType := flags.String("game", "", "game type")
	_ = flags.Bool("json", true, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}

	var (
		catalog []contracts.GameCatalogEntry
		err     error
	)
	if strings.TrimSpace(*gameType) == "" {
		catalog, err = st.ListGameCatalog(ctx)
	} else {
		entry, entryErr := st.GetGameCatalogEntry(ctx, strings.TrimSpace(*gameType))
		err = entryErr
		if entryErr == nil {
			catalog = []contracts.GameCatalogEntry{entry}
		}
	}
	if err != nil {
		return err
	}
	nodes, err := st.ListNodes(ctx, contracts.NodeTypeRoomHost)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*gameType) != "" {
		nodes = nodesForGame(nodes, strings.TrimSpace(*gameType))
	}
	return writeOutput(stdout, commandOutput{
		Action:  "status",
		Changed: false,
		Catalog: catalog,
		Nodes:   nodes,
	})
}

func requireReadyVersion(
	ctx context.Context,
	st releaseStore,
	gameType,
	version string,
) ([]contracts.NodeInfo, error) {
	gameType = strings.TrimSpace(gameType)
	version = strings.TrimSpace(version)
	nodes, err := st.ListNodes(ctx, contracts.NodeTypeRoomHost)
	if err != nil {
		return nil, err
	}
	ready := make([]contracts.NodeInfo, 0)
	for _, node := range nodes {
		if node.Status == contracts.NodeStatusReady &&
			node.GameVersions[gameType] == version {
			ready = append(ready, node)
		}
	}
	if len(ready) == 0 {
		return nil, fmt.Errorf(
			"no ready roomhost supports %s@%s",
			gameType,
			version,
		)
	}
	return ready, nil
}

func nodesForGame(nodes []contracts.NodeInfo, gameType string) []contracts.NodeInfo {
	filtered := make([]contracts.NodeInfo, 0)
	for _, node := range nodes {
		if _, ok := node.GameVersions[gameType]; ok {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

func writeOutput(stdout io.Writer, output commandOutput) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(output)
}

func releaseActor() string {
	if actor := strings.TrimSpace(os.Getenv("GAME_RELEASE_ACTOR")); actor != "" {
		return actor
	}
	if actor := strings.TrimSpace(os.Getenv("GITHUB_ACTOR")); actor != "" {
		return actor
	}
	return strings.TrimSpace(os.Getenv("USER"))
}
