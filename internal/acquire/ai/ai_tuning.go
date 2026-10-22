package roompkg

import "sort"

type TuningConfig struct {
	Candidates      int
	GridCandidates  bool
	GridLimit       int
	Games           int
	Players         int
	Seed            uint64
	MaxTurns        int
	SearchDepth     int
	BeamWidth       int
	OnCandidateDone func(done int, total int, result TuningResult)
}

type TuningResult struct {
	Name    string      `json:"name"`
	Weights AIWeights   `json:"weights"`
	Result  ArenaResult `json:"result"`
	Score   int         `json:"score"`
}

type TuningSummary struct {
	BaselineName string         `json:"baselineName"`
	Baseline     AIWeights      `json:"baseline"`
	Best         TuningResult   `json:"best"`
	Results      []TuningResult `json:"results"`
}

func RunAIWeightTuning(config TuningConfig) TuningSummary {
	if config.GridCandidates {
		candidates := GenerateGridTuningCandidates(config.GridLimit)
		return runAIWeightTuningWithCandidates(config, candidates)
	}
	if config.Candidates <= 0 {
		config.Candidates = len(localAIWeightPresets())
	}
	candidates := GenerateTuningCandidates(config.Candidates, config.Seed)
	return runAIWeightTuningWithCandidates(config, candidates)
}

func runAIWeightTuningWithCandidates(config TuningConfig, candidates []NamedAIWeights) TuningSummary {
	results := make([]TuningResult, 0, len(candidates))
	for _, candidate := range candidates {
		arenaResult := RunAIArena(ArenaConfig{
			Games:       config.Games,
			Players:     config.Players,
			Seed:        config.Seed,
			MaxTurns:    config.MaxTurns,
			SearchDepth: config.SearchDepth,
			BeamWidth:   config.BeamWidth,
		}, candidate.Weights, onlineAIWeights)
		results = append(results, TuningResult{
			Name:    candidate.Name,
			Weights: candidate.Weights,
			Result:  arenaResult,
			Score:   scoreTuningResult(arenaResult),
		})
		if config.OnCandidateDone != nil {
			config.OnCandidateDone(len(results), len(candidates), results[len(results)-1])
		}
	}
	results = RankTuningResults(results)
	summary := TuningSummary{
		BaselineName: "online",
		Baseline:     onlineAIWeights,
		Results:      results,
	}
	if len(results) > 0 {
		summary.Best = results[0]
	}
	return summary
}

func RankTuningResults(results []TuningResult) []TuningResult {
	ranked := append([]TuningResult(nil), results...)
	for i := range ranked {
		if ranked[i].Score == 0 {
			ranked[i].Score = scoreTuningResult(ranked[i].Result)
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		if ranked[i].Result.AverageValueDelta != ranked[j].Result.AverageValueDelta {
			return ranked[i].Result.AverageValueDelta > ranked[j].Result.AverageValueDelta
		}
		return ranked[i].Name < ranked[j].Name
	})
	return ranked
}

func scoreTuningResult(result ArenaResult) int {
	return result.CandidateWins*10000 - result.BaselineWins*10000 + int(result.AverageValueDelta)
}
