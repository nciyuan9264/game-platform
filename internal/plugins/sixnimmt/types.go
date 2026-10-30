package sixnimmt

import "time"

const (
	StatusMatch     = "match"
	StatusWaiting   = "waiting"
	StatusSelecting = "selecting"
	StatusChooseRow = "chooseRow"
	StatusRoundEnd  = "roundEnd"
	StatusEnd       = "end"
)

const (
	AutomationAI      = "ai"
	AutomationTimeout = "timeout"
)

type Player struct {
	UserID         string `json:"userID,omitempty"`
	PlayerID       string `json:"playerID"`
	DisplayName    string `json:"displayName,omitempty"`
	Online         bool   `json:"online"`
	Ready          bool   `json:"ready"`
	GameReady      bool   `json:"gameReady,omitempty"`
	AI             bool   `json:"ai"`
	SeatIndex      int    `json:"seatIndex"`
	Hand           []int  `json:"hand"`
	Taken          []int  `json:"taken"`
	RoundPenalty   int    `json:"roundPenalty"`
	TotalPenalty   int    `json:"totalPenalty"`
	LockedCard     *int   `json:"lockedCard,omitempty"`
	AutomationKind string `json:"automationKind,omitempty"`
}

type Selection struct {
	PlayerID       string `json:"playerID"`
	Card           int    `json:"card"`
	AutomationKind string `json:"automationKind,omitempty"`
}

type Placement struct {
	PlayerID       string `json:"playerID"`
	Card           int    `json:"card"`
	RowIndex       int    `json:"rowIndex"`
	Kind           string `json:"kind"`
	CollectedCards []int  `json:"collectedCards"`
	Penalty        int    `json:"penalty"`
	AutomationKind string `json:"automationKind,omitempty"`
}

type RowView struct {
	Index     int   `json:"index"`
	Cards     []int `json:"cards"`
	Bullheads int   `json:"bullheads"`
}

type TurnResolution struct {
	ResolutionID string      `json:"resolutionID"`
	RoundNo      int         `json:"roundNo"`
	TurnNo       int         `json:"turnNo"`
	RowsBefore   []RowView   `json:"rowsBefore"`
	Selections   []Selection `json:"selections"`
	Placements   []Placement `json:"placements"`
	RowsAfter    []RowView   `json:"rowsAfter"`
	Complete     bool        `json:"complete"`
}

type PendingChoice struct {
	PlayerID     string `json:"playerID"`
	Card         int    `json:"card"`
	RowPenalties []int  `json:"rowPenalties"`
}

type Action struct {
	ID             int    `json:"id"`
	RoundNo        int    `json:"roundNo"`
	TurnNo         int    `json:"turnNo"`
	Kind           string `json:"kind"`
	PlayerID       string `json:"playerID"`
	Card           *int   `json:"card,omitempty"`
	RowIndex       *int   `json:"rowIndex,omitempty"`
	Penalty        *int   `json:"penalty,omitempty"`
	AutomationKind string `json:"automationKind,omitempty"`
	Summary        string `json:"summary,omitempty"`
}

type RoundResult struct {
	RoundNo           int            `json:"roundNo"`
	RoundPenalties    map[string]int `json:"roundPenalties"`
	TotalPenalties    map[string]int `json:"totalPenalties"`
	ThresholdReached  bool           `json:"thresholdReached"`
	NextRoundDeadline time.Time      `json:"nextRoundDeadline,omitempty"`
}

type Result struct {
	WinnerIDs      []string       `json:"winnerIDs"`
	TotalPenalties map[string]int `json:"totalPenalties"`
	FinalRanks     map[string]int `json:"finalRanks"`
	RoundNo        int            `json:"roundNo"`
	Reason         string         `json:"reason"`
}

