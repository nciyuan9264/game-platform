package historyrebuild

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/gamelog"
	"github.com/nciyuan9264/game-platform/internal/history"
)

var ErrCommandLogUnavailable = errors.New(
	"command log is unavailable or has expired",
)

type Store interface {
	GetRoom(context.Context, string) (contracts.RoomRecord, error)
	GetMatch(context.Context, string) (contracts.MatchRecord, error)
	LatestMatchForRoom(context.Context, string) (contracts.MatchRecord, error)
	ListMatchPlayers(context.Context, string) ([]contracts.MatchPlayerRecord, error)
	FindHistoryGameByStart(
		context.Context,
		string,
		string,
		time.Time,
	) (int64, int, bool, error)
	StartHistoryGame(
		context.Context,
		history.Game,
		[]history.Player,
	) (int64, error)
	SetHistoryReplayMetadata(
		context.Context,
		int64,
		string,
		int,
		[]byte,
	) error
	AppendHistoryEvent(context.Context, history.Event) error
	FinishHistoryGame(context.Context, int64, history.FinalizeInput) error
}

type CommandLogReader interface {
	ListCommandLog(string, int64) ([]contracts.CommandLogRecord, error)
}

type Config struct {
	RoomID  string
	MatchID string
	Apply   bool
}

type PlayerSummary struct {
	PlayerID    string `json:"playerID"`
	UserID      *int64 `json:"userID,omitempty"`
	DisplayName string `json:"displayName"`
	GameRole    string `json:"gameRole"`
	IsAI        bool   `json:"isAI"`
	IsWinner    bool   `json:"isWinner"`
	FinalRank   *int   `json:"finalRank,omitempty"`
}

type Result struct {
	DryRun             bool            `json:"dryRun"`
	Applied            bool            `json:"applied"`
	AlreadyCompleted   bool            `json:"alreadyCompleted"`
	RoomID             string          `json:"roomID"`
	MatchID            string          `json:"matchID"`
	EngineVersion      string          `json:"engineVersion"`
	GameID             int64           `json:"gameID,omitempty"`
	ExistingEventCount int             `json:"existingEventCount,omitempty"`
	CommandCount       int             `json:"commandCount"`
	EventCount         int             `json:"eventCount"`
	StartedAt          time.Time       `json:"startedAt"`
	EndedAt            time.Time       `json:"endedAt"`
	WinnerPlayerID     string          `json:"winnerPlayerID"`
	Players            []PlayerSummary `json:"players"`
}

type Rebuilder struct {
	store              Store
	commands           CommandLogReader
	definition         game.Definition
	compatibleVersions map[string]struct{}
}

func New(
	store Store,
	commands CommandLogReader,
	definition game.Definition,
	compatibleVersions ...string,
) *Rebuilder {
	versions := map[string]struct{}{definition.Version: {}}
	for _, version := range compatibleVersions {
		if version != "" {
			versions[version] = struct{}{}
		}
	}
	return &Rebuilder{
		store:              store,
		commands:           commands,
		definition:         definition,
		compatibleVersions: versions,
	}
}

type rebuildPlan struct {
	room                 contracts.RoomRecord
	match                contracts.MatchRecord
	initialProjection    game.HistoryProjection
	finalProjection      game.HistoryProjection
	initialReplayPayload []byte
	initialPlayers       []history.Player
	finalPlayers         []history.Player
	events               []history.Event
	endedAt              time.Time
	durationSeconds      int
	winnerUserID         *int64
	winnerPlayerID       string
	finalResult          json.RawMessage
}

