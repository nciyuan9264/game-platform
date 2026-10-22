package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/nciyuan9264/game-platform/internal/acquire/ai"
)

func main() {
	if err := runCLI(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func runCLI(args []string, stdout io.Writer, stderr io.Writer) error {
	log.SetOutput(io.Discard)

	flags := flag.NewFlagSet("acquire-ai-arena", flag.ContinueOnError)
	flags.SetOutput(stderr)
	games := flags.Int("games", 20, "number of arena games to run")
	players := flags.Int("players", 2, "number of arena players, from 2 to 6")
	seed := flags.Uint64("seed", 1, "deterministic arena seed")
	depth := flags.Int("depth", 1, "AI search depth")
	beam := flags.Int("beam", 6, "AI search beam width")
	maxTurns := flags.Int("max-turns", 120, "maximum turns per game")
	jsonOut := flags.Bool("json", false, "print result as JSON")
	tune := flags.Bool("tune", false, "run local candidate weight tuning against online weights")
	candidateName := flags.String("candidate", "online", "candidate weights name for arena mode")
	baselineName := flags.String("baseline", "online", "baseline weights name for arena mode")
	candidates := flags.Int("candidates", 0, "number of generated candidates for tune mode")
	grid := flags.Bool("grid", false, "use deterministic grid candidates for tune mode")
	gridLimit := flags.Int("grid-limit", 0, "maximum deterministic grid candidates for tune mode; 0 means all")
	if err := flags.Parse(args); err != nil {
		return err
	}

	if *tune {
		summary := roompkg.RunAIWeightTuning(roompkg.TuningConfig{
			Candidates:     *candidates,
			GridCandidates: *grid,
			GridLimit:      *gridLimit,
			Games:          *games,
			Players:        *players,
			Seed:           *seed,
			MaxTurns:       *maxTurns,
			SearchDepth:    *depth,
			BeamWidth:      *beam,
			OnCandidateDone: func(done int, total int, result roompkg.TuningResult) {
				fmt.Fprintf(stderr, "tuning progress: %d/%d candidate=%s score=%d candidateWins=%d baselineWins=%d draws=%d averageValueDelta=%.2f averageTurns=%.2f\n",
					done,
					total,
					result.Name,
					result.Score,
					result.Result.CandidateWins,
					result.Result.BaselineWins,
					result.Result.Draws,
					result.Result.AverageValueDelta,
					result.Result.AverageTurns,
				)
			},
		})
		if *jsonOut {
			return writeJSON(stdout, summary)
		}
		fmt.Fprintf(stdout, "baseline=%s best=%s score=%d candidateWins=%d baselineWins=%d draws=%d averageValueDelta=%.2f\n",
			summary.BaselineName,
			summary.Best.Name,
			summary.Best.Score,
			summary.Best.Result.CandidateWins,
			summary.Best.Result.BaselineWins,
			summary.Best.Result.Draws,
			summary.Best.Result.AverageValueDelta,
		)
		return nil
	}

	candidateWeights, ok := roompkg.AIWeightsByName(*candidateName)
	if !ok {
		return fmt.Errorf("unknown candidate weights %q", *candidateName)
	}
	baselineWeights, ok := roompkg.AIWeightsByName(*baselineName)
	if !ok {
		return fmt.Errorf("unknown baseline weights %q", *baselineName)
	}

	result := roompkg.RunAIArena(roompkg.ArenaConfig{
		Games:       *games,
		Players:     *players,
		Seed:        *seed,
		MaxTurns:    *maxTurns,
		SearchDepth: *depth,
		BeamWidth:   *beam,
	}, candidateWeights, baselineWeights)

	if *jsonOut {
		return writeJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "candidate=%s baseline=%s players=%d games=%d candidateWins=%d baselineWins=%d draws=%d averageValueDelta=%.2f averageTurns=%.2f\n",
		*candidateName,
		*baselineName,
		result.Players,
		result.Games,
		result.CandidateWins,
		result.BaselineWins,
		result.Draws,
		result.AverageValueDelta,
		result.AverageTurns)
	return nil
}

func writeJSON(w io.Writer, value interface{}) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	return nil
}
