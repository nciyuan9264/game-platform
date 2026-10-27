package forsale

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestPluginPreparationAndPrivateViews(t *testing.T) {
	plugin := &Plugin{}
	stateRaw, err := plugin.InitRoom(context.Background(), game.RoomConfig{
		RoomID: "room", OwnerUserID: "1", OwnerPlayerID: "1",
		MaxPlayers: 6, RandomSeed: 301, RoundNo: 1,
		Visibility: contracts.VisibilityPrivate,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := stateRaw.(*State)
	for _, command := range []contracts.RoomCommand{
		{Type: "connect", UserID: "1", PlayerID: "1"},
		{Type: "match_add_ai", PlayerID: "1", Payload: map[string]any{"seatIndex": 1}},
		{Type: "match_add_ai", PlayerID: "1", Payload: map[string]any{"seatIndex": 2}},
		{Type: "match_begin", PlayerID: "1", SentAt: time.Unix(100, 0)},
		{Type: "game_ready", PlayerID: "1", SentAt: time.Unix(101, 0)},
	} {
		if _, err := plugin.ApplyCommand(context.Background(), state, command); err != nil {
			t.Fatalf("%s: %v", command.Type, err)
		}
	}
	if state.Status != StatusAuction || len(state.PlayerOrder) != 3 {
		t.Fatalf("state = %s players=%d", state.Status, len(state.PlayerOrder))
	}
	viewRaw, err := plugin.BuildPlayerView(context.Background(), state, game.ViewRequest{
		MatchID: "room-r1", PlayerID: "1", Seq: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	view := viewRaw.(map[string]any)
	playerData := view["playerData"].(map[string]any)
	if _, ok := playerData["1"].(map[string]any)["coins"]; !ok {
		t.Fatal("self coins are hidden")
	}
	if _, ok := playerData["ai_2"].(map[string]any)["coins"]; ok {
		t.Fatal("opponent coins leaked")
	}
	spectatorRaw, err := plugin.BuildSpectatorView(context.Background(), state, 7)
	if err != nil {
		t.Fatal(err)
	}
	spectator := spectatorRaw.(map[string]any)
	spectatorData := spectator["playerData"].(map[string]any)
	if _, ok := spectatorData["1"].(map[string]any)["properties"]; ok {
		t.Fatal("spectator properties leaked")
	}
}

func TestStateCodecRoundTrip(t *testing.T) {
	plugin := &Plugin{}
	state := preparedState(t, 5, 302)
	armAutomation(state, time.Unix(200, 0))
	encoded, err := plugin.EncodeState(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := plugin.DecodeState(context.Background(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(encoded, reencoded) {
		t.Fatalf("round trip changed state")
	}
}

func TestPhaseTimeoutOnlyAdvancesCurrentPhase(t *testing.T) {
	state := preparedState(t, 3, 303)
	for state.Status == StatusAuction {
		if err := applyPass(state, state.CurrentPlayer, SourceAI); err != nil {
			t.Fatal(err)
		}
	}
	armAutomation(state, time.Unix(300, 0))
	commands, err := (&Plugin{}).PendingCommands(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 || commands[0].Type != CommandPhaseTimeout {
		t.Fatalf("commands = %+v", commands)
	}
	if err := applyPhaseTimeout(state); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusAuction {
		t.Fatalf("state = %s", state.Status)
	}
	if err := applyPhaseTimeout(state); err == nil {
		t.Fatal("stale phase timeout was accepted")
	}
}

func TestClientCannotSubmitTimeout(t *testing.T) {
	state := preparedState(t, 3, 304)
	_, err := (&Plugin{}).ApplyCommand(context.Background(), state, contracts.RoomCommand{
		Type: CommandTurnTimeout, PlayerID: state.CurrentPlayer,
		ConnectionRef: contracts.ConnectionRef{
			GatewayNode: "gateway", SessionID: "session",
		},
	})
	if err == nil {
		t.Fatal("client timeout was accepted")
	}
}
