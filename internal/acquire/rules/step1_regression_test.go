package game

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform/internal/acquire/roomcore"
)

func mergeTestRoom() *domain.Room {
	base := roomcore.NewBase("merge-test", 8)
	board := make(map[string]*domain.Tile, 108)
	for column := 1; column <= 12; column++ {
		for row := 'A'; row <= 'I'; row++ {
			id := fmt.Sprintf("%d%c", column, row)
			board[id] = &domain.Tile{ID: id}
		}
	}
	return &domain.Room{
		Base: base,
		State: &domain.GameState{
			LastTileKey: "2A",
			RoomStatus:  domain.RoomStatusSetTile,
			BoardTiles:  board,
			Players:     map[string]*domain.PlayerState{},
			Companies: map[string]*domain.CompanyState{
				"American": {Name: "American", Tiles: 2, StockTotal: 25, StockPrice: 300},
				"Tower":    {Name: "Tower", Tiles: 3, StockTotal: 25, StockPrice: 300},
			},
		},
	}
}

func TestHandleMergeProcessUsesPlayerStateAndStableHolderOrder(t *testing.T) {
	room := mergeTestRoom()
	room.State.Players["bob"] = &domain.PlayerState{Stocks: map[string]int{"American": 1}}
	room.State.Players["alice"] = &domain.PlayerState{Stocks: map[string]int{"American": 1}}

	if err := handleMergeProcess(room, "Tower", []string{"American"}, map[string]int{"American": 2}); err != nil {
		t.Fatal(err)
	}

	settlement := room.State.MergeSettleData["American"]
	if len(settlement.Hoders) != 2 || settlement.Hoders[0] != "alice" || settlement.Hoders[1] != "bob" {
		t.Fatalf("holders = %v, want stable [alice bob]", settlement.Hoders)
	}
}

func TestHandleMergeProcessCompletesWithoutShareholders(t *testing.T) {
	room := mergeTestRoom()
	room.State.Players["alice"] = &domain.PlayerState{Stocks: map[string]int{}}
	room.State.BoardTiles["1A"].Belong = "American"
	room.State.BoardTiles["2A"].Belong = "Blank"
	room.State.BoardTiles["3A"].Belong = "Tower"

	if err := handleMergeProcess(room, "Tower", []string{"American"}, map[string]int{"American": 2}); err != nil {
		t.Fatal(err)
	}
	if room.State.RoomStatus != domain.RoomStatusBuyStock || len(room.State.MergeSettleData) != 0 {
		t.Fatalf("empty settlement did not complete: status=%q data=%+v", room.State.RoomStatus, room.State.MergeSettleData)
	}
}

func TestHandleMergingSettleRecordsReceipt(t *testing.T) {
	room := mergeTestRoom()
	room.State.RoomStatus = domain.RoomStatusMergingSettle
	room.State.MergeMainCompany = "Tower"
	room.State.Players["alice"] = &domain.PlayerState{
		Money:  6000,
		Stocks: map[string]int{"American": 4, "Tower": 0},
	}
	room.State.Players["bob"] = &domain.PlayerState{Stocks: map[string]int{"American": 1}}
	room.State.MergeSettleData = map[string]domain.SettleData{
		"American": {
			Hoders:    []string{"alice", "bob"},
			Dividends: map[string]int{"alice": 3000},
			Settled:   map[string]domain.MergingSettleResult{},
		},
	}
	payload, err := json.Marshal(MergingSettlePayload{Actions: []domain.MergingSettleItem{{
		Company: "American", SellAmount: 1, ExchangeAmount: 2,
	}}})
	if err != nil {
		t.Fatal(err)
	}

	HandleMergingSettleMessage(room, domain.Command{PlayerID: "alice", Payload: payload})

	settlement := room.State.MergeSettleData["American"]
	receipt := settlement.Settled["alice"]
	if receipt.HoldingBefore != 4 || receipt.SellAmount != 1 || receipt.ExchangeAmount != 2 {
		t.Fatalf("settlement receipt = %+v", receipt)
	}
}
