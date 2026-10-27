package hive

import "time"

type Side string

const (
	SideWhite Side = "white"
	SideBlack Side = "black"
)

type Kind string

const (
	KindQueen       Kind = "queen"
	KindAnt         Kind = "ant"
	KindBeetle      Kind = "beetle"
	KindGrasshopper Kind = "grasshopper"
	KindSpider      Kind = "spider"
)

const (
	StatusMatch   = "match"
	StatusWaiting = "waiting"
	StatusPlaying = "playing"
	StatusEnd     = "end"
)

type Hex struct {
	Q int `json:"q"`
	R int `json:"r"`
}

type Piece struct {
	PieceID string `json:"pieceID"`
	Side    Side   `json:"side"`
	Kind    Kind   `json:"kind"`
	Number  int    `json:"number"`
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
	PieceID   string `json:"pieceID,omitempty"`
	From      *Hex   `json:"from,omitempty"`
	To        *Hex   `json:"to,omitempty"`
	Path      []Hex  `json:"path"`
	Automatic bool   `json:"automatic"`
}

type Outcome struct {
	WinnerSide Side
	Draw       bool
	Reason     string
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

	Board             map[string][]string `json:"board"`
	Turns             map[Side]int        `json:"turns"`
	CurrentSide       Side                `json:"currentSide,omitempty"`
	CurrentPlayer     string              `json:"currentPlayer,omitempty"`
	WhitePlayerID     string              `json:"whitePlayerID,omitempty"`
	BlackPlayerID     string              `json:"blackPlayerID,omitempty"`
	FirstPlayerID     string              `json:"firstPlayerID,omitempty"`
	Log               []Action            `json:"log"`
	LastAction        *Action             `json:"lastAction,omitempty"`
	ConsecutivePasses int                 `json:"consecutivePasses,omitempty"`
	WinnerID          string              `json:"winnerID,omitempty"`
	WinnerSide        Side                `json:"winnerSide,omitempty"`
	Draw              bool                `json:"draw,omitempty"`
	EndReason         string              `json:"endReason,omitempty"`
	GameStartTime     time.Time           `json:"gameStartTime,omitempty"`
	TurnDeadline      time.Time           `json:"turnDeadline,omitempty"`
	TurnTimeout       time.Duration       `json:"turnTimeout,omitempty"`
	NoHumanChecks     int                 `json:"noHumanChecks,omitempty"`
}

type candidate struct {
	Kind    string
	PieceID string
	To      Hex
	Path    []Hex
}

func other(side Side) Side {
	if side == SideWhite {
		return SideBlack
	}
	return SideWhite
}

func sideName(side Side) string {
	if side == SideWhite {
		return "白方"
	}
	return "黑方"
}

func cloneState(state *State) *State {
	if state == nil {
		return nil
	}
	result := *state
	result.PlayerOrder = append([]string(nil), state.PlayerOrder...)
	result.Roster = make(map[string]*Player, len(state.Roster))
	for playerID, player := range state.Roster {
		if player == nil {
			continue
		}
		copyPlayer := *player
		result.Roster[playerID] = &copyPlayer
	}
	result.Board = make(map[string][]string, len(state.Board))
	for position, stack := range state.Board {
		result.Board[position] = append([]string(nil), stack...)
	}
	result.Turns = make(map[Side]int, len(state.Turns))
	for side, count := range state.Turns {
		result.Turns[side] = count
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
	result.Path = append([]Hex(nil), action.Path...)
	if action.From != nil {
		value := *action.From
		result.From = &value
	}
	if action.To != nil {
		value := *action.To
		result.To = &value
	}
	return result
}
