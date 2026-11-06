package othello

import (
	"time"

	"github.com/nciyuan9264/game-platform/internal/game"
)

type Side string

const (
	SideBlack Side = "black"
	SideWhite Side = "white"
)

type Disc string

const (
	DiscEmpty Disc = ""
	DiscBlack Disc = "black"
	DiscWhite Disc = "white"
)

const (
	StatusMatch   = "match"
	StatusWaiting = "waiting"
	StatusPlaying = "playing"
	StatusPass    = "pass"
	StatusEnd     = "end"
)

type Player struct {
	UserID          string            `json:"userID,omitempty"`
	PlayerID        string            `json:"playerID"`
	DisplayName     string            `json:"displayName,omitempty"`
	Online          bool              `json:"online"`
	Ready           bool              `json:"ready"`
	GameReady       bool              `json:"gameReady,omitempty"`
	AI              bool              `json:"ai"`
	AIDifficulty    game.AIDifficulty `json:"aiDifficulty,omitempty"`
	SeatIndex       int               `json:"seatIndex"`
	Side            Side              `json:"side,omitempty"`
	HintsUsed       int               `json:"hintsUsed,omitempty"`
	HintPending     bool              `json:"hintPending,omitempty"`
	HintPositionKey string            `json:"hintPositionKey,omitempty"`
	Hint            *HintAdvice       `json:"hint,omitempty"`
}

type Move struct {
	Index int     `json:"index"`
	Flips []int   `json:"flips"`
	Lines [][]int `json:"lines"`
}

type Action struct {
	ID        int    `json:"id"`
	Kind      string `json:"kind"`
	PlayerID  string `json:"playerID"`
	Side      Side   `json:"side"`
	Index     *int   `json:"index,omitempty"`
	Flips     []int  `json:"flips"`
	Automatic bool   `json:"automatic"`
}

type PositionAnalysis struct {
	RequestID     string  `json:"requestID"`
	PositionKey   string  `json:"positionKey"`
	Status        string  `json:"status"`
	BestMove      *int    `json:"bestMove,omitempty"`
	BlackWinRate  float64 `json:"blackWinRate,omitempty"`
	DrawRate      float64 `json:"drawRate,omitempty"`
	WhiteWinRate  float64 `json:"whiteWinRate,omitempty"`
	Score         int     `json:"score,omitempty"`
	Depth         int     `json:"depth,omitempty"`
	ElapsedMs     int64   `json:"elapsedMs,omitempty"`
	Exact         bool    `json:"exact"`
	EngineVersion string  `json:"engineVersion"`
}

type HintAdvice struct {
	PositionKey string `json:"positionKey"`
	Index       int    `json:"index"`
}

type State struct {
	RoomID      string             `json:"roomID"`
	OwnerID     string             `json:"ownerID"`
	Status      string             `json:"status"`
	MaxPlayers  int                `json:"maxPlayers"`
	Visibility  string             `json:"visibility"`
	RandomSeed  uint64             `json:"randomSeed"`
	RoundNo     int                `json:"roundNo"`
	Roster      map[string]*Player `json:"roster"`
	PlayerOrder []string           `json:"playerOrder"`

	Board         []Disc            `json:"board"`
	CurrentPlayer string            `json:"currentPlayer,omitempty"`
	BlackPlayerID string            `json:"blackPlayerID,omitempty"`
	WhitePlayerID string            `json:"whitePlayerID,omitempty"`
	LastAction    *Action           `json:"lastAction,omitempty"`
	Log           []Action          `json:"log"`
	WinnerID      string            `json:"winnerID,omitempty"`
	WinnerSide    Side              `json:"winnerSide,omitempty"`
	EndReason     string            `json:"endReason,omitempty"`
	GameStartTime time.Time         `json:"gameStartTime,omitempty"`
	TurnDeadline  time.Time         `json:"turnDeadline,omitempty"`
	TurnTimeout   time.Duration     `json:"turnTimeout,omitempty"`
	NoHumanChecks int               `json:"noHumanChecks,omitempty"`
	Analysis      *PositionAnalysis `json:"analysis,omitempty"`
}

func other(side Side) Side {
	if side == SideBlack {
		return SideWhite
	}
	return SideBlack
}

func discFor(side Side) Disc {
	if side == SideBlack {
		return DiscBlack
	}
	return DiscWhite
}

func sideForDisc(disc Disc) Side {
	if disc == DiscBlack {
		return SideBlack
	}
	return SideWhite
}

func cloneState(state *State) *State {
	if state == nil {
		return nil
	}
	result := *state
	result.PlayerOrder = append([]string(nil), state.PlayerOrder...)
	result.Board = append([]Disc(nil), state.Board...)
	result.Roster = make(map[string]*Player, len(state.Roster))
	for id, player := range state.Roster {
		if player == nil {
			continue
		}
		copyPlayer := *player
		if player.Hint != nil {
			hint := *player.Hint
			copyPlayer.Hint = &hint
		}
		result.Roster[id] = &copyPlayer
	}
	result.Log = make([]Action, len(state.Log))
	for index, action := range state.Log {
		result.Log[index] = cloneAction(action)
	}
	if state.LastAction != nil {
		action := cloneAction(*state.LastAction)
		result.LastAction = &action
	}
	if state.Analysis != nil {
		analysis := *state.Analysis
		if state.Analysis.BestMove != nil {
			bestMove := *state.Analysis.BestMove
			analysis.BestMove = &bestMove
		}
		result.Analysis = &analysis
	}
	return &result
}

func cloneAction(action Action) Action {
	result := action
	result.Flips = append([]int{}, action.Flips...)
	if action.Index != nil {
		index := *action.Index
		result.Index = &index
	}
	return result
}
