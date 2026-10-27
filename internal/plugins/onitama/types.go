package onitama

import "time"

type Side string

const (
	SideBlue Side = "blue"
	SideRed  Side = "red"
)

type PieceKind string

const (
	PieceMaster  PieceKind = "master"
	PieceStudent PieceKind = "student"
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

type Offset struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type Card struct {
	ID    string
	Name  string
	Mark  Side
	Moves []Offset
}

type Piece struct {
	ID   string    `json:"id"`
	Side Side      `json:"side"`
	Kind PieceKind `json:"kind"`
	Cell Cell      `json:"cell"`
}

type LegalMove struct {
	CardID          string `json:"cardID"`
	PieceID         string `json:"pieceID"`
	To              Cell   `json:"to"`
	CapturedPieceID string `json:"capturedPieceID,omitempty"`
	Winning         bool   `json:"winning"`
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
	ID              int       `json:"id"`
	Kind            string    `json:"kind"`
	PlayerID        string    `json:"playerID"`
	Side            Side      `json:"side"`
	CardID          string    `json:"cardID,omitempty"`
	PieceID         string    `json:"pieceID,omitempty"`
	From            *Cell     `json:"from,omitempty"`
	To              *Cell     `json:"to,omitempty"`
	CapturedPieceID string    `json:"capturedPieceID,omitempty"`
	CapturedKind    PieceKind `json:"capturedKind,omitempty"`
	ReceivedCardID  string    `json:"receivedCardID,omitempty"`
	Automatic       bool      `json:"automatic"`
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

	Pieces        map[string]*Piece `json:"pieces"`
	Hands         map[Side][]string `json:"hands"`
	DrawnCardIDs  []string          `json:"drawnCardIDs"`
	TransitCardID string            `json:"transitCardID,omitempty"`

	CurrentPlayer string        `json:"currentPlayer,omitempty"`
	BluePlayerID  string        `json:"bluePlayerID,omitempty"`
	RedPlayerID   string        `json:"redPlayerID,omitempty"`
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
	Kind    string
	CardID  string
	PieceID string
	To      Cell
}

func other(side Side) Side {
	if side == SideBlue {
		return SideRed
	}
	return SideBlue
}

func cloneState(state *State) *State {
	if state == nil {
		return nil
	}
	result := *state
	result.PlayerOrder = append([]string(nil), state.PlayerOrder...)
	result.DrawnCardIDs = append([]string(nil), state.DrawnCardIDs...)
	result.Roster = make(map[string]*Player, len(state.Roster))
	for id, player := range state.Roster {
		if player == nil {
			result.Roster[id] = nil
			continue
		}
		copyPlayer := *player
		result.Roster[id] = &copyPlayer
	}
	result.Pieces = make(map[string]*Piece, len(state.Pieces))
	for id, piece := range state.Pieces {
		if piece == nil {
			result.Pieces[id] = nil
			continue
		}
		copyPiece := *piece
		result.Pieces[id] = &copyPiece
	}
	result.Hands = make(map[Side][]string, len(state.Hands))
	for side, hand := range state.Hands {
		result.Hands[side] = append([]string(nil), hand...)
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
	return result
}