func (r *Rebuilder) Run(
	ctx context.Context,
	cfg Config,
) (Result, error) {
	result := Result{
		DryRun:  !cfg.Apply,
		RoomID:  cfg.RoomID,
		Players: []PlayerSummary{},
	}
	if r.store == nil || r.commands == nil ||
		r.definition.Engine == nil || r.definition.History == nil {
		return result, errors.New("history rebuilder is not fully configured")
	}
	if cfg.RoomID == "" {
		return result, errors.New("room ID is required")
	}
	room, err := r.store.GetRoom(ctx, cfg.RoomID)
	if err != nil {
		return result, fmt.Errorf("load room: %w", err)
	}
	match, err := r.loadMatch(ctx, room, cfg.MatchID)
	if err != nil {
		return result, err
	}
	result.MatchID = match.MatchID
	result.EngineVersion = match.EngineVersion
	if err := r.validateMatch(room, match); err != nil {
		return result, err
	}
	roster, err := r.store.ListMatchPlayers(ctx, match.MatchID)
	if err != nil {
		return result, fmt.Errorf("load match players: %w", err)
	}
	records, err := r.commands.ListCommandLog(match.MatchID, 0)
	if err != nil {
		return result, fmt.Errorf("load command log: %w", err)
	}
	plan, commandCount, err := r.buildPlan(
		ctx,
		room,
		match,
		roster,
		records,
	)
	if err != nil {
		return result, err
	}
	result.CommandCount = commandCount
	result.EventCount = len(plan.events)
	result.StartedAt = plan.initialProjection.StartedAt
	result.EndedAt = plan.endedAt
	result.WinnerPlayerID = plan.winnerPlayerID
	result.Players = summarizePlayers(plan.finalPlayers)

	gameID, nextSeq, finished, err := r.store.FindHistoryGameByStart(
		ctx,
		room.RoomID,
		room.GameType,
		plan.initialProjection.StartedAt,
	)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("find existing history game: %w", err)
	}
	if err == nil {
		result.GameID = gameID
		result.ExistingEventCount = nextSeq
		result.AlreadyCompleted = finished
		if finished || !cfg.Apply {
			return result, nil
		}
	}
	if !cfg.Apply {
		return result, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		gameID, err = r.store.StartHistoryGame(
			ctx,
			history.Game{
				RoomID:               room.RoomID,
				GameType:             room.GameType,
				EngineVersion:        match.EngineVersion,
				ReplaySchemaVersion:  plan.initialProjection.ReplaySchemaVersion,
				StartedAt:            plan.initialProjection.StartedAt,
				MaxPlayers:           plan.initialProjection.MaxPlayers,
				InitialState:         json.RawMessage(plan.initialProjection.InitialState),
				InitialReplayPayload: plan.initialReplayPayload,
			},
			plan.initialPlayers,
		)
		if err != nil {
			return result, fmt.Errorf("start history game: %w", err)
		}
		result.GameID = gameID
	} else {
		if err := r.store.SetHistoryReplayMetadata(
			ctx,
			gameID,
			match.EngineVersion,
			plan.initialProjection.ReplaySchemaVersion,
			plan.initialReplayPayload,
		); err != nil {
			return result, fmt.Errorf("set history replay metadata: %w", err)
		}
	}
	for _, event := range plan.events {
		event.GameID = gameID
		if err := r.store.AppendHistoryEvent(ctx, event); err != nil {
			return result, fmt.Errorf(
				"append history event %d: %w",
				event.Seq,
				err,
			)
		}
	}
	if err := r.store.FinishHistoryGame(
		ctx,
		gameID,
		history.FinalizeInput{
			EndedAt:         plan.endedAt,
			DurationSeconds: plan.durationSeconds,
			WinnerUserID:    plan.winnerUserID,
			WinnerPlayerID:  plan.winnerPlayerID,
			EndReason:       history.EndReasonCompleted,
			FinalResult:     plan.finalResult,
			Players:         plan.finalPlayers,
		},
	); err != nil {
		return result, fmt.Errorf("finish history game: %w", err)
	}
	result.Applied = true
	return result, nil
}

func (r *Rebuilder) loadMatch(
	ctx context.Context,
	room contracts.RoomRecord,
	matchID string,
) (contracts.MatchRecord, error) {
	if matchID == "" {
		matchID = room.CurrentMatchID
	}
	if matchID != "" {
		match, err := r.store.GetMatch(ctx, matchID)
		if err != nil {
			return contracts.MatchRecord{}, fmt.Errorf("load match: %w", err)
		}
		return match, nil
	}
	match, err := r.store.LatestMatchForRoom(ctx, room.RoomID)
	if err != nil {
		return contracts.MatchRecord{}, fmt.Errorf("load latest room match: %w", err)
	}
	return match, nil
}

func (r *Rebuilder) validateMatch(
	room contracts.RoomRecord,
	match contracts.MatchRecord,
) error {
	if room.GameType != r.definition.GameType ||
		match.GameType != r.definition.GameType {
		return fmt.Errorf(
			"game type mismatch: room=%s match=%s rebuilder=%s",
			room.GameType,
			match.GameType,
			r.definition.GameType,
		)
	}
	if match.RuntimeRoomID != room.RoomID {
		return fmt.Errorf(
			"match %s belongs to room %s, not %s",
			match.MatchID,
			match.RuntimeRoomID,
			room.RoomID,
		)
	}
	if _, ok := r.compatibleVersions[match.EngineVersion]; !ok {
		return fmt.Errorf(
			"engine version %q is not supported by this rebuilder",
			match.EngineVersion,
		)
	}
	if match.RandomSeed == 0 {
		return errors.New("match random seed is missing")
	}
	return nil
}

