package roompkg

import (
	"fmt"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform/internal/acquire/roomcore"
)

func newAITestRoom() *domain.Room {
	base := roomcore.NewBase("ai-test-room", 8)
	base.PlayerSeq = []string{"human_001", "ai_001"}
	base.Connections["human_001"] = &roomcore.PlayerConn{
		PlayerID: "human_001",
		Online:   true,
		Ready:    true,
		AI:       false,
	}
	base.Connections["ai_001"] = &roomcore.PlayerConn{
		PlayerID: "ai_001",
		Online:   true,
		Ready:    true,
		AI:       true,
	}

	boardTiles := make(map[string]*domain.Tile)
	for row := 1; row <= 12; row++ {
		for col := 'A'; col <= 'I'; col++ {
			id := fmt.Sprintf("%d%c", row, col)
			boardTiles[id] = &domain.Tile{ID: id, Belong: ""}
		}
	}
	boardTiles["1A"].Belong = "Blank"

	return &domain.Room{
		Base: base,
		State: &domain.GameState{
			CurrentPlayer:    "ai_001",
			LastTileKey:      "1A",
			RoomStatus:       domain.RoomStatusSetTile,
			OwnerID:          "human_001",
			MaxPlayers:       2,
			MergeMainCompany: "Tower",
			BoardTiles:       boardTiles,
			Players: map[string]*domain.PlayerState{
				"human_001": {
					Money:  6000,
					Tiles:  []string{"2A", "2B"},
					Stocks: map[string]int{"Tower": 2, "Sackson": 1},
				},
				"ai_001": {
					Money:  6000,
					Tiles:  []string{"3A", "3B"},
					Stocks: map[string]int{"Tower": 1, "Sackson": 0},
				},
			},
			Companies: map[string]*domain.CompanyState{
				"Tower":   {Name: "Tower", Tiles: 2, StockPrice: 200, StockTotal: 22},
				"Sackson": {Name: "Sackson", Tiles: 3, StockPrice: 300, StockTotal: 24},
			},
			MergingSelection: domain.MergingSelection{
				MainCompany:  []string{"Tower", "Sackson"},
				OtherCompany: []string{"American"},
			},
			MergeSettleData: map[string]domain.SettleData{
				"Sackson": {
					Hoders:    []string{"human_001", "ai_001"},
					Dividends: map[string]int{"human_001": 3000, "ai_001": 1500},
				},
			},
		},
	}
}

func TestCloneRoomForAISimulationDoesNotAliasState(t *testing.T) {
	original := newAITestRoom()
	cloned := cloneRoomForAISimulation(original)

	cloned.State.Players["ai_001"].Money = 1
	cloned.State.Players["ai_001"].Stocks["Tower"] = 9
	cloned.State.Players["ai_001"].Tiles[0] = "9I"
	cloned.State.BoardTiles["1A"].Belong = "Tower"
	cloned.State.Companies["Tower"].Tiles = 7
	cloned.State.MergingSelection.MainCompany[0] = "Imperial"
	settle := cloned.State.MergeSettleData["Sackson"]
	settle.Hoders[0] = "ai_001"
	settle.Dividends["human_001"] = 1
	cloned.State.MergeSettleData["Sackson"] = settle

	if original.State.Players["ai_001"].Money == 1 {
		t.Fatalf("player money aliased")
	}
	if original.State.Players["ai_001"].Stocks["Tower"] == 9 {
		t.Fatalf("player stocks aliased")
	}
	if original.State.Players["ai_001"].Tiles[0] == "9I" {
		t.Fatalf("player tiles aliased")
	}
	if original.State.BoardTiles["1A"].Belong == "Tower" {
		t.Fatalf("board tile aliased")
	}
	if original.State.Companies["Tower"].Tiles == 7 {
		t.Fatalf("company aliased")
	}
	if original.State.MergingSelection.MainCompany[0] == "Imperial" {
		t.Fatalf("merging selection aliased")
	}
	if original.State.MergeSettleData["Sackson"].Hoders[0] == "ai_001" {
		t.Fatalf("merge settle holders aliased")
	}
	if original.State.MergeSettleData["Sackson"].Dividends["human_001"] == 1 {
		t.Fatalf("merge settle dividends aliased")
	}
}

func TestCloneRoomForAISimulationUsesIsolatedRuntimeState(t *testing.T) {
	original := newAITestRoom()
	cloned := cloneRoomForAISimulation(original)

	if cloned == original {
		t.Fatalf("room pointer was reused")
	}
	if cloned.Base == original.Base {
		t.Fatalf("base pointer was reused")
	}
	if cloned.CmdCh == original.CmdCh {
		t.Fatalf("command channel was reused")
	}
	if cloned.Connections["ai_001"] == original.Connections["ai_001"] {
		t.Fatalf("player connection pointer was reused")
	}
	if cloned.Connections["ai_001"].Conn != nil {
		t.Fatalf("simulation connection should not trigger virtual writes")
	}
}
