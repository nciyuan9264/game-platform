package acquire

import (
	"context"
	"fmt"
	"testing"

	"github.com/nciyuan9264/game-platform-server/internal/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform-server/internal/contracts"
	"github.com/nciyuan9264/game-platform-server/internal/game"
)

const testRandomSeed = uint64(0x123456789abcdef0)

func TestPluginRunsMatchAndBuildsPrivateView(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	state, err := plugin.InitRoom(ctx, game.RoomConfig{RoomID: "acquire-room", OwnerPlayerID: "alice-1", MaxPlayers: 2, RandomSeed: testRandomSeed})
	if err != nil {
		t.Fatalf("InitRoom() error = %v", err)
	}
	apply := func(playerID, commandType string, payload any) game.ApplyResult {
		t.Helper()
		result, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{PlayerID: playerID, Type: commandType, Payload: payload})
		if err != nil {
			t.Fatalf("ApplyCommand(%s) error = %v", commandType, err)
		}
		return result
	}
	apply("alice-1", "connect", nil)
	apply("bob-2", "connect", nil)
	apply("bob-2", "match_ready", map[string]any{"ready": true})
	if got := apply("alice-1", "match_begin", nil).Status; got != contracts.RoomStatusPlaying {
		t.Fatalf("match_begin status = %q", got)
	}
	apply("alice-1", "game_ready", nil)
	apply("alice-1", "game_ready", nil) // reconnect/mount retry is idempotent

	view, err := plugin.BuildPlayerView(ctx, state, game.ViewRequest{RoomID: "acquire-room", PlayerID: "alice-1", Seq: 4})
	if err != nil {
		t.Fatalf("BuildPlayerView() error = %v", err)
	}
	payload := view.(map[string]any)
	if payload["type"] != contracts.MessageTypeRoomSync || payload["playerData"] == nil {
		t.Fatalf("unexpected view: %+v", payload)
	}
	pending, err := plugin.PendingCommands(ctx, state)
	if err != nil {
		t.Fatalf("PendingCommands() error = %v", err)
	}
	if len(pending) != 1 || pending[0].PlayerID != "alice-1" {
		t.Fatalf("unexpected pending commands: %+v", pending)
	}
	projection, err := plugin.ProjectHistory(ctx, state)
	if err != nil {
		t.Fatalf("ProjectHistory() error = %v", err)
	}
	if !projection.Started || len(projection.StateBlob) == 0 || len(projection.Players) != 2 {
		t.Fatalf("unexpected history projection: %+v", projection)
	}
	spectatorView, err := plugin.BuildSpectatorView(ctx, state, 6)
	if err != nil {
		t.Fatalf("BuildSpectatorView() error = %v", err)
	}
	spectatorPayload := spectatorView.(map[string]any)
	playersData := spectatorPayload["playersData"].(map[string]any)
	if playersData["alice-1"] == nil || playersData["bob-2"] == nil {
		t.Fatalf("spectator view omitted private player state: %+v", playersData)
	}
}