func (r *Rebuilder) buildPlan(
	ctx context.Context,
	room contracts.RoomRecord,
	match contracts.MatchRecord,
	roster []contracts.MatchPlayerRecord,
	records []contracts.CommandLogRecord,
) (rebuildPlan, int, error) {
	plan := rebuildPlan{room: room, match: match, events: []history.Event{}}
	state, err := r.definition.Engine.InitRoom(ctx, game.RoomConfig{
		RoomID:             room.RoomID,
		GameType:           room.GameType,
		OwnerUserID:        room.OwnerUserID,
		OwnerPlayerID:      room.OwnerPlayerID,
		MaxPlayers:         match.MaxPlayers,
		RandomSeed:         match.RandomSeed,
		RoundNo:            match.RoundNo,
		Visibility:         room.Visibility,
		TurnTimeoutSeconds: match.TurnTimeoutSeconds,
	})
	if err != nil {
		return plan, 0, fmt.Errorf("initialize replay state: %w", err)
	}
	commands, err := validatedCommands(room.RoomID, match, records)
	if err != nil {
		return plan, 0, err
	}
	if len(commands) == 0 {
		return plan, 0, fmt.Errorf(
			"%w for match %s",
			ErrCommandLogUnavailable,
			match.MatchID,
		)
	}
	applyContext := gamelog.WithSink(ctx, gamelog.NopSink{})
	var lastProjection game.HistoryProjection
	for _, record := range commands {
		command := contracts.RoomCommand{
			SchemaVersion: 1,
			CommandID:     record.CommandID,
			RoomID:        record.RoomID,
			MatchID:       record.MatchID,
			GameType:      room.GameType,
			Epoch:         record.Epoch,
			PlayerID:      record.PlayerID,
			Type:          record.Type,
			Payload:       record.Payload,
			SentAt:        record.CreatedAt,
			Replay:        true,
		}
		if _, err := r.definition.Engine.ApplyCommand(
			applyContext,
			state,
			command,
		); err != nil {
			return plan, 0, fmt.Errorf(
				"replay command seq %d (%s): %w",
				record.Seq,
				record.Type,
				err,
			)
		}
		projection, err := r.definition.History.ProjectHistory(ctx, state)
		if err != nil {
			return plan, 0, fmt.Errorf(
				"project history after command seq %d: %w",
				record.Seq,
				err,
			)
		}
		if !projection.Started {
			continue
		}
		replayPayload, err := history.EncodeReplayFrame(
			projection.ReplaySchemaVersion,
			projection.ReplayFrame,
		)
		if err != nil {
			return plan, 0, fmt.Errorf(
				"encode history after command seq %d: %w",
				record.Seq,
				err,
			)
		}
		if !plan.initialProjection.Started {
			plan.initialProjection = projection
			plan.initialReplayPayload = replayPayload
			plan.initialPlayers, err = materializePlayers(
				projection.Players,
				roster,
			)
			if err != nil {
				return plan, 0, err
			}
		}
		if r.definition.History.IsRecordableCommand(record.Type) {
			payload, err := json.Marshal(record.Payload)
			if err != nil {
				return plan, 0, fmt.Errorf(
					"encode command seq %d payload: %w",
					record.Seq,
					err,
				)
			}
			plan.events = append(plan.events, history.Event{
				Seq:           len(plan.events),
				OccurredAt:    record.CreatedAt,
				PlayerID:      record.PlayerID,
				CommandType:   record.Type,
				Payload:       payload,
				StateBlob:     projection.StateBlob,
				ReplayPayload: replayPayload,
			})
		}
		lastProjection = projection
	}
	if !plan.initialProjection.Started {
		return plan, 0, errors.New("replayed match never reached a started state")
	}
	if !lastProjection.Finished {
		return plan, 0, errors.New("replayed match did not reach a finished state")
	}
	plan.finalProjection = lastProjection
	plan.finalPlayers, err = materializePlayers(lastProjection.Players, roster)
	if err != nil {
		return plan, 0, err
	}
	plan.finalResult, err = json.Marshal(lastProjection.FinalResult)
	if err != nil {
		return plan, 0, fmt.Errorf("encode final result: %w", err)
	}
	for _, player := range plan.finalPlayers {
		if player.IsWinner {
			plan.winnerUserID = player.UserID
			plan.winnerPlayerID = player.PlayerID
			break
		}
	}
	if plan.winnerPlayerID == "" {
		return plan, 0, errors.New("finished projection does not identify a winner")
	}
	plan.endedAt = match.EndedAt
	if plan.endedAt.IsZero() {
		plan.endedAt = commands[len(commands)-1].CreatedAt
	}
	if plan.endedAt.Before(plan.initialProjection.StartedAt) {
		return plan, 0, errors.New("match ended before its projected start time")
	}
	plan.durationSeconds = int(
		plan.endedAt.Sub(plan.initialProjection.StartedAt).Seconds(),
	)
	return plan, len(commands), nil
}

