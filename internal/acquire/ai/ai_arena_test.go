package roompkg

import (
	"testing"

	"github.com/nciyuan9264/game-platform-server/internal/acquire/domain/domain"
)

func TestArenaDoesNotTouchHistoryOrCmdCh(t *testing.T) {
	sentinel := newArenaRoom(999)
	beforeSentinelCmds := len(sentinel.CmdCh)

	result := RunAIArena(ArenaConfig{
		Games:       2,
		Seed:        1,
		MaxTurns:    4,
		SearchDepth: 1,
		BeamWidth:   3,
	}, defaultAIWeights, defaultAIWeights)

	if result.Games != 2 {
		t.Fatalf("games = %d, want 2", result.Games)
	}
	if result.CandidateWins+result.BaselineWins+result.Draws != result.Games {
		t.Fatalf("result counts do not add up: %+v", result)
	}
	if result.AverageTurns <= 0 {
		t.Fatalf("average turns = %f, want > 0", result.AverageTurns)
	}
	if afterSentinelCmds := len(sentinel.CmdCh); afterSentinelCmds != beforeSentinelCmds {
		t.Fatalf("sentinel command channel changed: before=%d after=%d", beforeSentinelCmds, afterSentinelCmds)
	}
}

func TestArenaUsesSeedDeterministically(t *testing.T) {
	config := ArenaConfig{
		Games:       3,
		Seed:        42,
		MaxTurns:    8,
		SearchDepth: 1,
		BeamWidth:   3,
	}

	first := RunAIArena(config, defaultAIWeights, defaultAIWeights)
	second := RunAIArena(config, defaultAIWeights, defaultAIWeights)

	if first != second {
		t.Fatalf("same seed produced different results:\nfirst:  %+v\nsecond: %+v", first, second)
	}
}

func TestArenaSupportsConfiguredPlayerCount(t *testing.T) {
	room := newArenaRoom(123, 4)
	if got := len(room.Connections); got != 4 {
		t.Fatalf("connections = %d, want 4", got)
	}
	if got := len(room.State.Players); got != 4 {
		t.Fatalf("players = %d, want 4", got)
	}
	if room.State.MaxPlayers != 4 {
		t.Fatalf("max players = %d, want 4", room.State.MaxPlayers)
	}
	if _, ok := room.State.Players[arenaCandidatePlayerID]; !ok {
		t.Fatalf("candidate player missing")
	}
	for i := 1; i <= 3; i++ {
		playerID := arenaBaselinePlayerIDFor(i)
		if _, ok := room.State.Players[playerID]; !ok {
			t.Fatalf("baseline player %q missing", playerID)
		}
	}
}

func TestRunAIArenaReportsConfiguredPlayerCount(t *testing.T) {
	result := RunAIArena(ArenaConfig{
		Players:     4,
		Games:       2,
		Seed:        7,
		MaxTurns:    2,
		SearchDepth: 1,
		BeamWidth:   2,
	}, defaultAIWeights, defaultAIWeights)

	if result.Players != 4 {
		t.Fatalf("result players = %d, want 4", result.Players)
	}
	if result.CandidateWins+result.BaselineWins+result.Draws != result.Games {
		t.Fatalf("result counts do not add up: %+v", result)
	}
}

func TestArenaActionPlayerUsesMergeSettleHolder(t *testing.T) {
	room := newArenaRoom(123)
	room.State.CurrentPlayer = arenaCandidatePlayerID
	room.State.RoomStatus = domain.RoomStatusMergingSettle
	room.State.MergeSettleData = map[string]domain.SettleData{
		"Tower": {
			Hoders: []string{arenaBaselinePlayerID},
		},
	}

	playerID := arenaActionPlayerID(room)
	if playerID != arenaBaselinePlayerID {
		t.Fatalf("arena action player = %q, want merge holder %q", playerID, arenaBaselinePlayerID)
	}
}

func TestRankTuningResultsOrdersByScore(t *testing.T) {
	results := []TuningResult{
		{
			Name:    "weak",
			Weights: OnlineAIWeightsForArena(),
			Result:  ArenaResult{Games: 10, CandidateWins: 3, BaselineWins: 5, Draws: 2, AverageValueDelta: 100},
		},
		{
			Name:    "strong",
			Weights: OnlineAIWeightsForArena(),
			Result:  ArenaResult{Games: 10, CandidateWins: 6, BaselineWins: 2, Draws: 2, AverageValueDelta: 50},
		},
	}

	ranked := RankTuningResults(results)
	if ranked[0].Name != "strong" {
		t.Fatalf("top result = %q, want strong", ranked[0].Name)
	}
	if ranked[0].Score <= ranked[1].Score {
		t.Fatalf("scores not descending: %+v", ranked)
	}
}

func TestRunAIWeightTuningUsesOnlineWeightsAsBaseline(t *testing.T) {
	result := RunAIWeightTuning(TuningConfig{
		Candidates:  2,
		Games:       1,
		Seed:        7,
		MaxTurns:    2,
		SearchDepth: 1,
		BeamWidth:   2,
	})

	if result.BaselineName != "online" {
		t.Fatalf("baseline name = %q, want online", result.BaselineName)
	}
	if len(result.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(result.Results))
	}
	if result.Best.Name == "" {
		t.Fatalf("best result is empty")
	}
}

func TestRunAIWeightTuningReportsCandidateProgress(t *testing.T) {
	var calls []int
	result := RunAIWeightTuning(TuningConfig{
		Candidates:  3,
		Games:       1,
		Seed:        7,
		MaxTurns:    1,
		SearchDepth: 1,
		BeamWidth:   2,
		OnCandidateDone: func(done int, total int, result TuningResult) {
			if total != 3 {
				t.Fatalf("progress total = %d, want 3", total)
			}
			if result.Name == "" {
				t.Fatalf("progress result missing candidate name")
			}
			calls = append(calls, done)
		},
	})

	if len(result.Results) != 3 {
		t.Fatalf("results = %d, want 3", len(result.Results))
	}
	want := []int{1, 2, 3}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("progress calls = %v, want %v", calls, want)
		}
	}
}
