package fugitive

import "time"

type Role string

const (
	RoleFugitive Role = "fugitive"
	RoleMarshal  Role = "marshal"
)

const (
	StatusMatch          = "match"
	StatusWaiting        = "waiting"
	StatusOpening        = "opening"
	StatusDraw           = "draw"
	StatusFugitiveAction = "fugitiveAction"
	StatusMarshalSearch  = "marshalSearch"
	StatusManhunt        = "manhunt"
	StatusEnd            = "end"
)

type Card struct {
	Number int `json:"number"`
	Sprint int `json:"sprint"`
}

type Hideout struct {
	Card     Card   `json:"card"`
	Revealed bool   `json:"revealed"`
	Sprints  []Card `json:"sprints"`
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
	Role        Role   `json:"role,omitempty"`
	Hand        []Card `json:"hand"`
}

type Action struct {
	ID              int    `json:"id"`
	RoundNo         int    `json:"roundNo"`
	Kind            string `json:"kind"`
	PlayerID        string `json:"playerID"`
	Role            Role   `json:"role"`
	DeckIndex       *int   `json:"deckIndex,omitempty"`
	Numbers         []int  `json:"numbers,omitempty"`
	Destination     *int   `json:"destination,omitempty"`
	SprintCount     int    `json:"sprintCount"`
	Success         *bool  `json:"success,omitempty"`
	Automatic       bool   `json:"automatic"`
	PrivatePlayerID string `json:"privatePlayerID,omitempty"`
	Private         bool   `json:"private,omitempty"`
}

type State struct {
	RoomID             string             `json:"roomID"`
	OwnerID            string             `json:"ownerID"`
	Status             string             `json:"status"`
	MaxPlayers         int                `json:"maxPlayers"`
	Visibility         string             `json:"visibility"`
	TurnTimeoutSeconds int                `json:"turnTimeoutSeconds"`
	RandomSeed         uint64             `json:"randomSeed"`
	RandomCounter      uint64             `json:"randomCounter"`
	MatchRoundNo       int                `json:"matchRoundNo"`
	RoundNo            int                `json:"roundNo"`
	Roster             map[string]*Player `json:"roster"`
	PlayerOrder        []string           `json:"playerOrder"`
	OwnerRole          Role               `json:"ownerRole"`
	FugitivePlayerID   string             `json:"fugitivePlayerID,omitempty"`
	MarshalPlayerID    string             `json:"marshalPlayerID,omitempty"`
	CurrentPlayer      string             `json:"currentPlayer,omitempty"`
	DrawsRemaining     int                `json:"drawsRemaining"`
	OpeningHideouts    int                `json:"openingHideouts"`
	Decks              [][]Card           `json:"decks"`
	Route              []Hideout          `json:"route"`
	Log                []Action           `json:"log"`
	LastAction         *Action            `json:"lastAction,omitempty"`
	Searches           int                `json:"searches"`
	WinnerID           string             `json:"winnerID,omitempty"`
	WinnerRole         Role               `json:"winnerRole,omitempty"`
	EndReason          string             `json:"endReason,omitempty"`
	GameStartTime      time.Time          `json:"gameStartTime,omitempty"`
	TurnStartedAt      time.Time          `json:"turnStartedAt,omitempty"`
	TurnDeadline       time.Time          `json:"turnDeadline,omitempty"`
	TurnTimeout        time.Duration      `json:"turnTimeout,omitempty"`
	NoHumanChecks      int                `json:"noHumanChecks,omitempty"`
}

func otherRole(role Role) Role {
	if role == RoleMarshal {
		return RoleFugitive
	}
	return RoleMarshal
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
		copyPlayer.Hand = append([]Card(nil), player.Hand...)
		result.Roster[playerID] = &copyPlayer
	}
	result.Decks = make([][]Card, len(state.Decks))
	for index := range state.Decks {
		result.Decks[index] = append([]Card(nil), state.Decks[index]...)
	}
	result.Route = make([]Hideout, len(state.Route))
	for index, hideout := range state.Route {
		result.Route[index] = hideout
		result.Route[index].Sprints = append([]Card(nil), hideout.Sprints...)
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
	result.Numbers = append([]int(nil), action.Numbers...)
	if action.DeckIndex != nil {
		value := *action.DeckIndex
		result.DeckIndex = &value
	}
	if action.Destination != nil {
		value := *action.Destination
		result.Destination = &value
	}
	if action.Success != nil {
		value := *action.Success
		result.Success = &value
	}
	return result
}