func validatedCommands(
	roomID string,
	match contracts.MatchRecord,
	records []contracts.CommandLogRecord,
) ([]contracts.CommandLogRecord, error) {
	commands := make([]contracts.CommandLogRecord, 0, len(records))
	expectedSeq := int64(1)
	var lastStreamSequence uint64
	commandIDs := make(map[string]int64)
	for _, record := range records {
		if record.StreamSequence > 0 {
			if lastStreamSequence > 0 &&
				record.StreamSequence <= lastStreamSequence {
				return nil, errors.New("command log physical order regressed")
			}
			lastStreamSequence = record.StreamSequence
		}
		if record.RoomID != roomID || record.MatchID != match.MatchID {
			return nil, fmt.Errorf(
				"command log identity mismatch at stream sequence %d",
				record.StreamSequence,
			)
		}
		if record.Kind == contracts.CommandLogKindOwnerFence {
			continue
		}
		if record.Kind != "" && record.Kind != contracts.CommandLogKindCommand {
			return nil, fmt.Errorf("unknown command log kind %q", record.Kind)
		}
		if record.Seq != expectedSeq {
			return nil, fmt.Errorf(
				"command log gap: got seq %d, want %d",
				record.Seq,
				expectedSeq,
			)
		}
		if record.CommandID == "" || record.Type == "" || record.CreatedAt.IsZero() {
			return nil, fmt.Errorf("command log seq %d is incomplete", record.Seq)
		}
		if previous, exists := commandIDs[record.CommandID]; exists {
			return nil, fmt.Errorf(
				"command ID %s appears at seq %d and %d",
				record.CommandID,
				previous,
				record.Seq,
			)
		}
		commandIDs[record.CommandID] = record.Seq
		commands = append(commands, record)
		expectedSeq++
	}
	if match.LastAppliedSeq > 0 && expectedSeq-1 < match.LastAppliedSeq {
		return nil, fmt.Errorf(
			"command log incomplete: restored seq %d, durable head %d",
			expectedSeq-1,
			match.LastAppliedSeq,
		)
	}
	return commands, nil
}

func materializePlayers(
	players []game.HistoryPlayer,
	roster []contracts.MatchPlayerRecord,
) ([]history.Player, error) {
	rosterByPlayerID := make(
		map[string]contracts.MatchPlayerRecord,
		len(roster),
	)
	for _, player := range roster {
		rosterByPlayerID[player.PlayerID] = player
	}
	result := make([]history.Player, 0, len(players))
	for _, player := range players {
		identity, found := rosterByPlayerID[player.PlayerID]
		playerID := player.PlayerID
		displayName := player.DisplayName
		var userID *int64
		if player.IsAI {
			if found && identity.DisplayName != "" && identity.DisplayName != "AI" {
				displayName = identity.DisplayName
			}
			if displayName == "" || displayName == "AI" {
				displayName = player.PlayerID
			}
		} else {
			if !found || identity.IsAI {
				return nil, fmt.Errorf(
					"human history player %q is missing from match roster",
					player.PlayerID,
				)
			}
			parsed, err := strconv.ParseInt(identity.UserID, 10, 64)
			if err != nil || parsed <= 0 {
				return nil, fmt.Errorf(
					"human history player %q has invalid user ID %q",
					player.PlayerID,
					identity.UserID,
				)
			}
			userID = &parsed
			playerID = identity.UserID
			if identity.DisplayName != "" {
				displayName = identity.DisplayName
			} else if identity.Name != "" {
				displayName = identity.Name
			}
		}
		result = append(result, history.Player{
			UserID:         userID,
			PlayerID:       playerID,
			DisplayName:    displayName,
			SeatIndex:      player.SeatIndex,
			IsAI:           player.IsAI,
			FinalScore:     player.FinalScore,
			FinalMoney:     player.FinalMoney,
			FinalStocks:    player.FinalStocks,
			FinalRank:      player.FinalRank,
			IsWinner:       player.IsWinner,
			SourcePlayerID: player.PlayerID,
			GameRole:       player.GameRole,
		})
	}
	return result, nil
}

func summarizePlayers(players []history.Player) []PlayerSummary {
	result := make([]PlayerSummary, 0, len(players))
	for _, player := range players {
		result = append(result, PlayerSummary{
			PlayerID:    player.PlayerID,
			UserID:      player.UserID,
			DisplayName: player.DisplayName,
			GameRole:    player.GameRole,
			IsAI:        player.IsAI,
			IsWinner:    player.IsWinner,
			FinalRank:   player.FinalRank,
		})
	}
	return result
}