type State struct {
	RoomID                  string             `json:"roomID"`
	OwnerID                 string             `json:"ownerID"`
	Status                  string             `json:"status"`
	MaxPlayers              int                `json:"maxPlayers"`
	Visibility              string             `json:"visibility"`
	TurnTimeoutSeconds      int                `json:"turnTimeoutSeconds"`
	RandomSeed              uint64             `json:"randomSeed"`
	RandomCounter           uint64             `json:"randomCounter"`
	MatchRoundNo            int                `json:"matchRoundNo"`
	RoundNo                 int                `json:"roundNo"`
	TurnNo                  int                `json:"turnNo"`
	Roster                  map[string]*Player `json:"roster"`
	PlayerOrder             []string           `json:"playerOrder"`
	Rows                    [][]int            `json:"rows"`
	UnusedCards             []int              `json:"unusedCards"`
	Queue                   []Selection        `json:"queue"`
	QueueCursor             int                `json:"queueCursor"`
	Log                     []Action           `json:"log"`
	LastResolution          *TurnResolution    `json:"lastResolution,omitempty"`
	PendingChoice           *PendingChoice     `json:"pendingChoice,omitempty"`
	RoundResult             *RoundResult       `json:"roundResult,omitempty"`
	Result                  *Result            `json:"result,omitempty"`
	GameStartTime           time.Time          `json:"gameStartTime,omitempty"`
	TurnDeadline            time.Time          `json:"turnDeadline,omitempty"`
	TurnTimeout             time.Duration      `json:"turnTimeout,omitempty"`
	RoundTransitionDeadline time.Time          `json:"roundTransitionDeadline,omitempty"`
	NoHumanChecks           int                `json:"noHumanChecks,omitempty"`
}

func cloneState(value *State) *State {
	if value == nil {
		return nil
	}
	result := *value
	result.PlayerOrder = append([]string(nil), value.PlayerOrder...)
	result.Rows = cloneRows(value.Rows)
	result.UnusedCards = append([]int(nil), value.UnusedCards...)
	result.Queue = append([]Selection(nil), value.Queue...)
	result.Log = make([]Action, len(value.Log))
	for index, action := range value.Log {
		result.Log[index] = cloneAction(action)
	}
	result.Roster = make(map[string]*Player, len(value.Roster))
	for playerID, player := range value.Roster {
		if player == nil {
			continue
		}
		copyPlayer := *player
		copyPlayer.Hand = append([]int(nil), player.Hand...)
		copyPlayer.Taken = append([]int(nil), player.Taken...)
		if player.LockedCard != nil {
			card := *player.LockedCard
			copyPlayer.LockedCard = &card
		}
		result.Roster[playerID] = &copyPlayer
	}
	result.LastResolution = cloneResolution(value.LastResolution)
	if value.PendingChoice != nil {
		choice := *value.PendingChoice
		choice.RowPenalties = append([]int(nil), value.PendingChoice.RowPenalties...)
		result.PendingChoice = &choice
	}
	result.RoundResult = cloneRoundResult(value.RoundResult)
	result.Result = cloneResult(value.Result)
	return &result
}

func cloneRows(rows [][]int) [][]int {
	result := make([][]int, len(rows))
	for index := range rows {
		result[index] = append([]int(nil), rows[index]...)
	}
	return result
}

func cloneAction(value Action) Action {
	result := value
	if value.Card != nil {
		card := *value.Card
		result.Card = &card
	}
	if value.RowIndex != nil {
		row := *value.RowIndex
		result.RowIndex = &row
	}
	if value.Penalty != nil {
		penalty := *value.Penalty
		result.Penalty = &penalty
	}
	return result
}

func cloneResolution(value *TurnResolution) *TurnResolution {
	if value == nil {
		return nil
	}
	result := *value
	result.RowsBefore = cloneRowViews(value.RowsBefore)
	result.Selections = append([]Selection(nil), value.Selections...)
	result.Placements = make([]Placement, len(value.Placements))
	for index, placement := range value.Placements {
		result.Placements[index] = placement
		result.Placements[index].CollectedCards = append(
			[]int(nil),
			placement.CollectedCards...,
		)
	}
	result.RowsAfter = cloneRowViews(value.RowsAfter)
	return &result
}

func cloneRowViews(values []RowView) []RowView {
	result := make([]RowView, len(values))
	for index, row := range values {
		result[index] = row
		result[index].Cards = append([]int(nil), row.Cards...)
	}
	return result
}

func cloneRoundResult(value *RoundResult) *RoundResult {
	if value == nil {
		return nil
	}
	result := *value
	result.RoundPenalties = cloneIntMap(value.RoundPenalties)
	result.TotalPenalties = cloneIntMap(value.TotalPenalties)
	return &result
}

func cloneResult(value *Result) *Result {
	if value == nil {
		return nil
	}
	result := *value
	result.WinnerIDs = append([]string(nil), value.WinnerIDs...)
	result.TotalPenalties = cloneIntMap(value.TotalPenalties)
	result.FinalRanks = cloneIntMap(value.FinalRanks)
	return &result
}

func cloneIntMap(value map[string]int) map[string]int {
	result := make(map[string]int, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
