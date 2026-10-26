package quoridor

import "time"

type Side string

const (
	SideSouth Side = "south"
	SideNorth Side = "north"
)

type Orientation string

const (
	OrientationHorizontal Orientation = "h"
	OrientationVertical   Orientation = "v"
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

type WallSpec struct {
	Row         int         `json:"row"`
	Column      int         `json:"column"`
	Orientation Orientation `json:"orientation"`
}

type Wall struct {
	Row         int         `json:"row"`
	Column      int         `json:"column"`
	Orientation Orientation `json:"orientation"`
	Side        Side        `json:"side"`
}

type WallOption struct {
	Row         int         `json:"row"`
	Column      int         `json:"column"`
	Orientation Orientation `json:"orientation"`
	Legal       bool        `json:"legal"`
	ReasonCode  string      `json:"reasonCode,omitempty"`
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
	ID        int       `json:"id"`
	Kind      string    `json:"kind"`
	PlayerID  string    `json:"playerID"`
	Side      Side      `json:"side"`
	From      *Cell     `json:"from,omitempty"`
	To        *Cell     `json:"to,omitempty"`
	Wall      *WallSpec `json:"wall,omitempty"`
	Automatic bool      `json:"automatic"`
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

	Pawns         map[Side]Cell `json:"pawns"`
	Walls         []Wall        `json:"walls"`
	WallStock     map[Side]int  `json:"wallStock"`
	CurrentPlayer string        `json:"currentPlayer,omitempty"`
	SouthPlayerID string        `json:"southPlayerID,omitempty"`
	NorthPlayerID string        `json:"northPlayerID,omitempty"`
	FirstPlayerID string        `json:"firstPlayerID,omitempty"`
	LastAction    *Action       `json:"lastAction,omitempty"`
	Log           []Action      `json:"log"`
	WinnerID      string        `json:"winnerID,omitempty"`
	WinnerSide    Side          `json:"winnerSide,omitempty"`
	EndReason     string        `json:"endReason,omitempty"`
	GameStartTime time.Time     `json:"gameStartTime,omitempty"`
	TurnDeadline  time.Time     `json:"turnDeadline,omitempty"`
	TurnTimeout   time.Duration `json:"turnTimeout,omitempty"`
	NoHumanChecks int           `json:"noHumanChecks,omitempty"`
}

type candidate struct {
	Kind string
	To   Cell
	Wall WallSpec
}

func other(side Side) Side {
	if side == SideSouth {
		return SideNorth
	}
	return SideSouth
}

func targetRow(side Side) int {
	if side == SideSouth {
		return 0
	}
	return 8
}

func cloneState(state *State) *State {
	if state == nil {
		return nil
	}
	result := *state
	result.PlayerOrder = make([]string, len(state.PlayerOrder))
	copy(result.PlayerOrder, state.PlayerOrder)
	result.Roster = make(map[string]*Player, len(state.Roster))
	for id, player := range state.Roster {
		if player == nil {
			result.Roster[id] = nil
			continue
		}
		copyPlayer := *player
		result.Roster[id] = &copyPlayer
	}
	result.Pawns = make(map[Side]Cell, len(state.Pawns))
	for side, cell := range state.Pawns {
		result.Pawns[side] = cell
	}
	result.Walls = make([]Wall, len(state.Walls))
	copy(result.Walls, state.Walls)
	result.WallStock = make(map[Side]int, len(state.WallStock))
	for side, stock := range state.WallStock {
		result.WallStock[side] = stock
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
	if action.From != nil {
		value := *action.From
		result.From = &value
	}
	if action.To != nil {
		value := *action.To
		result.To = &value
	}
	if action.Wall != nil {
		value := *action.Wall
		result.Wall = &value
	}
	return result
}
