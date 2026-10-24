package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/history"
	"github.com/nciyuan9264/game-platform/internal/observability"
	"github.com/nciyuan9264/game-platform/internal/plugins"
	"github.com/nciyuan9264/game-platform/internal/store"
)

type backfillStore interface {
	ListReplayBackfillGameIDs(context.Context, string, int64, int) ([]int64, error)
	GetHistoryGame(context.Context, int64, string, bool) (history.Game, []history.Player, []history.Event, error)
	BackfillReplayPayloads(context.Context, int64, string, int, []byte, map[int][]byte) error
	CountMissingReplayPayloads(context.Context, string) (history.ReplayMissingCounts, error)
}

type backfillConfig struct {
	GameType            string
	EngineVersion       string
	ReplaySchemaVersion int
	AfterID             int64
	BatchSize           int
	DryRun              bool
}

type backfillOutput struct {
	GameType      string                      `json:"gameType"`
	EngineVersion string                      `json:"engineVersion"`
	Processed     int                         `json:"processed"`
	Succeeded     int                         `json:"succeeded"`
	FailedIDs     []int64                     `json:"failedGameIDs"`
	LastID        int64                       `json:"lastGameID"`
	Remaining     history.ReplayMissingCounts `json:"remaining"`
	DryRun        bool                        `json:"dryRun"`
}

