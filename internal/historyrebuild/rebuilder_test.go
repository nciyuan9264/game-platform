package historyrebuild

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/history"
)

type fakeStore struct {
	room           contracts.RoomRecord
	match          contracts.MatchRecord
	roster         []contracts.MatchPlayerRecord
	existing       bool
	finished       bool
	gameID         int64
	startedGame    history.Game
	startedPlayers []history.Player
	events         []history.Event
	finalized      *history.FinalizeInput
	metadataCalls  int
}

func (s *fakeStore) GetRoom(
	context.Context,
	string,
) (contracts.RoomRecord, error) {
	return s.room, nil
}

func (s *fakeStore) GetMatch(
	context.Context,
	string,
) (contracts.MatchRecord, error) {
	return s.match, nil
}

func (s *fakeStore) LatestMatchForRoom(
	context.Context,
	string,
) (contracts.MatchRecord, error) {
	return s.match, nil
}

func (s *fakeStore) ListMatchPlayers(
	context.Context,
	string,
) ([]contracts.MatchPlayerRecord, error) {
	return append([]contracts.MatchPlayerRecord(nil), s.roster...), nil
}

func (s *fakeStore) FindHistoryGameByStart(
	context.Context,
	string,
	string,
	time.Time,
) (int64, int, bool, error) {
	if !s.existing {
		return 0, 0, false, sql.ErrNoRows
	}
	return s.gameID, len(s.events), s.finished, nil
}

func (s *fakeStore) StartHistoryGame(
	_ context.Context,
	gameRecord history.Game,
	players []history.Player,
) (int64, error) {
	s.existing = true
	s.gameID = 41
	s.startedGame = gameRecord
	s.startedPlayers = append([]history.Player(nil), players...)
	return s.gameID, nil
}

func (s *fakeStore) SetHistoryReplayMetadata(
	context.Context,
	int64,
	string,
	int,
	[]byte,
) error {
	s.metadataCalls++
	return nil
}

func (s *fakeStore) AppendHistoryEvent(
	_ context.Context,
	event history.Event,
) error {
	for index := range s.events {
		if s.events[index].Seq == event.Seq {
			s.events[index] = event
			return nil
		}
	}
	s.events = append(s.events, event)
	return nil
}

func (s *fakeStore) FinishHistoryGame(
	_ context.Context,
	_ int64,
	input history.FinalizeInput,
) error {
	s.finished = true
	s.finalized = &input
	return nil
}

type fakeCommandReader struct {
	records []contracts.CommandLogRecord
}

func (r fakeCommandReader) ListCommandLog(
	string,
	int64,
) ([]contracts.CommandLogRecord, error) {
	return append([]contracts.CommandLogRecord(nil), r.records...), nil
}

type fakeState struct {
	Started   bool      `json:"started"`
	Finished  bool      `json:"finished"`
	StartedAt time.Time `json:"startedAt"`
	Moves     int       `json:"moves"`
}

type fakePlugin struct{}

func (fakePlugin) GameType() string { return "test-game" }
func (fakePlugin) Version() string  { return "test-v2" }

func (fakePlugin) InitRoom(
	context.Context,
	game.RoomConfig,
) (game.State, error) {
	return &fakeState{}, nil
}

func (fakePlugin) EncodeState(
	_ context.Context,
	state game.State,
) ([]byte, error) {
	return json.Marshal(state)
}

func (fakePlugin) DecodeState(
	_ context.Context,
	data []byte,
) (game.State, error) {
	state := &fakeState{}
	return state, json.Unmarshal(data, state)
}

func (fakePlugin) ApplyCommand(
	_ context.Context,
	state game.State,
	command contracts.RoomCommand,
) (game.ApplyResult, error) {
	typed := state.(*fakeState)
	switch command.Type {
	case "start":
		typed.Started = true
		typed.StartedAt = command.SentAt
	case "move":
		if !typed.Started || typed.Finished {
			return game.ApplyResult{}, errors.New("invalid move")
		}
		typed.Moves++
	case "finish":
		if !typed.Started || typed.Finished {
			return game.ApplyResult{}, errors.New("invalid finish")
		}
		typed.Finished = true
	default:
		return game.ApplyResult{}, errors.New("unknown command")
	}
	return game.ApplyResult{Finished: typed.Finished}, nil
}

