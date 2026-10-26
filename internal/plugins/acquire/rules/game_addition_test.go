package game

import (
	"testing"

	"github.com/nciyuan9264/game-platform/internal/plugins/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform/internal/plugins/acquire/roomcore"
)

func TestHandleTurnTimeoutMessageIgnoresEndedRoom(t *testing.T) {
	base := roomcore.NewBase("ended-room", 8)
	base.Connections["alice"] = &roomcore.PlayerConn{PlayerID: "alice", Online: true}
	base.Connections["bob"] = &roomcore.PlayerConn{PlayerID: "bob", Online: true}
	r := &domain.Room{
		Base: base,
		State: &domain.GameState{
			CurrentPlayer:    "alice",
			RoomStatus:       domain.RoomStatusEnd,
			MergeMainCompany: "Tower",
			MergeSettleData: map[string]domain.SettleData{
				"Tower": {Hoders: []string{"alice"}},
			},
			MergingSelection: domain.MergingSelection{
				MainCompany: []string{"Tower"},
			},
		},
	}

	HandleTurnTimeoutMessage(r, domain.Command{Type: "turn_timeout", PlayerID: "alice"})

	if r.State.CurrentPlayer != "alice" {
		t.Fatalf("current player = %q, want alice", r.State.CurrentPlayer)
	}
	if r.State.RoomStatus != domain.RoomStatusEnd {
		t.Fatalf("room status = %q, want end", r.State.RoomStatus)
	}
	if r.State.MergeMainCompany != "Tower" {
		t.Fatalf("merge main company was reset")
	}
	if _, ok := r.State.MergeSettleData["Tower"]; !ok {
		t.Fatalf("merge settle data was reset")
	}
	if len(r.State.MergingSelection.MainCompany) != 1 || r.State.MergingSelection.MainCompany[0] != "Tower" {
		t.Fatalf("merging selection was reset: %+v", r.State.MergingSelection)
	}
}
