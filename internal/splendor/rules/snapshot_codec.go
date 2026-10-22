package rules

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"

	"github.com/nciyuan9264/game-platform-server/internal/splendor/domain"
)

// snapshotEnvelope 是 state_blob 的载荷结构：权威 GameState + 派生 result。
type snapshotEnvelope struct {
	State  *domain.GameState      `json:"state"`
	Result map[string]interface{} `json:"result"`
}

// EncodeStateSnapshot 把一帧权威状态编码为可落库的字节：
//  1. JSON 序列化整个 envelope；
//  2. gzip 压缩（高度重复的 JSON 压缩比很高）。
//
// splendor 的 GameState 体积本就不大（无 acquire 的 108 格棋盘），无需瘦身。
func EncodeStateSnapshot(state *domain.GameState, result map[string]interface{}) ([]byte, error) {
	env := snapshotEnvelope{State: state, Result: result}
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
//   - 新对局：gzip(JSON)；
//   - 老对局/兜底：未压缩的 JSON。
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
	return env.State, env.Result, true
}
