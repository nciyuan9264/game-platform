package game

import (
	"testing"

	"github.com/nciyuan9264/game-platform-server/internal/acquire/domain/domain"
)

// TestEncodeDecodeStateSnapshot 验证瘦身 + gzip 编码后，解码能补齐完整 108 格棋盘，
// 且非空地格子、玩家、公司数据保真。
func TestEncodeDecodeStateSnapshot(t *testing.T) {
	// 只放几格有归属的 tile，其余留空（不放入 map，模拟瘦身前的完整棋盘里大量空地）。
	board := map[string]*domain.Tile{
		"1A":  {ID: "1A", Belong: "American"},
		"1B":  {ID: "1B", Belong: "American"},
		"12I": {ID: "12I", Belong: "Tower"},
		"5E":  {ID: "5E", Belong: ""}, // 空地：应被瘦身丢弃，解码后补回 belong=""
	}
	state := &domain.GameState{
		CurrentPlayer: "ai_001",
		RoomStatus:    domain.RoomStatusBuyStock,
		LastTileKey:   "12I",
		BoardTiles:    board,
		Players: map[string]*domain.PlayerState{
			"ai_001": {Money: 5000, Stocks: map[string]int{"American": 3}, Tiles: []string{"2A"}},
		},
		Companies: map[string]*domain.CompanyState{
			"American": {Name: "American", Tiles: 2, StockTotal: 22, StockPrice: 300},
		},
	}
	result := map[string]interface{}{"ai_001": map[string]interface{}{"money": 5000}}

	blob, err := EncodeStateSnapshot(state, result)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if len(blob) < 2 || blob[0] != 0x1f || blob[1] != 0x8b {
		t.Fatalf("blob 不是 gzip 格式")
	}

	got, gotResult, ok := DecodeStateSnapshot(blob)
	if !ok {
		t.Fatal("decode failed")
	}

	// 棋盘必须补齐为 108 格。
	if len(got.BoardTiles) != 108 {
		t.Fatalf("解码后棋盘格数 = %d, 期望 108", len(got.BoardTiles))
	}
	// 非空地格子保真。
	if got.BoardTiles["1A"].Belong != "American" || got.BoardTiles["12I"].Belong != "Tower" {
		t.Fatalf("非空地格子归属错误: 1A=%q 12I=%q", got.BoardTiles["1A"].Belong, got.BoardTiles["12I"].Belong)
	}
	// 被瘦身的空地补回，belong 为空。
	if got.BoardTiles["5E"].Belong != "" || got.BoardTiles["9H"].Belong != "" {
		t.Fatalf("空地补齐错误: 5E=%q 9H=%q", got.BoardTiles["5E"].Belong, got.BoardTiles["9H"].Belong)
	}
	// 其它状态保真。
	if got.CurrentPlayer != "ai_001" || got.RoomStatus != domain.RoomStatusBuyStock || got.LastTileKey != "12I" {
		t.Fatalf("标量状态丢失: %+v", got)
	}
	if got.Players["ai_001"].Money != 5000 || got.Players["ai_001"].Stocks["American"] != 3 {
		t.Fatalf("玩家状态错误: %+v", got.Players["ai_001"])
	}
	if got.Companies["American"].StockPrice != 300 {
		t.Fatalf("公司状态错误: %+v", got.Companies["American"])
	}
	if gotResult["ai_001"] == nil {
		t.Fatal("result 丢失")
	}
}

// TestDecodeLegacyUncompressedJSON 验证老对局未压缩 JSON 也能被解码并补齐棋盘。
func TestDecodeLegacyUncompressedJSON(t *testing.T) {
	legacy := []byte(`{"state":{"currentPlayer":"ai_002","roomStatus":"setTile","boardTiles":{"3C":{"id":"3C","belong":"Tower"}},"players":{},"companies":{}},"result":{}}`)
	got, _, ok := DecodeStateSnapshot(legacy)
	if !ok {
		t.Fatal("legacy decode failed")
	}
	if len(got.BoardTiles) != 108 {
		t.Fatalf("legacy 解码后棋盘格数 = %d, 期望 108", len(got.BoardTiles))
	}
	if got.BoardTiles["3C"].Belong != "Tower" {
		t.Fatalf("legacy 非空地格子丢失: %q", got.BoardTiles["3C"].Belong)
	}
}
