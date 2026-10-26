package roompkg

import (
	"testing"

	"github.com/nciyuan9264/game-platform/internal/plugins/acquire/domain/domain"
)

func TestEvaluateRoomRewardsMajorityControl(t *testing.T) {
	r := newAITestRoom()
	r.State.Companies["Tower"].Tiles = 6
	r.State.Companies["Tower"].StockPrice = 500
	r.State.Players["ai_001"].Money = 3000
	r.State.Players["human_001"].Money = 3000

	r.State.Players["ai_001"].Stocks["Tower"] = 4
	r.State.Players["human_001"].Stocks["Tower"] = 2
	majorityScore := evaluateRoomForPlayer(r, "ai_001", defaultAIWeights)

	r.State.Players["ai_001"].Stocks["Tower"] = 1
	r.State.Players["human_001"].Stocks["Tower"] = 5
	minorityScore := evaluateRoomForPlayer(r, "ai_001", defaultAIWeights)

	if majorityScore <= minorityScore {
		t.Fatalf("majority score = %d, minority score = %d; want majority higher", majorityScore, minorityScore)
	}
}

func TestEvaluateRoomPenalizesLeadingHuman(t *testing.T) {
	r := newAITestRoom()
	r.State.Companies["Tower"].Tiles = 8
	r.State.Companies["Tower"].StockPrice = 600
	r.State.Players["ai_001"].Money = 3000
	r.State.Players["ai_001"].Stocks["Tower"] = 1

	r.State.Players["human_001"].Money = 3000
	r.State.Players["human_001"].Stocks["Tower"] = 1
	tiedScore := evaluateRoomForPlayer(r, "ai_001", defaultAIWeights)

	r.State.Players["human_001"].Money = 9000
	r.State.Players["human_001"].Stocks["Tower"] = 6
	leadingHumanScore := evaluateRoomForPlayer(r, "ai_001", defaultAIWeights)

	if leadingHumanScore >= tiedScore {
		t.Fatalf("leading human score = %d, tied score = %d; want leading human penalty", leadingHumanScore, tiedScore)
	}
}

func TestEvaluateRoomRewardsTopRankInMultiplayer(t *testing.T) {
	r := newAITestRoom()
	addEvaluatorTestPlayer(r, "human_002", 8000)
	r.State.MaxPlayers = 3
	r.State.Players["ai_001"].Money = 10000
	r.State.Players["human_001"].Money = 9000
	weights := AIWeights{TopRankBonus: 1000}

	topScore := evaluateRoomForPlayer(r, "ai_001", weights)
	secondScore := evaluateRoomForPlayer(r, "human_001", weights)

	if topScore <= secondScore {
		t.Fatalf("top score = %d, second score = %d; want top rank bonus", topScore, secondScore)
	}
}

func TestEvaluateRoomPenalizesLeaderGapInMultiplayer(t *testing.T) {
	r := newAITestRoom()
	addEvaluatorTestPlayer(r, "human_002", 7000)
	r.State.MaxPlayers = 3
	r.State.Players["ai_001"].Money = 6000
	r.State.Players["human_001"].Money = 10000
	weights := AIWeights{LeaderGapPenalty: 2}

	score := evaluateRoomForPlayer(r, "ai_001", weights)

	if score >= 0 {
		t.Fatalf("multiplayer leader gap score = %d, want negative penalty", score)
	}
}

func TestEvaluateRoomPenalizesSecondPlaceInMultiplayer(t *testing.T) {
	r := newAITestRoom()
	addEvaluatorTestPlayer(r, "human_002", 1000)
	r.State.MaxPlayers = 3
	r.State.Players["ai_001"].Money = 9000
	r.State.Players["human_001"].Money = 10000
	weights := AIWeights{SecondPlacePenalty: 500}

	score := evaluateRoomForPlayer(r, "ai_001", weights)

	if score != -500 {
		t.Fatalf("second place score = %d, want -500", score)
	}
}

func addEvaluatorTestPlayer(r *domain.Room, playerID string, money int) {
	r.State.Players[playerID] = &domain.PlayerState{
		Money:  money,
		Tiles:  []string{"4A", "4B"},
		Stocks: map[string]int{"Tower": 0, "Sackson": 0},
	}
}
