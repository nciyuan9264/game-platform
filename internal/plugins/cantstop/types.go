package cantstop

import "time"

const (
	StatusMatch   = "match"
	StatusWaiting = "waiting"
	StatusRoll    = "roll"
	StatusChoose  = "choose"
	StatusDecide  = "decide"
	StatusBust    = "bust"
	StatusBanked  = "banked"
	StatusEnd     = "end"
)

const (
	SourceManual  = "manual"
	SourceAI      = "ai"
	SourceTimeout = "timeout"
	SourceSystem  = "system"
)

type Player struct {
	UserID      string      `json:"userID,omitempty"`
	PlayerID    string      `json:"playerID"`
	DisplayName string      `json:"displayName,omitempty"`
	Online      bool        `json:"online"`
	Ready       bool        `json:"ready"`
	GameReady   bool        `json:"gameReady,omitempty"`
	AI          bool        `json:"ai"`
	SeatIndex   int         `json:"seatIndex"`
	Saved       map[int]int `json:"saved"`
	Busts       int         `json:"busts"`
}

type PairingOption struct {
	ActionID        string      `json:"actionID"`
	PairingIndex    int         `json:"pairingIndex"`
	VariantIndex    int         `json:"variantIndex"`
	Sums            []int       `json:"sums"`
	DiceIndexes     []int       `json:"diceIndexes"`
	Advances        map[int]int `json:"advances"`
	RunnerPositions map[int]int `json:"runnerPositions"`
	StepCount       int         `json:"stepCount"`
}

type Action struct {
	ID         int    `json:"id"`
	TurnNumber int    `json:"turnNumber"`
	RollNumber int    `json:"rollNumber"`
	Kind       string `json:"kind"`
	PlayerID   string `json:"playerID"`
	Dice       []int  `json:"dice,omitempty"`
	ActionID   string `json:"actionID,omitempty"`
	Columns    []int  `json:"columns"`
	Gain       int    `json:"gain"`
	Source     string `json:"source"`
	Summary    string `json:"summary,omitempty"`
}

type Result struct {
	WinnerID       string         `json:"winnerID"`
	ClaimedColumns []int          `json:"claimedColumns"`
	TurnCount      int            `json:"turnCount"`
	RollCount      int            `json:"rollCount"`
	BustCounts     map[string]int `json:"bustCounts"`
	Reason         string         `json:"reason"`
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
	Roster             map[string]*Player `json:"roster"`
	PlayerOrder        []string           `json:"playerOrder"`

	CurrentPlayer      string          `json:"currentPlayer,omitempty"`
	NextPlayer         string          `json:"nextPlayer,omitempty"`
	FirstPlayer        string          `json:"firstPlayer,omitempty"`
	TurnNumber         int             `json:"turnNumber"`
	RollNumber         int             `json:"rollNumber"`
	TotalRolls         int             `json:"totalRolls"`
	Dice               []int           `json:"dice"`
	PairingOptions     []PairingOption `json:"pairingOptions"`
	Runners            map[int]int     `json:"runners"`
	ClaimedBy          map[int]string  `json:"claimedBy"`
	LostGain           int             `json:"lostGain"`
	Log                []Action        `json:"log"`
	LastAction         *Action         `json:"lastAction,omitempty"`
	Result             *Result         `json:"result,omitempty"`
	GameStartTime      time.Time       `json:"gameStartTime,omitempty"`
	TurnDeadline       time.Time       `json:"turnDeadline,omitempty"`
	TurnTimeout        time.Duration   `json:"turnTimeout,omitempty"`
	TransitionDeadline time.Time       `json:"transitionDeadline,omitempty"`
	TransitionTimeout  time.Duration   `json:"transitionTimeout,omitempty"`
	NoHumanChecks      int             `json:"noHumanChecks,omitempty"`
}

func cloneState(state *State) *State {
	if state == nil {
		return nil
	}
	result := *state
	result.PlayerOrder = append([]string{}, state.PlayerOrder...)
	result.Dice = append([]int{}, state.Dice...)
	result.Runners = cloneProgress(state.Runners)
	result.ClaimedBy = make(map[int]string, len(state.ClaimedBy))
	for column, playerID := range state.ClaimedBy {
		result.ClaimedBy[column] = playerID
	}
	result.Roster = make(map[string]*Player, len(state.Roster))
	for playerID, player := range state.Roster {
		if player == nil {
			continue
		}
		copyPlayer := *player
		copyPlayer.Saved = cloneProgress(player.Saved)
		result.Roster[playerID] = &copyPlayer
	}
	result.PairingOptions = make([]PairingOption, len(state.PairingOptions))
	for index, option := range state.PairingOptions {
		result.PairingOptions[index] = clonePairingOption(option)
	}
	result.Log = make([]Action, len(state.Log))
	for index, action := range state.Log {
		result.Log[index] = cloneAction(action)
	}
	if state.LastAction != nil {
		action := cloneAction(*state.LastAction)
		result.LastAction = &action
	}
	if state.Result != nil {
		value := *state.Result
		value.ClaimedColumns = append([]int(nil), state.Result.ClaimedColumns...)
		value.BustCounts = cloneStringIntMap(state.Result.BustCounts)
		result.Result = &value
	}
	return &result
}

func clonePairingOption(value PairingOption) PairingOption {
	result := value
	result.Sums = append([]int(nil), value.Sums...)
	result.DiceIndexes = append([]int(nil), value.DiceIndexes...)
	result.Advances = cloneProgress(value.Advances)
	result.RunnerPositions = cloneProgress(value.RunnerPositions)
	return result
}

func cloneAction(value Action) Action {
	result := value
	result.Dice = append([]int(nil), value.Dice...)
	result.Columns = append([]int(nil), value.Columns...)
	return result
}

func cloneProgress(value map[int]int) map[int]int {
	result := make(map[int]int, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func cloneStringIntMap(value map[string]int) map[string]int {
	result := make(map[string]int, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