func (fakePlugin) BuildPlayerView(
	context.Context,
	game.State,
	game.ViewRequest,
) (any, error) {
	return nil, nil
}

func (fakePlugin) ProjectHistory(
	_ context.Context,
	state game.State,
) (game.HistoryProjection, error) {
	typed := state.(*fakeState)
	if !typed.Started {
		return game.HistoryProjection{}, nil
	}
	blob, err := json.Marshal(typed)
	if err != nil {
		return game.HistoryProjection{}, err
	}
	winnerRank := 1
	loserRank := 2
	players := []game.HistoryPlayer{
		{
			PlayerID:    "human-runtime",
			DisplayName: "Human",
			SeatIndex:   0,
			GameRole:    "runner",
			IsWinner:    typed.Finished,
		},
		{
			PlayerID:    "ai_2",
			DisplayName: "Bot",
			SeatIndex:   1,
			GameRole:    "hunter",
			IsAI:        true,
		},
	}
	if typed.Finished {
		players[0].FinalRank = &winnerRank
		players[1].FinalRank = &loserRank
	}
	winner := ""
	if typed.Finished {
		winner = "human-runtime"
	}
	return game.HistoryProjection{
		Started:             true,
		Finished:            typed.Finished,
		StartedAt:           typed.StartedAt,
		MaxPlayers:          2,
		ReplaySchemaVersion: 1,
		ReplayFrame: map[string]any{
			"roomData":    map[string]any{"moves": typed.Moves},
			"playersData": map[string]any{"human-runtime": map[string]any{}},
			"result":      map[string]any{"winnerID": winner},
		},
		InitialState:    blob,
		StateBlob:       blob,
		FinalResult:     map[string]any{"winnerID": winner},
		WinnerPlayerID:  winner,
		RoomDisplayName: "Recovered Room",
		Players:         players,
	}, nil
}

func (fakePlugin) IsRecordableCommand(commandType string) bool {
	return commandType == "move" || commandType == "finish"
}

func testDefinition() game.Definition {
	plugin := fakePlugin{}
	return game.Definition{
		GameType:          plugin.GameType(),
		Version:           plugin.Version(),
		DefaultMaxPlayers: 2,
		Engine:            plugin,
		History:           plugin,
	}
}

func rebuildFixture() (*fakeStore, fakeCommandReader, time.Time) {
	startedAt := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	room := contracts.RoomRecord{
		RoomID:         "room-1",
		GameType:       "test-game",
		Visibility:     contracts.VisibilityPrivate,
		DisplayName:    "Recovered Room",
		OwnerUserID:    "101",
		OwnerPlayerID:  "human-runtime",
		CurrentMatchID: "match-1",
	}
	match := contracts.MatchRecord{
		MatchID:            "match-1",
		RuntimeRoomID:      room.RoomID,
		GameType:           room.GameType,
		DisplayName:        room.DisplayName,
		TurnTimeoutSeconds: 60,
		EngineVersion:      "test-v1",
		MaxPlayers:         2,
		RoundNo:            1,
		LastAppliedSeq:     3,
		RandomSeed:         42,
		StartedAt:          startedAt,
		EndedAt:            startedAt.Add(5 * time.Minute),
	}
	store := &fakeStore{
		room:  room,
		match: match,
		roster: []contracts.MatchPlayerRecord{
			{
				UserID:      "101",
				PlayerID:    "human-runtime",
				DisplayName: "Alice",
				SeatIndex:   0,
			},
			{
				PlayerID:    "ai_2",
				DisplayName: "Grey Fox",
				SeatIndex:   1,
				IsAI:        true,
			},
		},
	}
	reader := fakeCommandReader{records: []contracts.CommandLogRecord{
		{
			Kind:      contracts.CommandLogKindCommand,
			RoomID:    room.RoomID,
			MatchID:   match.MatchID,
			Seq:       1,
			CommandID: "cmd-1",
			PlayerID:  "human-runtime",
			Type:      "start",
			CreatedAt: startedAt,
		},
		{
			Kind:      contracts.CommandLogKindCommand,
			RoomID:    room.RoomID,
			MatchID:   match.MatchID,
			Seq:       2,
			CommandID: "cmd-2",
			PlayerID:  "human-runtime",
			Type:      "move",
			Payload:   map[string]any{"value": 1},
			CreatedAt: startedAt.Add(time.Minute),
		},
		{
			Kind:      contracts.CommandLogKindCommand,
			RoomID:    room.RoomID,
			MatchID:   match.MatchID,
			Seq:       3,
			CommandID: "cmd-3",
			PlayerID:  "human-runtime",
			Type:      "finish",
			CreatedAt: startedAt.Add(2 * time.Minute),
		},
	}}
	return store, reader, startedAt
}

