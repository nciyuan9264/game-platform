package davinci

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestDavinciHistoryStartsDeterministically(t *testing.T) {
	ctx := context.Background()
	plugin := NewPlugin()
	raw, err := plugin.InitRoom(ctx, game.RoomConfig{
		RoomID:        "davinci-history",
		OwnerPlayerID: "137",
		MaxPlayers:    2,
		RandomSeed:    42,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := raw.(*State)
	startedAt := time.Date(2026, time.October, 24, 12, 30, 0, 0, time.UTC)
	commands := []contracts.RoomCommand{
		{PlayerID: "137", Type: CommandConnect},
		{PlayerID: "137", Type: CommandAddAI, Payload: map[string]any{"seatIndex": 1}},
		{PlayerID: "137", Type: CommandMatchBegin},
		{PlayerID: "137", Type: CommandInitTimeout, SentAt: startedAt, Replay: true},
		{PlayerID: "137", Type: CommandGameReady, SentAt: startedAt, Replay: true},
	}
	for _, command := range commands {
		if _, err := plugin.ApplyCommand(ctx, state, command); err != nil {
			t.Fatalf("%s: %v", command.Type, err)
		}
	}

	if !state.GameStartTime.Equal(startedAt) {
		t.Fatalf("game start time = %s, want %s", state.GameStartTime, startedAt)
	}
	if _, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{
		PlayerID: "137",
		Type:     CommandGameReady,
		SentAt:   startedAt.Add(time.Hour),
		Replay:   true,
	}); err == nil {
		t.Fatal("second game_ready was accepted")
	}
	if !state.GameStartTime.Equal(startedAt) {
		t.Fatalf("second game_ready changed start time to %s", state.GameStartTime)
	}

	projection, err := plugin.ProjectHistory(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	if !projection.Started || projection.Finished || projection.AbandonedEligible {
		t.Fatalf("history lifecycle = %+v", projection)
	}
	if !projection.StartedAt.Equal(startedAt) || projection.MaxPlayers != 2 {
		t.Fatalf("history metadata = %+v", projection)
	}
	if projection.ReplaySchemaVersion != davinciHistorySchemaVersion ||
		len(projection.InitialState) == 0 ||
		len(projection.StateBlob) == 0 ||
		projection.ReplayFrame == nil {
		t.Fatalf("history materialization = %+v", projection)
	}
	if len(projection.Players) != 2 {
		t.Fatalf("history players = %+v", projection.Players)
	}
	for _, player := range projection.Players {
		if player.UserID != "" {
			t.Fatalf("history player inferred user ID: %+v", player)
		}
	}

	encoded, err := plugin.EncodeState(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	decodedRaw, err := plugin.DecodeState(ctx, encoded)
	if err != nil {
		t.Fatal(err)
	}
	decoded := decodedRaw.(*State)
	if !decoded.GameStartTime.Equal(startedAt) {
		t.Fatalf("decoded start time = %s, want %s", decoded.GameStartTime, startedAt)
	}
	reencoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(encoded, reencoded) {
		t.Fatalf("snapshot round trip changed state:\n%s\n%s", encoded, reencoded)
	}
}

func TestDavinciHistoryExposesRuntimeRandomSeed(t *testing.T) {
	ctx := context.Background()
	plugin := NewPlugin()
	const seed = uint64(42)
	raw, err := plugin.InitRoom(ctx, game.RoomConfig{
		RoomID:        "davinci-history-seed",
		OwnerPlayerID: "137",
		MaxPlayers:    2,
		RandomSeed:    seed,
	})
	if err != nil {
		t.Fatal(err)
	}

	provider, ok := any(plugin).(game.RandomSeedProvider)
	if !ok {
		t.Fatal("davinci plugin does not implement game.RandomSeedProvider")
	}
	got, err := provider.RandomSeed(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != seed {
		t.Fatalf("random seed = %d, want %d", got, seed)
	}
}

func TestDavinciHistoryProjectsWinnerAndRanks(t *testing.T) {
	startedAt := time.Date(2026, time.October, 24, 12, 30, 0, 0, time.UTC)
	state := &State{
		RoomID:        "davinci-finished",
		Status:        StatusEnd,
		MaxPlayers:    2,
		WinnerID:      "137",
		GameStartTime: startedAt,
		PlayerSeq:     []string{"137", "AI_2"},
		Players: map[string]*Player{
			"137": {
				PlayerID:  "137",
				SeatIndex: 0,
				Cards: []Card{
					{ID: "human-hidden", IsRevealed: false},
					{ID: "human-revealed", IsRevealed: true},
				},
			},
			"AI_2": {
				PlayerID:  "AI_2",
				AI:        true,
				SeatIndex: 1,
				Cards:     []Card{{ID: "ai-revealed", IsRevealed: true}},
			},
		},
	}
	projection, err := NewPlugin().ProjectHistory(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if !projection.Started || !projection.Finished ||
		projection.WinnerPlayerID != "137" ||
		!projection.StartedAt.Equal(startedAt) {
		t.Fatalf("finished projection = %+v", projection)
	}
	if len(projection.Players) != 2 {
		t.Fatalf("players = %+v", projection.Players)
	}
	human, ai := projection.Players[0], projection.Players[1]
	if !human.IsWinner || human.FinalRank == nil || *human.FinalRank != 1 {
		t.Fatalf("human result = %+v", human)
	}
	if ai.IsWinner || !ai.IsAI || ai.FinalRank == nil || *ai.FinalRank != 2 {
		t.Fatalf("AI result = %+v", ai)
	}
}

func TestDavinciHistoryCommandClassification(t *testing.T) {
	plugin := NewPlugin()
	for _, commandType := range []string{
		CommandGetCard,
		CommandGuessCard,
		CommandSetCard,
		CommandTurnTimeout,
	} {
		if !plugin.IsRecordableCommand(commandType) {
			t.Fatalf("%s should be recordable", commandType)
		}
	}
	for _, commandType := range []string{
		CommandConnect,
		CommandDisconnect,
		CommandMatchBegin,
		CommandGameReady,
		CommandAddAI,
		CommandInitialize,
		CommandInitTimeout,
	} {
		if plugin.IsRecordableCommand(commandType) {
			t.Fatalf("%s should not be recordable", commandType)
		}
	}
	if definition := Definition(); definition.Version != "davinci-v5" || definition.History == nil {
		t.Fatalf("definition = %+v", definition)
	}
}

func TestDavinciInitializationDoesNotStartHistory(t *testing.T) {
	state := &State{
		RoomID:     "initializing-history",
		Status:     StatusInitialize,
		MaxPlayers: 2,
		PlayerSeq:  []string{"137", "AI_2"},
		Players: map[string]*Player{
			"137":  {PlayerID: "137"},
			"AI_2": {PlayerID: "AI_2", AI: true},
		},
	}
	projection, err := NewPlugin().ProjectHistory(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Started {
		t.Fatalf("initialization created history: %+v", projection)
	}
}

func TestDavinciV3SnapshotWithoutStartTimeStillDecodes(t *testing.T) {
	raw, err := NewPlugin().DecodeState(context.Background(), []byte(`{
		"roomID":"legacy-room",
		"ownerID":"1",
		"status":"waiting",
		"maxPlayers":2,
		"players":{},
		"randomSeed":1
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if state := raw.(*State); !state.GameStartTime.IsZero() {
		t.Fatalf("legacy start time = %s, want zero", state.GameStartTime)
	}
}