func main() {
	if err := runMain(context.Background(), os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func runMain(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("backfill-replay", flag.ContinueOnError)
	gameType := flags.String("game-type", "", "game type to backfill")
	engineVersion := flags.String("engine-version", "", "explicit legacy engine version mapping")
	replaySchemaVersion := flags.Int("replay-schema-version", 1, "target replay schema version")
	afterID := flags.Int64("after-id", 0, "resume after this game ID")
	batchSize := flags.Int("batch-size", 100, "games per database page")
	dryRun := flags.Bool("dry-run", false, "validate without writing")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg := backfillConfig{
		GameType:            *gameType,
		EngineVersion:       *engineVersion,
		ReplaySchemaVersion: *replaySchemaVersion,
		AfterID:             *afterID,
		BatchSize:           *batchSize,
		DryRun:              *dryRun,
	}
	if cfg.GameType == "" || cfg.EngineVersion == "" {
		return errors.New("--game-type and --engine-version are required")
	}
	if cfg.ReplaySchemaVersion <= 0 || cfg.BatchSize <= 0 || cfg.BatchSize > 1000 {
		return errors.New("invalid replay schema version or batch size")
	}

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
	output, err := runBackfill(ctx, st, plugins.Registry(), cfg)
	encoded, encodeErr := json.Marshal(output)
	if encodeErr == nil {
		fmt.Println(string(encoded))
	}
	if err != nil {
		return err
	}
	return encodeErr
}

func runBackfill(
	ctx context.Context,
	st backfillStore,
	registry *game.Registry,
	cfg backfillConfig,
) (backfillOutput, error) {
	output := backfillOutput{
		GameType:      cfg.GameType,
		EngineVersion: cfg.EngineVersion,
		FailedIDs:     []int64{},
		DryRun:        cfg.DryRun,
		LastID:        cfg.AfterID,
	}
	definition, ok := registry.Get(cfg.GameType)
	if !ok || definition.LegacyReplay == nil {
		return output, fmt.Errorf("legacy replay provider is unavailable for %s", cfg.GameType)
	}

	for {
		ids, err := st.ListReplayBackfillGameIDs(
			ctx,
			cfg.GameType,
			output.LastID,
			cfg.BatchSize,
		)
		if err != nil {
			return output, err
		}
		if len(ids) == 0 {
			break
		}
		for _, gameID := range ids {
			output.Processed++
			output.LastID = gameID
			if err := backfillGame(ctx, st, definition, cfg, gameID); err != nil {
				output.FailedIDs = append(output.FailedIDs, gameID)
				observability.Error(
					"replay backfill failed",
					"gameID",
					gameID,
					"error",
					err,
				)
				continue
			}
			output.Succeeded++
		}
	}
	remaining, err := st.CountMissingReplayPayloads(ctx, cfg.GameType)
	if err != nil {
		return output, err
	}
	output.Remaining = remaining
	if len(output.FailedIDs) > 0 {
		return output, fmt.Errorf(
			"replay backfill failed for game IDs %v",
			output.FailedIDs,
		)
	}
	return output, nil
}

func backfillGame(
	ctx context.Context,
	st backfillStore,
	definition game.Definition,
	cfg backfillConfig,
	gameID int64,
) error {
	gameRecord, players, events, err := st.GetHistoryGame(
		ctx,
		gameID,
		cfg.GameType,
		true,
	)
	if err != nil {
		return err
	}
	if gameRecord.EngineVersion != "" &&
		gameRecord.EngineVersion != cfg.EngineVersion {
		return fmt.Errorf(
			"game engine version is %s, explicit mapping is %s",
			gameRecord.EngineVersion,
			cfg.EngineVersion,
		)
	}
	if gameRecord.ReplaySchemaVersion != 0 &&
		gameRecord.ReplaySchemaVersion != cfg.ReplaySchemaVersion {
		return fmt.Errorf(
			"game replay schema is %d, target is %d",
			gameRecord.ReplaySchemaVersion,
			cfg.ReplaySchemaVersion,
		)
	}
	historyPlayers := legacyHistoryPlayers(players)
	initialState, err := definition.LegacyReplay.DecodeReplayState(
		ctx,
		gameRecord.InitialState,
		nil,
		nil,
		historyPlayers,
	)
	if err != nil {
		return fmt.Errorf("decode initial state: %w", err)
	}
	initialLegacy, err := definition.LegacyReplay.BuildReplaySnapshot(
		ctx,
		initialState,
		-1,
		len(events),
		"",
		"",
		nil,
	)
	if err != nil {
		return fmt.Errorf("build initial replay: %w", err)
	}
	initialPayload, err := encodeLegacyFrame(
		cfg.ReplaySchemaVersion,
		initialLegacy,
	)
	if err != nil {
		return err
	}
	initialValidationPayload := initialPayload
	if len(gameRecord.InitialReplayPayload) > 0 {
		initialValidationPayload = gameRecord.InitialReplayPayload
	}
	if err := compareMaterializedReplay(
		cfg.ReplaySchemaVersion,
		initialValidationPayload,
		-1,
		len(events),
		nil,
		initialLegacy,
	); err != nil {
		return fmt.Errorf("validate initial frame: %w", err)
	}

	eventPayloads := make(map[int][]byte)
	for index := range events {
		event := &events[index]
		state, err := definition.LegacyReplay.DecodeReplayState(
			ctx,
			gameRecord.InitialState,
			event.StateSnapshot,
			event.StateBlob,
			historyPlayers,
		)
		if err != nil {
			return fmt.Errorf("decode event %d: %w", event.Seq, err)
		}
		var commandPayload any
		if len(event.Payload) > 0 {
			if err := json.Unmarshal(event.Payload, &commandPayload); err != nil {
				return fmt.Errorf("decode event %d payload: %w", event.Seq, err)
			}
		}
		legacy, err := definition.LegacyReplay.BuildReplaySnapshot(
			ctx,
			state,
			event.Seq,
			len(events),
			event.CommandType,
			event.PlayerID,
			commandPayload,
		)
		if err != nil {
			return fmt.Errorf("build event %d replay: %w", event.Seq, err)
		}
		payload, err := encodeLegacyFrame(cfg.ReplaySchemaVersion, legacy)
		if err != nil {
			return fmt.Errorf("encode event %d replay: %w", event.Seq, err)
		}
		validationPayload := payload
		if len(event.ReplayPayload) > 0 {
			validationPayload = event.ReplayPayload
		}
		if err := compareMaterializedReplay(
			cfg.ReplaySchemaVersion,
			validationPayload,
			event.Seq,
			len(events),
			event,
			legacy,
		); err != nil {
			return fmt.Errorf("validate event %d frame: %w", event.Seq, err)
		}
		if len(event.ReplayPayload) == 0 {
			eventPayloads[event.Seq] = payload
		}
	}
	if cfg.DryRun {
		return nil
	}
	return st.BackfillReplayPayloads(
		ctx,
		gameID,
		cfg.EngineVersion,
		cfg.ReplaySchemaVersion,
		initialPayload,
		eventPayloads,
	)
}

func encodeLegacyFrame(schemaVersion int, legacy any) ([]byte, error) {
	encoded, err := json.Marshal(legacy)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	frame := history.ReplayFrame{
		RoomData:    fields["roomData"],
		PlayersData: fields["playersData"],
		Result:      fields["result"],
	}
	return history.EncodeReplayFrame(schemaVersion, frame)
}

func compareMaterializedReplay(
	schemaVersion int,
	payload []byte,
	seq,
	total int,
	event *history.Event,
	legacy any,
) error {
	materialized, err := history.MaterializedReplayJSON(
		schemaVersion,
		payload,
		seq,
		total,
		event,
		nil,
	)
	if err != nil {
		return err
	}
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		return err
	}
	left, err := canonicalJSON(materialized)
	if err != nil {
		return err
	}
	right, err := canonicalJSON(legacyJSON)
	if err != nil {
		return err
	}
	if string(left) != string(right) {
		return errors.New("materialized replay differs from legacy replay")
	}
	return nil
}

func canonicalJSON(value []byte) ([]byte, error) {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return nil, err
	}
	return json.Marshal(decoded)
}

func legacyHistoryPlayers(players []history.Player) []game.HistoryPlayer {
	result := make([]game.HistoryPlayer, 0, len(players))
	for _, player := range players {
		userID := ""
		if player.UserID != nil {
			userID = strconv.FormatInt(*player.UserID, 10)
		}
		result = append(result, game.HistoryPlayer{
			UserID:      userID,
			PlayerID:    player.PlayerID,
			SeatIndex:   player.SeatIndex,
			IsAI:        player.IsAI,
			FinalScore:  player.FinalScore,
			FinalMoney:  player.FinalMoney,
			FinalStocks: player.FinalStocks,
			FinalRank:   player.FinalRank,
			IsWinner:    player.IsWinner,
		})
	}
	return result
}
