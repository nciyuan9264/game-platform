package roompkg

import (
	"math"
	"sort"
	"time"

	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
)

type AISearchConfig struct {
	// Depth 控制搜索向前看的动作步数；1 表示只评估当前动作，2/3/4 会继续沿真实行动玩家递归展开后续动作。
	Depth int
	// BeamWidth 控制每层搜索保留的高分候选动作数量，越大越强但越慢。
	BeamWidth int
	// ActionLimit 控制每个阶段最多枚举多少个候选动作，0 表示关闭搜索并回退启发式。
	ActionLimit int
	// Weights 控制局面评估器使用的打分权重。
	Weights AIWeights
}

var defaultAISearchConfig = AISearchConfig{
	Depth:       1,
	BeamWidth:   6,
	ActionLimit: 24,
	Weights:     defaultAIWeights,
}

var aiSearchConfigForRuntime = defaultAISearchConfig

func onlineAISearchConfigForRoom(r *domain.Room) (AISearchConfig, string, int) {
	players := onlineAIPlayerCountForRoom(r)
	weightsName, weights := onlineAIWeightsForPlayers(players)
	cfg := aiSearchConfigForRuntime
	cfg.Weights = weights
	return cfg, weightsName, players
}

func onlineAIPlayerCountForRoom(r *domain.Room) int {
	if r == nil || r.State == nil {
		return 2
	}
	if r.State.MaxPlayers > 0 {
		return normalizeArenaPlayers(r.State.MaxPlayers)
	}
	if len(r.State.Players) > 0 {
		return normalizeArenaPlayers(len(r.State.Players))
	}
	return 2
}

type scoredAIAction struct {
	action aiAction
	room   *domain.Room
	score  int
}

func chooseBestActionBySearch(r *domain.Room, playerID string, status domain.RoomStatus, explicitMainCompany []string, cfg AISearchConfig) (aiAction, bool) {
	if cfg.ActionLimit == 0 {
		return aiAction{}, false
	}
	if cfg.Depth <= 0 {
		cfg.Depth = 1
	}
	if cfg.BeamWidth <= 0 {
		cfg.BeamWidth = 1
	}
	if cfg.ActionLimit < 0 {
		cfg.ActionLimit = 24
	}
	if cfg.Weights == (AIWeights{}) {
		cfg.Weights = defaultAIWeights
	}

	start := time.Now()
	actions := enumerateActions(r, playerID, status, explicitMainCompany, cfg.ActionLimit)
	if len(actions) == 0 {
		return aiAction{}, false
	}

	scored := make([]scoredAIAction, 0, len(actions))
	for _, action := range actions {
		if time.Since(start) > 200*time.Millisecond {
			break
		}
		sim, ok := simulateAction(r, playerID, action)
		if !ok {
			continue
		}
		score := evaluateRoomForPlayer(sim, playerID, cfg.Weights)
		if cfg.Depth > 1 {
			score = searchValueForPlayer(sim, playerID, cfg.Depth-1, cfg, start)
		}
		scored = append(scored, scoredAIAction{action: action, room: sim, score: score})
	}
	if len(scored) == 0 {
		return aiAction{}, false
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return actionTieBreakKey(scored[i].action) < actionTieBreakKey(scored[j].action)
	})
	if len(scored) > cfg.BeamWidth {
		scored = scored[:cfg.BeamWidth]
	}

	return scored[0].action, true
}

