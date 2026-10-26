package roompkg

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/plugins/acquire/domain/domain"
)

func markCompanyTiles(r *domain.Room, company string, tiles ...string) {
	for _, tile := range tiles {
		r.State.BoardTiles[tile].Belong = company
	}
}

func TestSearchChoosesMajorityStockPurchase(t *testing.T) {
	r := newAITestRoom()
	r.State.CurrentPlayer = "ai_001"
	r.State.RoomStatus = domain.RoomStatusBuyStock
	r.State.Players["ai_001"].Money = 600
	r.State.Players["ai_001"].Stocks["Tower"] = 2
	r.State.Players["human_001"].Stocks["Tower"] = 2
	r.State.Players["ai_001"].Stocks["Sackson"] = 0
	r.State.Players["human_001"].Stocks["Sackson"] = 6
	r.State.Companies["Tower"].StockPrice = 200
	r.State.Companies["Tower"].StockTotal = 21
	r.State.Companies["Sackson"].StockPrice = 300
	r.State.Companies["Sackson"].StockTotal = 19
	markCompanyTiles(r, "Tower", "5A", "5B")
	markCompanyTiles(r, "Sackson", "7A", "7B", "7C")

	action, ok := chooseBestActionBySearch(r, "ai_001", domain.RoomStatusBuyStock, nil, AISearchConfig{
		Depth:       1,
		BeamWidth:   6,
		ActionLimit: 20,
		Weights:     defaultAIWeights,
	})
	if !ok {
		t.Fatalf("search returned no action")
	}
	if action.Kind != aiActionBuyStock || action.Stocks["Tower"] == 0 {
		t.Fatalf("action = %+v, want buying Tower to take majority", action)
	}
}

func TestSearchAvoidsBenefitingLeaderMerge(t *testing.T) {
	r := newAITestRoom()
	r.State.CurrentPlayer = "ai_001"
	r.State.RoomStatus = domain.RoomStatusSetTile
	r.State.Players["ai_001"].Tiles = []string{"2B", "8H"}
	r.State.Players["ai_001"].Stocks["Tower"] = 4
	r.State.Players["ai_001"].Stocks["Sackson"] = 0
	r.State.Players["human_001"].Money = 9000
	r.State.Players["human_001"].Stocks["Sackson"] = 6
	r.State.Players["human_001"].Stocks["Tower"] = 0
	r.State.Companies["Tower"].Tiles = 3
	r.State.Companies["Tower"].StockPrice = 300
	r.State.Companies["Sackson"].Tiles = 2
	r.State.Companies["Sackson"].StockPrice = 200
	markCompanyTiles(r, "Tower", "1B", "8G", "8F")
	markCompanyTiles(r, "Sackson", "3B", "3C")

	action, ok := chooseBestActionBySearch(r, "ai_001", domain.RoomStatusSetTile, nil, AISearchConfig{
		Depth:       1,
		BeamWidth:   4,
		ActionLimit: 10,
		Weights:     defaultAIWeights,
	})
	if !ok {
		t.Fatalf("search returned no action")
	}
	if action.Kind != aiActionPlaceTile || action.TileKey != "8H" {
		t.Fatalf("action = %+v, want safe expansion 8H instead of leader-benefiting merge 2B", action)
	}
}

func TestSearchValueUsesDepthThreeContinuation(t *testing.T) {
	r := newAITestRoom()
	r.State.CurrentPlayer = "human_001"
	r.State.RoomStatus = domain.RoomStatusSetTile
	r.State.Players["human_001"].Tiles = []string{"2A", "2B"}
	cfg := AISearchConfig{Depth: 3, BeamWidth: 3, ActionLimit: 8, Weights: defaultAIWeights}

	immediate := evaluateRoomForPlayer(r, "ai_001", cfg.Weights)
	deep := searchValueForPlayer(r, "ai_001", 3, cfg, time.Now())

	if deep == immediate {
		t.Fatalf("depth-3 value did not include continuation: got %d, immediate %d", deep, immediate)
	}
}

func TestSearchValueUsesDepthFourContinuation(t *testing.T) {
	r := newAITestRoom()
	r.State.CurrentPlayer = "human_001"
	r.State.RoomStatus = domain.RoomStatusSetTile
	r.State.Players["human_001"].Tiles = []string{"2A", "2B"}
	cfg := AISearchConfig{Depth: 4, BeamWidth: 3, ActionLimit: 8, Weights: defaultAIWeights}

	depthThree := searchValueForPlayer(r, "ai_001", 3, cfg, time.Now())
	depthFour := searchValueForPlayer(r, "ai_001", 4, cfg, time.Now())

	if depthFour == depthThree {
		t.Fatalf("depth-4 value did not add another ply: depth3=%d depth4=%d", depthThree, depthFour)
	}
}

