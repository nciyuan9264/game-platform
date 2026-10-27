package hex

import "time"

const boardSize = 11

type Side string

const (
	SideRed  Side = "red"
	SideTeal Side = "teal"
)

type Stone string

const (
	StoneEmpty Stone = ""
	StoneRed   Stone = "red"
	StoneTeal  Stone = "teal"
)

const (
	StatusMatch   = "match"
	StatusWaiting = "waiting"
	StatusPlaying = "playing"
	StatusEnd     = "end"
)

type Cell struct {
	Row    int `json:"row"`
	Column int `json:"column"`
}

type Player struct {
	UserID      string `json:"userID,omitempty"`
	PlayerID    string `json:"playerID"`
	DisplayName string `json:"displayName,omitempty"`
	Online      bool   `json:"online"`
	Ready       bool   `json:"ready"`
	GameReady   bool   `json:"gameReady,omitempty"`
	AI          bool   `json:"ai"`
	SeatIndex   int    `json:"seatIndex"`
	Side        Side   `json:"side,omitempty"`
}

type Action struct {
	ID        int    `json:"id"`
	Kind      string `json:"kind"`
	PlayerID  string `json:"playerID"`
	Side      Side   `json:"side"`
	Cell      *Cell  `json:"cell,omitempty"`
	Automatic bool   `json:"automatic"`
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

	Board         []Stone       `json:"board"`
	CurrentPlayer string        `json:"currentPlayer,omitempty"`
	RedPlayerID   string        `json:"redPlayerID,omitempty"`
	TealPlayerID  string        `json:"tealPlayerID,omitempty"`
	FirstPlayerID string        `json:"firstPlayerID,omitempty"`
	LastAction    *Action       `json:"lastAction,omitempty"`
	Log           []Action      `json:"log"`
	WinningPath   []Cell        `json:"winningPath"`
	WinnerID      string        `json:"winnerID,omitempty"`
	WinnerSide    Side          `json:"winnerSide,omitempty"`
	EndReason     string        `json:"endReason,omitempty"`
	GameStartTime time.Time     `json:"gameStartTime,omitempty"`
	TurnDeadline  time.Time     `json:"turnDeadline,omitempty"`
	TurnTimeout   time.Duration `json:"turnTimeout,omitempty"`
	NoHumanChecks int           `json:"noHumanChecks,omitempty"`
}

func other(side Side) Side {
	if side == SideRed {
		return SideTeal
	}
	return SideRed
}

func stoneFor(side Side) Stone {
	if side == SideRed {
		return StoneRed
	}
	return StoneTeal
}

func cloneState(state *State) *State {
	if state == nil {
		return nil
	}
	result := *state
	result.PlayerOrder = make([]string, len(state.PlayerOrder))
	copy(result.PlayerOrder, state.PlayerOrder)
	result.Board = make([]Stone, len(state.Board))
	copy(result.Board, state.Board)
	result.WinningPath = make([]Cell, len(state.WinningPath))
	copy(result.WinningPath, state.WinningPath)
	result.Roster = make(map[string]*Player, len(state.Roster))
	for id, player := range state.Roster {
		if player == nil {
			result.Roster[id] = nil
			continue
		}
		copyPlayer := *player
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
	return &result
}

func cloneAction(action Action) Action {
	result := action
	if action.Cell != nil {
		cell := *action.Cell
		result.Cell = &cell
	}
	return result
}
