package game

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"

	"github.com/nciyuan9264/game-platform-server/internal/acquire/domain/domain"
)

// snapshotEnvelope 是 state_snapshot / state_blob 的载荷结构：权威 GameState + 派生 result。
type snapshotEnvelope struct {
	State  *domain.GameState      `json:"state"`
	Result map[string]interface{} `json:"result"`
}

// EncodeStateSnapshot 把一帧权威状态编码为可落库的字节：
//  1. 瘦身 BoardTiles——只保留 belong != "" 的格子（空地占 108 格中的大多数，回放时再补齐）；
//  2. JSON 序列化整个 envelope；
//  3. gzip 压缩（高度重复的 JSON 压缩比很高）。
//
// 不修改传入的 state（对 BoardTiles 做浅拷贝过滤），可安全在录制循环中调用。
func EncodeStateSnapshot(state *domain.GameState, result map[string]interface{}) ([]byte, error) {
	env := snapshotEnvelope{State: thinState(state), Result: result}
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeStateSnapshot 解码一帧快照字节，兼容两种来源：
//   - 新对局：gzip(JSON)（瘦身后的 BoardTiles）；
//   - 老对局：未压缩的 JSON（jsonb 全量 BoardTiles）。
//
// 无论哪种，最终都会把 BoardTiles 补齐为完整 12x9 棋盘（空地 belong=""），保证回放渲染一致。
func DecodeStateSnapshot(raw []byte) (*domain.GameState, map[string]interface{}, bool) {
	if len(raw) == 0 {
		return nil, nil, false
	}

	jsonBytes := raw
	// gzip 魔数 0x1f 0x8b：是则先解压。
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, nil, false
		}
		defer zr.Close()
		decompressed, err := io.ReadAll(zr)
		if err != nil {
			return nil, nil, false
		}
		jsonBytes = decompressed
	}

	var env snapshotEnvelope
	if err := json.Unmarshal(jsonBytes, &env); err != nil || env.State == nil {
		return nil, nil, false
	}
	if env.State.Players == nil {
		env.State.Players = map[string]*domain.PlayerState{}
	}
	if env.State.Companies == nil {
		env.State.Companies = map[string]*domain.CompanyState{}
	}
	rehydrateBoard(env.State)
	return env.State, env.Result, true
}

// thinState 浅拷贝 GameState，并把 BoardTiles 过滤为仅含非空地格子，不改动原 state。
func thinState(s *domain.GameState) *domain.GameState {
	if s == nil {
		return nil
	}
	cp := *s
	bt := make(map[string]*domain.Tile, len(s.BoardTiles))
	for k, t := range s.BoardTiles {
		if t != nil && t.Belong != "" {
			bt[k] = t
		}
	}
	cp.BoardTiles = bt
	return &cp
}

// rehydrateBoard 把（可能被瘦身过的）BoardTiles 补齐为完整 12x9 棋盘，
// 缺失格子填充为空地（belong=""），与 HandleMatchBegin 的初始化方式一致。
func rehydrateBoard(s *domain.GameState) {
	full := make(map[string]*domain.Tile, 108)
	for col := 1; col <= 12; col++ {
		for row := 'A'; row <= 'I'; row++ {
			id := fmt.Sprintf("%d%c", col, row)
			if t, ok := s.BoardTiles[id]; ok && t != nil {
				full[id] = t
			} else {
				full[id] = &domain.Tile{ID: id, Belong: ""}
			}
		}
	}
	s.BoardTiles = full
}