func searchValueForPlayer(r *domain.Room, rootPlayerID string, remainingDepth int, cfg AISearchConfig, start time.Time) int {
	if r == nil || r.State == nil || remainingDepth <= 0 || time.Since(start) > 200*time.Millisecond {
		return evaluateRoomForPlayer(r, rootPlayerID, cfg.Weights)
	}
	if r.State.RoomStatus == domain.RoomStatusEnd {
		return evaluateRoomForPlayer(r, rootPlayerID, cfg.Weights)
	}

	actor := searchActionPlayerID(r)
	if actor == "" {
		return evaluateRoomForPlayer(r, rootPlayerID, cfg.Weights)
	}
	actions := enumerateActions(r, actor, r.State.RoomStatus, nil, cfg.ActionLimit)
	if len(actions) == 0 {
		return evaluateRoomForPlayer(r, rootPlayerID, cfg.Weights)
	}

	scored := make([]scoredAIAction, 0, len(actions))
	for _, action := range actions {
		if time.Since(start) > 200*time.Millisecond {
			break
		}
		sim, ok := simulateAction(r, actor, action)
		if !ok {
			continue
		}
		score := evaluateRoomForPlayer(sim, rootPlayerID, cfg.Weights)
		scored = append(scored, scoredAIAction{action: action, room: sim, score: score})
	}
	if len(scored) == 0 {
		return evaluateRoomForPlayer(r, rootPlayerID, cfg.Weights)
	}

	maximizing := actor == rootPlayerID
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			if maximizing {
				return scored[i].score > scored[j].score
			}
			return scored[i].score < scored[j].score
		}
		return actionTieBreakKey(scored[i].action) < actionTieBreakKey(scored[j].action)
	})
	if len(scored) > cfg.BeamWidth {
		scored = scored[:cfg.BeamWidth]
	}

	if maximizing {
		best := math.MinInt
		for _, item := range scored {
			if time.Since(start) > 200*time.Millisecond {
				break
			}
			best = max(best, searchValueForPlayer(item.room, rootPlayerID, remainingDepth-1, cfg, start))
		}
		if best != math.MinInt {
			return best
		}
	} else {
		worst := math.MaxInt
		for _, item := range scored {
			if time.Since(start) > 200*time.Millisecond {
				break
			}
			value := searchValueForPlayer(item.room, rootPlayerID, remainingDepth-1, cfg, start)
			if cfg.Weights.OpponentResponseCost > 1 {
				before := evaluateRoomForPlayer(r, rootPlayerID, cfg.Weights)
				value -= max(0, before-value) * (cfg.Weights.OpponentResponseCost - 1)
			}
			if value < worst {
				worst = value
			}
		}
		if worst != math.MaxInt {
			return worst
		}
	}
	return evaluateRoomForPlayer(r, rootPlayerID, cfg.Weights)
}

func searchActionPlayerID(r *domain.Room) string {
	return nextActionPlayerID(r)
}

func nextActionPlayerID(r *domain.Room) string {
	if r == nil || r.State == nil {
		return ""
	}
	if r.State.RoomStatus != domain.RoomStatusMergingSettle {
		return r.State.CurrentPlayer
	}
	companies := make([]string, 0, len(r.State.MergeSettleData))
	for company := range r.State.MergeSettleData {
		companies = append(companies, company)
	}
	sort.Strings(companies)
	for _, company := range companies {
		data := r.State.MergeSettleData[company]
		if len(data.Hoders) > 0 {
			return data.Hoders[0]
		}
	}
	return ""
}

func estimateOpponentBestResponseCost(r *domain.Room, playerID string, cfg AISearchConfig) int {
	opponent := chooseSearchOpponent(r, playerID)
	if opponent == "" {
		return 0
	}
	before := evaluateRoomForPlayer(r, playerID, cfg.Weights)
	actions := enumerateActions(r, opponent, r.State.RoomStatus, nil, cfg.BeamWidth)
	if len(actions) == 0 {
		return 0
	}
	worst := 0
	for _, action := range actions {
		sim, ok := simulateAction(r, opponent, action)
		if !ok {
			continue
		}
		after := evaluateRoomForPlayer(sim, playerID, cfg.Weights)
		worst = max(worst, before-after)
	}
	return worst * cfg.Weights.OpponentResponseCost
}

func chooseSearchOpponent(r *domain.Room, playerID string) string {
	if r == nil || r.State == nil {
		return ""
	}
	if leader, _ := leadingOpponent(r, playerID); leader != "" {
		return leader
	}
	best := ""
	bestValue := math.MinInt
	for pid := range r.State.Players {
		if pid == playerID {
			continue
		}
		if value := estimatePlayerTotalValue(r, pid); value > bestValue {
			bestValue = value
			best = pid
		}
	}
	return best
}

func actionTieBreakKey(action aiAction) string {
	_, payload, ok := payloadForAction(action)
	if !ok {
		return string(action.Kind)
	}
	return string(action.Kind) + ":" + string(payload)
}
