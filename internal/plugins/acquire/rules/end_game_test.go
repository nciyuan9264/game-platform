package game

import (
	"fmt"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/plugins/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform/internal/plugins/acquire/roomcore"
)

func newEndGameRoom() *domain.Room {
	base := roomcore.NewBase("end-game", 8)
	base.Connections["human-1"] = &roomcore.PlayerConn{PlayerID: "human-1", Online: true}
	base.Connections["human-2"] = &roomcore.PlayerConn{PlayerID: "human-2"}
	base.Connections["ai-1"] = &roomcore.PlayerConn{PlayerID: "ai-1", Online: true, AI: true}
	return &domain.Room{
		Base: base,
		State: &domain.GameState{
			RoomStatus: domain.RoomStatusSetTile,
			BoardTiles: make(map[string]*domain.Tile),
			EndGame:    domain.EndGameState{Votes: make(map[string]bool)},
		},
	}
}

func TestRefreshEndGameEligibilityLatchesAndIncludesEveryHuman(t *testing.T) {
	room := newEndGameRoom()
	RefreshEndGameEligibility(room, map[string]int{})
	if room.State.EndGame.Eligible {
		t.Fatal("empty company set became eligible")
	}

	RefreshEndGameEligibility(room, map[string]int{"Tower": 11, "Sackson": 12})
	if room.State.EndGame.Eligible {
		t.Fatal("only some established safe companies unlocked end game")
	}

	allCompanies := map[string]int{
		"Sackson":     11,
		"Tower":       12,
		"American":    13,
		"Festival":    14,
		"Worldwide":   15,
		"Continental": 16,
		"Imperial":    10,
	}
	RefreshEndGameEligibility(room, allCompanies)
	if room.State.EndGame.Eligible {
		t.Fatal("an established company below 11 tiles unlocked end game")
	}

	allCompanies["Imperial"] = 11
	RefreshEndGameEligibility(room, allCompanies)
	if !room.State.EndGame.Eligible || room.State.EndGame.Condition != EndConditionAllCompaniesSafe {
		t.Fatalf("all seven established safe companies did not unlock end game: %+v", room.State.EndGame)
	}
	if room.State.EndGame.Votes["human-1"] || room.State.EndGame.Votes["human-2"] {
		t.Fatal("human votes must default to false")
	}
	if !room.State.EndGame.Votes["ai-1"] {
		t.Fatal("AI vote must default to true")
	}

	RefreshEndGameEligibility(room, map[string]int{"Tower": 11, "Sackson": 12, "American": 2})
	if room.State.EndGame.Condition != EndConditionAllCompaniesSafe {
		t.Fatal("latched eligibility regressed after an unsafe company appeared")
	}
}

func TestEndVotesAreReversibleAndFinalizeAtTurnBoundary(t *testing.T) {
	room := newEndGameRoom()
	RefreshEndGameEligibility(room, map[string]int{"Tower": 41})

	HandleEndVoteMessage(room, domain.Command{PlayerID: "human-1", Payload: []byte(`{"allow":true}`)})
	if AllEndVotesApproved(room) {
		t.Fatal("offline human was skipped")
	}
	HandleEndVoteMessage(room, domain.Command{PlayerID: "human-2", Payload: []byte(`{"allow":true}`)})
	if !AllEndVotesApproved(room) {
		t.Fatal("unanimous human approval was not recognized")
	}
	if ShouldFinalizeEnd(room, false) || !ShouldFinalizeEnd(room, true) {
		t.Fatal("end approval did not respect the turn boundary")
	}

	HandleEndVoteMessage(room, domain.Command{PlayerID: "human-1", Payload: []byte(`{"allow":false}`)})
	if AllEndVotesApproved(room) {
		t.Fatal("revoked vote remained approved")
	}
}

func TestBoardFullForcesEndAtTurnBoundary(t *testing.T) {
	room := newEndGameRoom()
	for index := 0; index < acquireBoardTileCount; index++ {
		id := fmt.Sprintf("tile-%d", index)
		room.State.BoardTiles[id] = &domain.Tile{ID: id, Belong: "Blank"}
	}
	if !IsBoardFull(room) || ShouldFinalizeEnd(room, false) || !ShouldFinalizeEnd(room, true) {
		t.Fatal("full-board end condition did not respect the turn boundary")
	}
}
