package yinsh

import "time"

type Side string

const (
	SideWhite Side = "white"
	SideBlack Side = "black"
)

const (
	StatusMatch      = "match"
	StatusWaiting    = "waiting"
	StatusPlacement  = "placement"
	StatusMove       = "move"
	StatusRemoveRow  = "remove_row"
	StatusRemoveRing = "remove_ring"
	StatusEnd        = "end"
)

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

type MoveOption struct {
	To    string   `json:"to"`
	Flips []string `json:"flips"`
}

type Row struct {
	ID     string   `json:"id"`
	Points []string `json:"points"`
	Owner  Side     `json:"owner"`
}

type Action struct {
	ID       int            `json:"id"`
	Action   string         `json:"action"`
	PlayerID string         `json:"playerID"`
	Payload  map[string]any `json:"payload,omitempty"`
}

type State struct {
	RoomID       string             `json:"roomID"`
	OwnerID      string             `json:"ownerID"`
	Status       string             `json:"status"`
	MaxPlayers   int                `json:"maxPlayers"`
	Visibility   string             `json:"visibility"`
	RandomSeed   uint64             `json:"randomSeed"`
	RandomCount  uint64             `json:"randomCount"`
	RoundNo      int                `json:"roundNo"`
	Roster       map[string]*Player `json:"roster"`
	PlayerOrder  []string           `json:"playerOrder"`
	Rings        map[string]Side    `json:"rings"`
	Markers      map[string]Side    `json:"markers"`
	PlacedRings  map[Side]int       `json:"placedRings"`
	RingsRemoved map[Side]int       `json:"ringsRemoved"`

	CurrentPlayer     string   `json:"currentPlayer,omitempty"`
	MoverPlayer       string   `json:"moverPlayer,omitempty"`
	ResolverPlayer    string   `json:"resolverPlayer,omitempty"`
	Resolution        []string `json:"resolution,omitempty"`
	ConsecutivePasses int      `json:"consecutivePasses,omitempty"`

	LastAction    *Action       `json:"lastAction,omitempty"`
	Log           []Action      `json:"log"`
	WinnerID      string        `json:"winnerID,omitempty"`
	EndReason     string        `json:"endReason,omitempty"`
	GameStartTime time.Time     `json:"gameStartTime,omitempty"`
	TurnDeadline  time.Time     `json:"turnDeadline,omitempty"`
	TurnTimeout   time.Duration `json:"turnTimeout,omitempty"`
	NoHumanChecks int           `json:"noHumanChecks,omitempty"`
}

func other(side Side) Side {
	if side == SideWhite {
		return SideBlack
	}
	return SideWhite
}

func cloneState(state *State) *State {
	if state == nil {
		return nil
	}
	copyState := *state
	copyState.PlayerOrder = append([]string(nil), state.PlayerOrder...)
	copyState.Resolution = append([]string(nil), state.Resolution...)
	copyState.Roster = make(map[string]*Player, len(state.Roster))
	for id, player := range state.Roster {
		if player == nil {
			continue
		}
		copyPlayer := *player
		copyState.Roster[id] = &copyPlayer
	}
	copyState.Rings = cloneSideMap(state.Rings)
	copyState.Markers = cloneSideMap(state.Markers)
	copyState.PlacedRings = cloneSideCount(state.PlacedRings)
	copyState.RingsRemoved = cloneSideCount(state.RingsRemoved)
	copyState.Log = make([]Action, len(state.Log))
	for index, action := range state.Log {
		copyState.Log[index] = cloneAction(action)
	}
	if state.LastAction != nil {
		action := cloneAction(*state.LastAction)
		copyState.LastAction = &action
	}
	return &copyState
}

func cloneSideMap(values map[string]Side) map[string]Side {
	result := make(map[string]Side, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneSideCount(values map[Side]int) map[Side]int {
	return map[Side]int{
		SideWhite: values[SideWhite],
		SideBlack: values[SideBlack],
	}
}

func cloneAction(action Action) Action {
	result := action
	if action.Payload != nil {
		result.Payload = make(map[string]any, len(action.Payload))
		for key, value := range action.Payload {
			switch typed := value.(type) {
			case []string:
				result.Payload[key] = append([]string(nil), typed...)
			default:
				result.Payload[key] = value
			}
		}
	}
	return result
}
