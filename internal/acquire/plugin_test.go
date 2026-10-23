package acquire

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
	rules "github.com/nciyuan9264/game-platform/internal/acquire/rules"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
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

func TestBuildMatchSyncIncludesRequiredRoomMetadata(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	state, err := plugin.InitRoom(ctx, game.RoomConfig{
		RoomID:        "acquire-match-sync",
		OwnerPlayerID: "alice-1",
		MaxPlayers:    6,
		RandomSeed:    testRandomSeed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{
		PlayerID: "alice-1",
		Type:     "connect",
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := plugin.BuildPlayerView(ctx, state, game.ViewRequest{
		RoomID:     "acquire-match-sync",
		MatchID:    "match-1",
		PlayerID:   "alice-1",
		Visibility: contracts.VisibilityPrivate,
		MaxPlayers: 6,
		PlayerProfiles: map[string]game.PlayerProfile{
			"alice-1": {
				UserID:      "user-1",
				DisplayName: "Alice",
				Avatar:      "https://example.com/alice.png",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	view := raw.(contracts.MatchSync)
	if view.Visibility != contracts.VisibilityPrivate || view.MaxPlayers != 6 {
		t.Fatalf("room metadata = (%q, %d), want (%q, 6)", view.Visibility, view.MaxPlayers, contracts.VisibilityPrivate)
	}
	if len(view.PlayerOrder) != 1 || view.PlayerOrder[0] != "alice-1" {
		t.Fatalf("player order = %v, want [alice-1]", view.PlayerOrder)
	}
	player := view.Players["alice-1"]
	if player.UserID != "user-1" || player.DisplayName != "Alice" || player.Avatar != "https://example.com/alice.png" {
		t.Fatalf("player profile = %+v", player)
	}

	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"visibility", "maxPlayers", "playerOrder"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("serialized match sync omitted %q: %s", key, encoded)
		}
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

func TestPluginSerializesMergeSettlementByTurnOrder(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	state, err := plugin.InitRoom(ctx, game.RoomConfig{
		RoomID:        "acquire-serial-merge",
		OwnerPlayerID: "alice",
		MaxPlayers:    2,
		RandomSeed:    testRandomSeed,
	})
	if err != nil {
		t.Fatal(err)
	}
	room, err := asRoom(state)
	if err != nil {
		t.Fatal(err)
	}
	room.Connections["alice"] = &domain.PlayerConn{PlayerID: "alice", Online: true, SeatIndex: 0}
	room.Connections["bob"] = &domain.PlayerConn{PlayerID: "bob", Online: true, SeatIndex: 1}
	room.PlayerSeq = []string{"alice", "bob"}
	room.State.CurrentPlayer = "alice"
	room.State.RoomStatus = domain.RoomStatusMergingSettle
	room.State.LastTileKey = "2A"
	room.State.MergeMainCompany = "Tower"
	room.State.MergeSettleData = map[string]domain.SettleData{
		"American": {Hoders: []string{"bob", "alice"}, Dividends: map[string]int{}},
	}
	room.State.Companies["Tower"] = &domain.CompanyState{Name: "Tower", Tiles: 3, StockTotal: 1, StockPrice: 300}
	room.State.Companies["American"] = &domain.CompanyState{Name: "American", Tiles: 2, StockTotal: 21, StockPrice: 200}
	room.State.Players = map[string]*domain.PlayerState{
		"alice": {Stocks: map[string]int{"American": 2, "Tower": 12}},
		"bob":   {Stocks: map[string]int{"American": 2, "Tower": 12}},
	}
	view, err := plugin.BuildPlayerView(ctx, state, game.ViewRequest{PlayerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	tempData := view.(map[string]any)["tempData"].(map[string]any)
	if tempData["merge_settle_current_player"] != "alice" {
		t.Fatalf("view current settlement player = %v, want alice", tempData["merge_settle_current_player"])
	}

	exchange := map[string]any{"actions": []map[string]any{{
		"company": "American", "exchangeAmount": 2, "sellAmount": 0,
	}}}
	if _, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{
		PlayerID: "bob", Type: "game_merging_settle", Payload: exchange,
	}); err == nil || err.Error() != "请等待当前股东完成并购结算" {
		t.Fatalf("early bob settlement error = %v", err)
	}
	if _, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{
		PlayerID: "alice", Type: "game_merging_settle", Payload: exchange,
	}); err != nil {
		t.Fatal(err)
	}
	if room.State.Companies["Tower"].StockTotal != 0 || rules.CurrentMergeSettlePlayer(room) != "bob" {
		t.Fatalf("after alice settlement: stock=%d current=%q",
			room.State.Companies["Tower"].StockTotal, rules.CurrentMergeSettlePlayer(room))
	}
	if _, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{
		PlayerID: "bob", Type: "game_merging_settle", Payload: exchange,
	}); err == nil || err.Error() != "存续公司库存已变化，请调整兑换数量" {
		t.Fatalf("stale bob settlement error = %v", err)
	}
	if room.State.Companies["Tower"].StockTotal != 0 || rules.CurrentMergeSettlePlayer(room) != "bob" {
		t.Fatalf("stale settlement changed state: stock=%d current=%q",
			room.State.Companies["Tower"].StockTotal, rules.CurrentMergeSettlePlayer(room))
	}

	sell := map[string]any{"actions": []map[string]any{{
		"company": "American", "exchangeAmount": 0, "sellAmount": 2,
	}}}
	if _, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{
		PlayerID: "bob", Type: "game_merging_settle", Payload: sell,
	}); err != nil {
		t.Fatal(err)
	}
	if room.State.RoomStatus != domain.RoomStatusBuyStock {
		t.Fatalf("room status = %q, want buyStock", room.State.RoomStatus)
	}
}

func TestPluginReplaysLegacyMergeSettlementOrder(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	state, err := plugin.InitRoom(ctx, game.RoomConfig{
		RoomID:        "acquire-legacy-merge-replay",
		OwnerPlayerID: "alice",
		MaxPlayers:    2,
		RandomSeed:    testRandomSeed,
	})
	if err != nil {
		t.Fatal(err)
	}
	room, err := asRoom(state)
	if err != nil {
		t.Fatal(err)
	}
	room.Connections["alice"] = &domain.PlayerConn{PlayerID: "alice", Online: true, SeatIndex: 0}
	room.Connections["bob"] = &domain.PlayerConn{PlayerID: "bob", Online: true, SeatIndex: 1}
	room.PlayerSeq = []string{"alice", "bob"}
	room.State.CurrentPlayer = "alice"
	room.State.RoomStatus = domain.RoomStatusMergingSettle
	room.State.MergeMainCompany = "Tower"
	room.State.MergeSettleData = map[string]domain.SettleData{
		"American": {Hoders: []string{"alice", "bob"}, Dividends: map[string]int{}},
	}
	room.State.Companies["Tower"] = &domain.CompanyState{Name: "Tower", Tiles: 3, StockTotal: 10, StockPrice: 300}
	room.State.Companies["American"] = &domain.CompanyState{Name: "American", Tiles: 2, StockTotal: 21, StockPrice: 200}
	room.State.Players = map[string]*domain.PlayerState{
		"alice": {Stocks: map[string]int{"American": 2}},
		"bob":   {Stocks: map[string]int{"American": 2}},
	}

	_, replayErr := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{
		PlayerID: "bob",
		Type:     "game_merging_settle",
		Payload: map[string]any{"actions": []map[string]any{{
			"company": "American", "exchangeAmount": 0, "sellAmount": 1,
		}}},
		Replay: true,
	})
	if replayErr != nil {
		t.Fatalf("replay legacy settlement: %v", replayErr)
	}
	if rules.IsPendingMergeSettlePlayer(room, "bob") {
		t.Fatal("legacy shareholder remains pending after replay")
	}
	if got := rules.CurrentMergeSettlePlayer(room); got != "alice" {
		t.Fatalf("current settlement player = %q, want alice", got)
	}
}

func TestPluginLabelsLiveAndReplayOperationLogs(t *testing.T) {
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	var output bytes.Buffer
	log.SetOutput(&output)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	}()

	plugin := &Plugin{}
	apply := func(roomID, commandID string, replay bool) {
		t.Helper()
		state, err := plugin.InitRoom(context.Background(), game.RoomConfig{
			RoomID:        roomID,
			OwnerPlayerID: "alice",
			MaxPlayers:    1,
			RandomSeed:    testRandomSeed,
		})
		if err != nil {
			t.Fatal(err)
		}
		room, err := asRoom(state)
		if err != nil {
			t.Fatal(err)
		}
		room.Connections["alice"] = &domain.PlayerConn{PlayerID: "alice", Online: true, SeatIndex: 0}
		room.PlayerSeq = []string{"alice"}
		room.State.CurrentPlayer = "alice"
		room.State.RoomStatus = domain.RoomStatusBuyStock
		room.State.BoardTiles = map[string]*domain.Tile{"1A": {ID: "1A"}}
		room.State.Players = map[string]*domain.PlayerState{
			"alice": {Money: 6000, Stocks: map[string]int{}},
		}

		if _, err := plugin.ApplyCommand(context.Background(), state, contracts.RoomCommand{
			CommandID: commandID,
			RoomID:    roomID,
			MatchID:   roomID + "-r1",
			PlayerID:  "alice",
			Type:      "game_buy_stock",
			Payload:   map[string]any{"stocks": map[string]int{}},
			Replay:    replay,
		}); err != nil {
			t.Fatalf("apply command: %v", err)
		}
		if room.CommandLogger != nil {
			t.Fatal("command logger scope was not restored")
		}
	}

	apply("live-room", "live-command", false)
	apply("replay-room", "replay-command", true)
	logs := output.String()
	liveTagged := strings.Contains(logs, `"command_id":"live-command"`) &&
		strings.Contains(logs, `"execution_mode":"live"`) &&
		strings.Contains(logs, `"is_replay":false`)
	replayTagged := strings.Contains(logs, `"command_id":"replay-command"`) &&
		strings.Contains(logs, `"execution_mode":"replay"`) &&
		strings.Contains(logs, `"is_replay":true`)

	if !liveTagged || !replayTagged {
		t.Fatalf("operation logs are not distinctly tagged:\n%s", logs)
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

func TestPluginResetAutomationStateReleasesAIRunning(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	state, err := plugin.InitRoom(ctx, game.RoomConfig{
		RoomID:        "acquire-reset-automation",
		OwnerPlayerID: "alice-1",
		MaxPlayers:    2,
		RandomSeed:    testRandomSeed,
	})
	if err != nil {
		t.Fatal(err)
	}
	room, err := asRoom(state)
	if err != nil {
		t.Fatal(err)
	}
	room.AIRunning = true

	if err := plugin.ResetAutomationState(ctx, state); err != nil {
		t.Fatal(err)
	}
	if room.AIRunning {
		t.Fatal("AIRunning remained true after automation reset")
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

func TestAcquireValidationRejectsInvalidActionsBeforeMutation(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	state, err := plugin.InitRoom(ctx, game.RoomConfig{
		RoomID:        "acquire-validation",
		OwnerPlayerID: "alice-1",
		MaxPlayers:    2,
		RandomSeed:    testRandomSeed,
	})
	if err != nil {
		t.Fatal(err)
	}
	room, err := asRoom(state)
	if err != nil {
		t.Fatal(err)
	}
	room.Connections["alice-1"] = &domain.PlayerConn{PlayerID: "alice-1", Online: true}
	room.State.CurrentPlayer = "alice-1"
	room.State.RoomStatus = domain.RoomStatusBuyStock
	room.State.BoardTiles = testAcquireBoard(nil)
	room.State.Players["alice-1"] = &domain.PlayerState{
		Money:  100,
		Tiles:  []string{"1A"},
		Stocks: map[string]int{"Tower": 0},
	}
	room.State.Companies["Tower"] = &domain.CompanyState{
		Name: "Tower", Tiles: 2, StockTotal: 25, StockPrice: 200,
	}

	_, err = plugin.ApplyCommand(ctx, state, contracts.RoomCommand{
		PlayerID: "alice-1",
		Type:     "game_buy_stock",
		Payload:  map[string]any{"stocks": map[string]int{"Tower": 1}},
	})
	if err == nil {
		t.Fatal("purchase above cash balance was accepted")
	}
	if player := room.State.Players["alice-1"]; player.Money != 100 || player.Stocks["Tower"] != 0 {
		t.Fatalf("rejected purchase mutated player: %+v", player)
	}
	if room.State.Companies["Tower"].StockTotal != 25 {
		t.Fatalf("rejected purchase mutated inventory: %+v", room.State.Companies["Tower"])
	}

	room.State.RoomStatus = domain.RoomStatusSetTile
	_, err = plugin.ApplyCommand(ctx, state, contracts.RoomCommand{
		PlayerID: "alice-1",
		Type:     "game_place_tile",
		Payload:  map[string]any{"tileKey": "unknown"},
	})
	if err == nil {
		t.Fatal("unknown tile was accepted")
	}
	if room.State.LastTileKey != "" || len(room.State.Players["alice-1"].Tiles) != 1 {
		t.Fatal("rejected tile placement mutated game state")
	}
}

func TestAcquireRoomSettingsAndRequestedAISeat(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	state, err := plugin.InitRoom(ctx, game.RoomConfig{
		RoomID:        "acquire-settings",
		OwnerPlayerID: "alice-1",
		MaxPlayers:    6,
		RandomSeed:    testRandomSeed,
		Visibility:    contracts.VisibilityPublic,
	})
	if err != nil {
		t.Fatal(err)
	}
	apply := func(commandType string, payload any) {
		t.Helper()
		if _, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{
			PlayerID: "alice-1",
			Type:     commandType,
			Payload:  payload,
		}); err != nil {
			t.Fatalf("%s: %v", commandType, err)
		}
	}
	apply("connect", nil)
	apply("match_add_ai", map[string]any{"seatIndex": 4})
	apply("room_update_settings", map[string]any{"visibility": contracts.VisibilityPrivate})

	room, err := asRoom(state)
	if err != nil {
		t.Fatal(err)
	}
	var ai *domain.PlayerConn
	for _, player := range room.Connections {
		if player.AI {
			ai = player
		}
	}
	if ai == nil || ai.SeatIndex != 4 {
		t.Fatalf("requested AI seat was not retained: %+v", ai)
	}
	metadata, err := plugin.RoomMetadata(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Visibility != contracts.VisibilityPrivate {
		t.Fatalf("visibility = %q, want private", metadata.Visibility)
	}
}

func TestAcquireEndVoteIsVisibleRecordableAndFinalizesOnTurnBoundary(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	state, err := plugin.InitRoom(ctx, game.RoomConfig{
		RoomID:        "acquire-end-vote",
		OwnerPlayerID: "alice-1",
		MaxPlayers:    2,
		RandomSeed:    testRandomSeed,
	})
	if err != nil {
		t.Fatal(err)
	}
	room, err := asRoom(state)
	if err != nil {
		t.Fatal(err)
	}
	room.Connections["alice-1"] = &domain.PlayerConn{PlayerID: "alice-1", Online: true, SeatIndex: 0}
	room.Connections["bob-2"] = &domain.PlayerConn{PlayerID: "bob-2", Online: true, SeatIndex: 1}
	room.PlayerSeq = []string{"alice-1", "bob-2"}
	room.State.RoomStatus = domain.RoomStatusSetTile
	room.State.CurrentPlayer = "alice-1"
	room.State.Players = map[string]*domain.PlayerState{
		"alice-1": {Tiles: []string{"1A"}, Stocks: map[string]int{}},
		"bob-2":   {Tiles: []string{"2A"}, Stocks: map[string]int{}},
	}
	room.State.EndGame = domain.EndGameState{
		Eligible:  true,
		Condition: "company_size",
		Company:   "Tower",
		TileCount: 41,
		Votes:     map[string]bool{"alice-1": false, "bob-2": false},
	}

	for _, playerID := range []string{"alice-1", "bob-2"} {
		if _, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{
			PlayerID: playerID,
			Type:     "game_end_vote",
			Payload:  map[string]any{"allow": true},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if room.State.RoomStatus == domain.RoomStatusEnd {
		t.Fatal("vote ended the game before a turn boundary")
	}
	if !plugin.IsRecordableCommand("game_end_vote") {
		t.Fatal("end vote is not recorded for replay")
	}

	view, err := plugin.BuildPlayerView(ctx, state, game.ViewRequest{
		RoomID: "acquire-end-vote", PlayerID: "alice-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	endGame := view.(map[string]any)["roomData"].(map[string]any)["endGame"].(domain.EndGameState)
	if !endGame.Votes["alice-1"] || !endGame.Votes["bob-2"] {
		t.Fatalf("end votes missing from player view: %+v", endGame)
	}

	result, err := plugin.ApplyCommand(ctx, state, contracts.RoomCommand{
		PlayerID: "alice-1",
		Type:     "turn_timeout",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Finished || room.State.RoomStatus != domain.RoomStatusEnd {
		t.Fatalf("approved game did not end at turn boundary: result=%+v status=%q", result, room.State.RoomStatus)
	}
}
