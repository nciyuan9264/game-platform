package fugitive

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/history"
)

func TestHistoryProjectionEncodesRequiredReplaySections(t *testing.T) {
	state := startedHumanAIState(t)
	projection, err := (&Plugin{}).ProjectHistory(
		context.Background(),
		state,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !projection.Started || projection.Finished {
		t.Fatalf("projection = %+v", projection)
	}
	payload, err := history.EncodeReplayFrame(
		projection.ReplaySchemaVersion,
		projection.ReplayFrame,
	)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := history.DecodeReplayFrame(
		projection.ReplaySchemaVersion,
		payload,
	)
	if err != nil {
		t.Fatal(err)
	}
	var players map[string]struct {
		Role      Role `json:"role"`
		HandCount int  `json:"handCount"`
		SeatIndex int  `json:"seatIndex"`
		AI        bool `json:"ai"`
	}
	if err := json.Unmarshal(frame.PlayersData, &players); err != nil {
		t.Fatal(err)
	}
	if len(players) != 2 ||
		players[state.FugitivePlayerID].Role != RoleFugitive ||
		players[state.FugitivePlayerID].HandCount == 0 ||
		players[state.MarshalPlayerID].Role != RoleMarshal ||
		!players[state.MarshalPlayerID].AI {
		t.Fatalf("players data = %+v", players)
	}
}

func TestFinishedHistoryProjectsHumanAIWinnerRolesAndRanks(t *testing.T) {
	state := startedHumanAIState(t)
	state.Status = StatusEnd
	state.WinnerID = state.FugitivePlayerID
	state.WinnerRole = RoleFugitive
	state.EndReason = "escaped"
	state.Searches = 3

	projection, err := (&Plugin{}).ProjectHistory(
		context.Background(),
		state,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !projection.Finished ||
		projection.WinnerPlayerID != state.FugitivePlayerID ||
		len(projection.Players) != 2 {
		t.Fatalf("projection = %+v", projection)
	}
	players := make(map[string]game.HistoryPlayer, len(projection.Players))
	for _, player := range projection.Players {
		players[player.PlayerID] = player
	}
	human := players[state.FugitivePlayerID]
	bot := players[state.MarshalPlayerID]
	if human.UserID != "owner" || human.IsAI ||
		human.GameRole != string(RoleFugitive) ||
		!human.IsWinner || human.FinalRank == nil || *human.FinalRank != 1 {
		t.Fatalf("human = %+v", human)
	}
	if bot.UserID != "" || !bot.IsAI ||
		bot.GameRole != string(RoleMarshal) ||
		bot.IsWinner || bot.FinalRank == nil || *bot.FinalRank != 2 {
		t.Fatalf("bot = %+v", bot)
	}
	payload, err := history.EncodeReplayFrame(
		projection.ReplaySchemaVersion,
		projection.ReplayFrame,
	)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := history.DecodeReplayFrame(
		projection.ReplaySchemaVersion,
		payload,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(frame.RoomData) == 0 ||
		len(frame.PlayersData) == 0 ||
		len(frame.Result) == 0 {
		t.Fatalf("frame = %+v", frame)
	}
}

func startedHumanAIState(t *testing.T) *State {
	t.Helper()
	plugin := &Plugin{}
	value, err := plugin.InitRoom(
		context.Background(),
		game.RoomConfig{
			RoomID:        "fugitive-history",
			OwnerPlayerID: "owner",
			MaxPlayers:    2,
			RandomSeed:    42,
			RoundNo:       1,
			Visibility:    contracts.VisibilityPrivate,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	state := value.(*State)
	apply(t, plugin, state, "owner", "connect", nil)
	apply(t, plugin, state, "owner", "match_add_ai", map[string]any{
		"seatIndex": 1,
	})
	apply(t, plugin, state, "owner", "match_begin", nil)
	apply(t, plugin, state, "owner", "game_ready", nil)
	if state.Status != StatusOpening || state.GameStartTime.IsZero() {
		t.Fatalf("state did not start: %+v", state)
	}
	state.Roster["owner"].DisplayName = "Alice"
	return state
}