func TestBuildAIActionMsgFallsBackWhenSearchDisabled(t *testing.T) {
	originalConfig := aiSearchConfigForRuntime
	aiSearchConfigForRuntime = AISearchConfig{ActionLimit: 0}
	t.Cleanup(func() { aiSearchConfigForRuntime = originalConfig })

	r := newAITestRoom()
	r.State.CurrentPlayer = "ai_001"
	r.State.RoomStatus = domain.RoomStatusSetTile
	r.State.Players["ai_001"].Tiles = []string{"3A"}

	cmdType, payload, ok := buildAIActionMsg(r, "ai_001", domain.RoomStatusSetTile, nil)
	if !ok {
		t.Fatalf("buildAIActionMsg returned ok=false")
	}
	if cmdType != "game_place_tile" {
		t.Fatalf("cmdType = %q, want game_place_tile", cmdType)
	}
	if len(payload) == 0 {
		t.Fatalf("payload is empty")
	}
}

func TestBuildAIActionMsgLogsSearchStrategy(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	r := newAITestRoom()
	r.State.CurrentPlayer = "ai_001"
	r.State.RoomStatus = domain.RoomStatusSetTile
	r.State.Players["ai_001"].Tiles = []string{"3A"}

	_, _, ok := buildAIActionMsg(r, "ai_001", domain.RoomStatusSetTile, nil)
	if !ok {
		t.Fatalf("buildAIActionMsg returned ok=false")
	}
	if !strings.Contains(buf.String(), `"ai_strategy":"search"`) {
		t.Fatalf("log missing search strategy: %s", buf.String())
	}
}

func TestOnlineAISearchConfigUsesPlayerCount(t *testing.T) {
	r := newAITestRoom()
	r.State.MaxPlayers = 4

	cfg, configName, players := onlineAISearchConfigForRoom(r)

	if players != 4 {
		t.Fatalf("players = %d, want 4", players)
	}
	if configName != "online_4p_auto002_fallback" {
		t.Fatalf("config name = %q, want online_4p_auto002_fallback", configName)
	}
	if cfg.Weights != onlineAIWeights {
		t.Fatalf("4p fallback weights = %+v, want online weights %+v", cfg.Weights, onlineAIWeights)
	}
}

func TestOnlineAISearchConfigFallsBackToTwoPlayers(t *testing.T) {
	_, configName, players := onlineAISearchConfigForRoom(nil)
	if players != 2 {
		t.Fatalf("nil room players = %d, want 2", players)
	}
	if configName != "online_2p_auto002" {
		t.Fatalf("nil room config = %q, want online_2p_auto002", configName)
	}
}

func TestBuildAIActionMsgLogsPlayerCountConfig(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	r := newAITestRoom()
	r.State.MaxPlayers = 4
	r.State.CurrentPlayer = "ai_001"
	r.State.RoomStatus = domain.RoomStatusSetTile
	r.State.Players["ai_001"].Tiles = []string{"3A"}

	_, _, ok := buildAIActionMsg(r, "ai_001", domain.RoomStatusSetTile, nil)
	if !ok {
		t.Fatalf("buildAIActionMsg returned ok=false")
	}
	logs := buf.String()
	if !strings.Contains(logs, `"ai_players":4`) {
		t.Fatalf("log missing ai_players: %s", logs)
	}
	if !strings.Contains(logs, `"ai_config":"online_4p_auto002_fallback"`) {
		t.Fatalf("log missing ai_config: %s", logs)
	}
}

func TestBuildAIActionMsgLogsHeuristicStrategy(t *testing.T) {
	originalConfig := aiSearchConfigForRuntime
	aiSearchConfigForRuntime = AISearchConfig{ActionLimit: 0}
	t.Cleanup(func() { aiSearchConfigForRuntime = originalConfig })

	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	r := newAITestRoom()
	r.State.CurrentPlayer = "ai_001"
	r.State.RoomStatus = domain.RoomStatusSetTile
	r.State.Players["ai_001"].Tiles = []string{"3A"}

	_, _, ok := buildAIActionMsg(r, "ai_001", domain.RoomStatusSetTile, nil)
	if !ok {
		t.Fatalf("buildAIActionMsg returned ok=false")
	}
	if !strings.Contains(buf.String(), `"ai_strategy":"heuristic"`) {
		t.Fatalf("log missing heuristic strategy: %s", buf.String())
	}
}