func TestPluginSnapshotRoundTripAndAudioEffect(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	state, err := plugin.InitRoom(ctx, game.RoomConfig{RoomID: "acquire-room", OwnerPlayerID: "alice-1", RandomSeed: testRandomSeed})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{PlayerID: "alice-1", Type: "connect"}); err != nil {
		t.Fatal(err)
	}
	result, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{
		PlayerID: "alice-1",
		Type:     "game_play_audio",
		Payload:  map[string]any{"audioType": "quickily"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Effects) != 1 || result.Effects[0].Audience != game.AudienceAll {
		t.Fatalf("unexpected audio effects: %+v", result.Effects)
	}
	encoded, err := plugin.EncodeState(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := plugin.DecodeState(ctx, encoded)
	if err != nil {
		t.Fatal(err)
	}
	room, err := asRoom(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if room.ID != "acquire-room" || room.Connections["alice-1"] == nil {
		t.Fatalf("unexpected decoded room: %+v", room)
	}
}

func TestPluginUsesProvidedRandomSeed(t *testing.T) {
	const seed = uint64(0x9a7b6c5d4e3f2011)
	plugin := &Plugin{}
	if _, err := plugin.InitRoom(context.Background(), game.RoomConfig{RoomID: "missing-seed"}); err == nil {
		t.Fatal("room initialized without a random seed")
	}
	state, err := plugin.InitRoom(context.Background(), game.RoomConfig{
		RoomID:        "secure-seed-room",
		OwnerPlayerID: "alice-1",
		RandomSeed:    seed,
	})
	if err != nil {
		t.Fatal(err)
	}
	room, err := asRoom(state)
	if err != nil {
		t.Fatal(err)
	}
	if room.State.RandomSeed != seed {
		t.Fatalf("random seed = %d, want %d", room.State.RandomSeed, seed)
	}
}

func TestOwnerOnlyCommandsAreEnforcedByPlugin(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	state, err := plugin.InitRoom(ctx, game.RoomConfig{RoomID: "owner-check", OwnerPlayerID: "alice-1", MaxPlayers: 2, RandomSeed: testRandomSeed})
	if err != nil {
		t.Fatal(err)
	}
	apply := func(playerID, commandType string, payload any) error {
		_, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{
			PlayerID: playerID,
			Type:     commandType,
			Payload:  payload,
		})
		return err
	}
	if err := apply("alice-1", "connect", nil); err != nil {
		t.Fatal(err)
	}
	if err := apply("bob-2", "connect", nil); err != nil {
		t.Fatal(err)
	}
	if err := apply("bob-2", "match_ready", map[string]any{"ready": true}); err != nil {
		t.Fatal(err)
	}
	if err := apply("bob-2", "match_begin", nil); err == nil {
		t.Fatal("non-owner started the game")
	}
	if err := apply("alice-1", "match_begin", nil); err != nil {
		t.Fatal(err)
	}
	room, err := asRoom(state)
	if err != nil {
		t.Fatal(err)
	}
	room.State.RoomStatus = domain.RoomStatusEnd
	if err := apply("bob-2", "game_restart_game", nil); err == nil {
		t.Fatal("non-owner restarted the game")
	}
	if err := apply("alice-1", "game_restart_game", nil); err == nil {
		t.Fatal("terminal room was restarted in place")
	}
}

func TestRosterRemainsStableAcrossReconnectAndIncludesAI(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	state, err := plugin.InitRoom(ctx, game.RoomConfig{RoomID: "acquire-room", OwnerPlayerID: "alice-1", MaxPlayers: 2, RandomSeed: testRandomSeed})
	if err != nil {
		t.Fatal(err)
	}
	apply := func(playerID, commandType string) {
		t.Helper()
		if _, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{PlayerID: playerID, Type: commandType}); err != nil {
			t.Fatalf("ApplyCommand(%s) error = %v", commandType, err)
		}
	}
	apply("alice-1", "connect")
	apply("alice-1", "match_add_ai")
	apply("alice-1", "match_begin")
	apply("alice-1", "game_ready")
	apply("alice-1", "disconnect")
	apply("alice-1", "connect")

	roster, err := plugin.Roster(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(roster) != 2 {
		t.Fatalf("roster size = %d, want 2: %+v", len(roster), roster)
	}
	aiCount := 0
	for _, player := range roster {
		if player.IsAI {
			aiCount++
		}
	}
	if aiCount != 1 {
		t.Fatalf("AI count = %d, want 1: %+v", aiCount, roster)
	}
}

func TestPluginSchedulesAIForMergeSettleHolder(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	state, err := plugin.InitRoom(ctx, game.RoomConfig{RoomID: "acquire-merge-ai", OwnerPlayerID: "alice-1", MaxPlayers: 2, RandomSeed: testRandomSeed})
	if err != nil {
		t.Fatal(err)
	}
	room, err := asRoom(state)
	if err != nil {
		t.Fatal(err)
	}
	room.Connections["alice-1"] = &domain.PlayerConn{PlayerID: "alice-1", Online: true, Ready: true}
	room.Connections["ai_001"] = &domain.PlayerConn{PlayerID: "ai_001", Online: true, Ready: true, AI: true}
	room.PlayerSeq = []string{"alice-1", "ai_001"}
	room.State.RoomStatus = domain.RoomStatusMergingSettle
	room.State.CurrentPlayer = "alice-1"
	room.State.LastTileKey = "8F"
	room.State.MergeMainCompany = "Tower"
	room.State.BoardTiles = testAcquireBoard(map[string]string{
		"8F": "Festival",
		"8G": "Festival",
		"9F": "Tower",
		"9G": "Tower",
	})
	room.State.MergeSettleData = map[string]domain.SettleData{
		"Festival": {Hoders: []string{"ai_001"}, Dividends: map[string]int{"ai_001": 5000}},
	}
	room.State.Companies = map[string]*domain.CompanyState{
		"Tower":    {Name: "Tower", Tiles: 6, StockTotal: 22, StockPrice: 600},
		"Festival": {Name: "Festival", Tiles: 4, StockTotal: 23, StockPrice: 500},
	}
	room.State.Players = map[string]*domain.PlayerState{
		"alice-1": {Money: 3000, Stocks: map[string]int{"Tower": 1}},
		"ai_001":  {Money: 5000, Stocks: map[string]int{"Festival": 2, "Tower": 2}},
	}

	result, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{PlayerID: "alice-1", Type: "game_ready"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.AIRequests) != 1 || result.AIRequests[0].PlayerID != "ai_001" {
		t.Fatalf("AIRequests = %+v, want ai_001 merge settle request", result.AIRequests)
	}
	aiCommand, err := plugin.Decide(ctx, result.AIRequests[0])
	if err != nil {
		t.Fatal(err)
	}
	if aiCommand.Type != "game_merging_settle" {
		t.Fatalf("AI command type = %q, want game_merging_settle", aiCommand.Type)
	}
}

func TestPluginPendingCommandsRecoverMergeSettleAIHolder(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	state, err := plugin.InitRoom(ctx, game.RoomConfig{RoomID: "acquire-merge-ai-restore", OwnerPlayerID: "alice-1", MaxPlayers: 2, RandomSeed: testRandomSeed})
	if err != nil {
		t.Fatal(err)
	}
	room, err := asRoom(state)
	if err != nil {
		t.Fatal(err)
	}
	room.Connections["alice-1"] = &domain.PlayerConn{PlayerID: "alice-1", Online: true, Ready: true}
	room.Connections["ai_001"] = &domain.PlayerConn{PlayerID: "ai_001", Online: true, Ready: true, AI: true}
	room.State.RoomStatus = domain.RoomStatusMergingSettle
	room.State.CurrentPlayer = "alice-1"
	room.State.LastTileKey = "8F"
	room.State.MergeMainCompany = "Tower"
	room.State.BoardTiles = testAcquireBoard(map[string]string{
		"8F": "Festival",
		"8G": "Festival",
		"9F": "Tower",
		"9G": "Tower",
	})
	room.State.MergeSettleData = map[string]domain.SettleData{
		"Festival": {Hoders: []string{"ai_001"}, Dividends: map[string]int{"ai_001": 5000}},
	}
	room.State.Companies = map[string]*domain.CompanyState{
		"Tower":    {Name: "Tower", Tiles: 6, StockTotal: 22, StockPrice: 600},
		"Festival": {Name: "Festival", Tiles: 4, StockTotal: 23, StockPrice: 500},
	}
	room.State.Players = map[string]*domain.PlayerState{
		"alice-1": {Money: 3000, Stocks: map[string]int{"Tower": 1}},
		"ai_001":  {Money: 5000, Stocks: map[string]int{"Festival": 2, "Tower": 2}},
	}

	pending, err := plugin.PendingCommands(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].PlayerID != "ai_001" || pending[0].Type != "game_merging_settle" {
		t.Fatalf("pending = %+v, want ai_001 game_merging_settle", pending)
	}
}

func testAcquireBoard(belongs map[string]string) map[string]*domain.Tile {
	board := make(map[string]*domain.Tile, 108)
	for column := 1; column <= 12; column++ {
		for row := 'A'; row <= 'I'; row++ {
			id := fmt.Sprintf("%d%c", column, row)
			board[id] = &domain.Tile{ID: id, Belong: belongs[id]}
		}
	}
	return board
}

func TestHumanPresenceClosesAfterThreeConsecutiveEmptyChecks(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	state, err := plugin.InitRoom(ctx, game.RoomConfig{RoomID: "acquire-room", OwnerPlayerID: "alice-1", RandomSeed: testRandomSeed})
	if err != nil {
		t.Fatal(err)
	}
	for checkNumber := 1; checkNumber <= 3; checkNumber++ {
		check, err := plugin.ObserveHumanPresence(ctx, state, false)
		if err != nil {
			t.Fatal(err)
		}
		if check.ConsecutiveEmpty != checkNumber || check.Limit != 3 {
			t.Fatalf("check %d = %+v", checkNumber, check)
		}
		if check.ShouldClose != (checkNumber == 3) {
			t.Fatalf("check %d ShouldClose = %v", checkNumber, check.ShouldClose)
		}
	}
	reset, err := plugin.ObserveHumanPresence(ctx, state, true)
	if err != nil {
		t.Fatal(err)
	}
	if reset.ConsecutiveEmpty != 0 || reset.ShouldClose {
		t.Fatalf("reset check = %+v", reset)
	}
}
