package forsale

import "time"

const (
	StatusMatch      = "match"
	StatusWaiting    = "waiting"
	StatusAuction    = "auction"
	StatusBuyResult  = "buyResult"
	StatusBridge     = "bridge"
	StatusSellSelect = "sellSelect"
	StatusSellLocked = "sellLocked"
	StatusSellReveal = "sellReveal"
	StatusEnd        = "end"
)

const (
	SourceManual  = "manual"
	SourceAI      = "ai"
	SourceTimeout = "timeout"
	SourceSystem  = "system"
)

type Player struct {
	UserID         string    `json:"userID,omitempty"`
	PlayerID       string    `json:"playerID"`
	DisplayName    string    `json:"displayName,omitempty"`
	Online         bool      `json:"online"`
	Ready          bool      `json:"ready"`
	GameReady      bool      `json:"gameReady,omitempty"`
	AI             bool      `json:"ai"`
	SeatIndex      int       `json:"seatIndex"`
	Coins          int       `json:"coins"`
	Bid            int       `json:"bid"`
	Passed         bool      `json:"passed"`
	Properties     []int     `json:"properties"`
	Cheques        []int     `json:"cheques"`
	LockedProperty int       `json:"lockedProperty,omitempty"`
	WonProperty    int       `json:"wonProperty,omitempty"`
	RoundCost      int       `json:"roundCost,omitempty"`
	ActionDeadline time.Time `json:"actionDeadline,omitempty"`
}

type Trade struct {
	PlayerID string `json:"playerID"`
	Property int    `json:"property"`
	Amount   int    `json:"amount"`
}

type RoundRecord struct {
	Round  int     `json:"round"`
	Trades []Trade `json:"trades"`
}

type Action struct {
	ID       int    `json:"id"`
	Round    int    `json:"round"`
	Phase    string `json:"phase"`
	Kind     string `json:"kind"`
	PlayerID string `json:"playerID"`
	Source   string `json:"source"`
	Amount   *int   `json:"amount,omitempty"`
	Property *int   `json:"property,omitempty"`
	Cheque   *int   `json:"cheque,omitempty"`
	Paid     *int   `json:"paid,omitempty"`
}

type PlayerResult struct {
	ChequeTotal int  `json:"chequeTotal"`
	Coins       int  `json:"coins"`
	Total       int  `json:"total"`
	Rank        int  `json:"rank"`
	Winner      bool `json:"winner"`
}

type State struct {
	RoomID        string             `json:"roomID"`
	OwnerID       string             `json:"ownerID"`
	Status        string             `json:"status"`
	MaxPlayers    int                `json:"maxPlayers"`
	Visibility    string             `json:"visibility"`
	RandomSeed    uint64             `json:"randomSeed"`
	RandomCounter uint64             `json:"randomCounter"`
	MatchRoundNo  int                `json:"matchRoundNo"`
	Roster        map[string]*Player `json:"roster"`
	PlayerOrder   []string           `json:"playerOrder"`

	Round              int                     `json:"round"`
	TotalRounds        int                     `json:"totalRounds"`
	PropertyDeck       []int                   `json:"propertyDeck"`
	ChequeDeck         []int                   `json:"chequeDeck"`
	RemovedProperties  []int                   `json:"removedProperties"`
	RemovedCheques     []int                   `json:"removedCheques"`
	PropertyMarket     []int                   `json:"propertyMarket"`
	ChequeMarket       []int                   `json:"chequeMarket"`
	HighestBid         int                     `json:"highestBid"`
	CurrentPlayer      string                  `json:"currentPlayer,omitempty"`
	StarterPlayer      string                  `json:"starterPlayer,omitempty"`
	Purchases          []RoundRecord           `json:"purchases"`
	Sales              []RoundRecord           `json:"sales"`
	ActionLog          []Action                `json:"actionLog"`
	LastAction         *Action                 `json:"lastAction,omitempty"`
	ActionCounter      int                     `json:"actionCounter"`
	Result             map[string]PlayerResult `json:"result"`
	EndReason          string                  `json:"endReason,omitempty"`
	GameStartTime      time.Time               `json:"gameStartTime,omitempty"`
	TurnDeadline       time.Time               `json:"turnDeadline,omitempty"`
	TurnTimeout        time.Duration           `json:"turnTimeout,omitempty"`
	TransitionDeadline time.Time               `json:"transitionDeadline,omitempty"`
	TransitionTimeout  time.Duration           `json:"transitionTimeout,omitempty"`
	NoHumanChecks      int                     `json:"noHumanChecks,omitempty"`
}

func cloneState(state *State) *State {
	if state == nil {
		return nil
	}
	result := *state
	result.PlayerOrder = append([]string(nil), state.PlayerOrder...)
	result.PropertyDeck = append([]int(nil), state.PropertyDeck...)
	result.ChequeDeck = append([]int(nil), state.ChequeDeck...)
	result.RemovedProperties = append([]int(nil), state.RemovedProperties...)
	result.RemovedCheques = append([]int(nil), state.RemovedCheques...)
	result.PropertyMarket = append([]int(nil), state.PropertyMarket...)
	result.ChequeMarket = append([]int(nil), state.ChequeMarket...)
	result.Roster = make(map[string]*Player, len(state.Roster))
	for playerID, player := range state.Roster {
		if player == nil {
			continue
		}
		copyPlayer := *player
		copyPlayer.Properties = append([]int(nil), player.Properties...)
		copyPlayer.Cheques = append([]int(nil), player.Cheques...)
		result.Roster[playerID] = &copyPlayer
	}
	result.Purchases = cloneRoundRecords(state.Purchases)
	result.Sales = cloneRoundRecords(state.Sales)
	result.ActionLog = append([]Action(nil), state.ActionLog...)
	if state.LastAction != nil {
		action := *state.LastAction
		result.LastAction = &action
	}
	result.Result = make(map[string]PlayerResult, len(state.Result))
	for playerID, value := range state.Result {
		result.Result[playerID] = value
	}
	return &result
}

func cloneRoundRecords(records []RoundRecord) []RoundRecord {
	result := make([]RoundRecord, len(records))
	for index, record := range records {
		result[index] = record
		result[index].Trades = append([]Trade(nil), record.Trades...)
	}
	return result
}
