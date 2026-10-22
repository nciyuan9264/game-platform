package rules

import (
	"github.com/nciyuan9264/game-platform-server/internal/splendor/domain"
)

// RecomputeDerivedState 基于当前 r.State 重新计算每位玩家的分数，并产出 result。
//
// 该函数仅做纯计算与 state 同步更新，不做任何 IO（不广播、不写日志）。
// 同时被 BroadcastToRoom（每条命令处理后）和 replay 引擎（回放推进时）使用，
// 保证直播与回放的派生状态完全一致，避免分叉。
//
// 返回 result（每个玩家的 score / cards / nobles）。
func RecomputeDerivedState(r *domain.Room) map[string]interface{} {
	result := make(map[string]interface{})
	for pid, ps := range r.State.Players {
		if ps == nil {
			continue
		}
		score := 0
		for _, c := range ps.NormalCard {
			score += c.Points
		}
		for _, n := range ps.NobleCard {
			score += n.Points
		}
		ps.Score = score
		result[pid] = map[string]interface{}{
			"score":  score,
			"cards":  len(ps.NormalCard),
			"nobles": len(ps.NobleCard),
		}
	}
	return result
}