func TestRebuilderDryRunAndIdempotentApply(t *testing.T) {
	store, reader, startedAt := rebuildFixture()
	rebuilder := New(store, reader, testDefinition(), "test-v1")

	dryRun, err := rebuilder.Run(context.Background(), Config{RoomID: "room-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !dryRun.DryRun || dryRun.Applied || dryRun.CommandCount != 3 ||
		dryRun.EventCount != 2 || dryRun.StartedAt != startedAt ||
		len(store.events) != 0 || store.finalized != nil {
		t.Fatalf("dry run = %+v, store = %+v", dryRun, store)
	}

	applied, err := rebuilder.Run(
		context.Background(),
		Config{RoomID: "room-1", Apply: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !applied.Applied || applied.GameID != 41 ||
		store.startedGame.EngineVersion != "test-v1" ||
		len(store.startedPlayers) != 2 || len(store.events) != 2 ||
		store.finalized == nil || store.finalized.WinnerUserID == nil ||
		*store.finalized.WinnerUserID != 101 ||
		store.finalized.WinnerPlayerID != "101" {
		t.Fatalf("applied = %+v, store = %+v", applied, store)
	}
	if store.events[0].Seq != 0 || store.events[1].Seq != 1 {
		t.Fatalf("events = %+v", store.events)
	}

	again, err := rebuilder.Run(
		context.Background(),
		Config{RoomID: "room-1", Apply: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !again.AlreadyCompleted || again.Applied ||
		len(store.events) != 2 || store.metadataCalls != 0 {
		t.Fatalf("second apply = %+v, store = %+v", again, store)
	}
}

func TestRebuilderRejectsCommandLogGap(t *testing.T) {
	store, reader, _ := rebuildFixture()
	reader.records = append(reader.records[:1], reader.records[2:]...)
	rebuilder := New(store, reader, testDefinition(), "test-v1")
	_, err := rebuilder.Run(context.Background(), Config{RoomID: "room-1"})
	if err == nil {
		t.Fatal("expected command log gap error")
	}
}

func TestValidatedCommandsAllowsLogAheadOfDurableHead(t *testing.T) {
	store, reader, _ := rebuildFixture()
	store.match.LastAppliedSeq = 2
	commands, err := validatedCommands(
		store.room.RoomID,
		store.match,
		reader.records,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 3 {
		t.Fatalf("commands = %d", len(commands))
	}
}

func TestRebuilderReportsExpiredCommandLog(t *testing.T) {
	store, _, _ := rebuildFixture()
	store.match.LastAppliedSeq = 0
	rebuilder := New(store, fakeCommandReader{}, testDefinition(), "test-v1")
	_, err := rebuilder.Run(context.Background(), Config{RoomID: "room-1"})
	if !errors.Is(err, ErrCommandLogUnavailable) {
		t.Fatalf("error = %v", err)
	}
}
