package loveletter

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/nciyuan9264/game-platform/internal/game"
)

const (
	StatusMatch      = "match"
	StatusWaiting    = "waiting"
	StatusDraw       = "draw"
	StatusPlay       = "play"
	StatusChancellor = "chancellor"
	StatusRoundEnd   = "roundEnd"
	StatusEnd        = "end"
)

type Card struct {
	CardID string `json:"cardID"`
	Rank   int    `json:"rank"`
}

type KnownCard struct {
	Rank  int  `json:"rank"`
	Valid bool `json:"valid"`
}

type PrivateEffect struct {
	EffectID     string `json:"effectID"`
	Kind         string `json:"kind"`
	TargetPlayer string `json:"targetPlayerID,omitempty"`
	TargetRank   *int   `json:"targetRank,omitempty"`
	ActorRank    *int   `json:"actorRank,omitempty"`
	Summary      string `json:"summary,omitempty"`
}

type Player struct {
	UserID      string               `json:"userID,omitempty"`
	PlayerID    string               `json:"playerID"`
	DisplayName string               `json:"displayName,omitempty"`
	Online      bool                 `json:"online"`
	Ready       bool                 `json:"ready"`
	GameReady   bool                 `json:"gameReady,omitempty"`
	AI          bool                 `json:"ai"`
	SeatIndex   int                  `json:"seatIndex"`
	Hand        []Card               `json:"hand"`
	Discards    []Card               `json:"discards"`
	Alive       bool                 `json:"alive"`
	Protected   bool                 `json:"protected"`
	SpyUsed     bool                 `json:"spyUsed"`
	Favors      int                  `json:"favors"`
	Private     *PrivateEffect       `json:"privateEffect,omitempty"`
	KnownCards  map[string]KnownCard `json:"knownCards,omitempty"`
}

type Action struct {
	ID           int    `json:"id"`
	RoundNo      int    `json:"roundNo"`
	Kind         string `json:"kind"`
	PlayerID     string `json:"playerID"`
	CardRank     *int   `json:"cardRank,omitempty"`
	TargetPlayer string `json:"targetPlayerID,omitempty"`
	GuessRank    *int   `json:"guessRank,omitempty"`
	Success      *bool  `json:"success,omitempty"`
	Automatic    bool   `json:"automatic"`
	Private      bool   `json:"private,omitempty"`
	Summary      string `json:"summary,omitempty"`
}

type RoundResult struct {
	WinnerIDs      []string        `json:"winnerIDs"`
	SpyPlayerID    string          `json:"spyPlayerID,omitempty"`
	Reason         string          `json:"reason"`
	FavorsBefore   map[string]int  `json:"favorsBefore"`
	FavorsAfter    map[string]int  `json:"favorsAfter"`
	RevealedHands  map[string]Card `json:"revealedHands"`
	MatchWinnerIDs []string        `json:"matchWinnerIDs"`
}

type Result struct {
	WinnerIDs []string       `json:"winnerIDs"`
	Favors    map[string]int `json:"favors"`
	FinalRank map[string]int `json:"finalRanks"`
	RoundNo   int            `json:"roundNo"`
	Reason    string         `json:"reason"`
}

type State struct {
	RoomID             string             `json:"roomID"`
	OwnerID            string             `json:"ownerID"`
	Status             string             `json:"status"`
	MaxPlayers         int                `json:"maxPlayers"`
	Visibility         string             `json:"visibility"`
	DisplayName        string             `json:"displayName"`
	TurnTimeoutSeconds int                `json:"turnTimeoutSeconds"`
	RandomSeed         uint64             `json:"randomSeed"`
	RandomCounter      uint64             `json:"randomCounter"`
	MatchRoundNo       int                `json:"matchRoundNo"`
	RoundNo            int                `json:"roundNo"`
	Goal               int                `json:"goal"`
	Roster             map[string]*Player `json:"roster"`
	PlayerOrder        []string           `json:"playerOrder"`

	CurrentPlayer           string        `json:"currentPlayer,omitempty"`
	Deck                    []Card        `json:"deck"`
	Reserve                 *Card         `json:"reserve,omitempty"`
	Burn                    []Card        `json:"burn"`
	Log                     []Action      `json:"log"`
	LastAction              *Action       `json:"lastAction,omitempty"`
	RoundResult             *RoundResult  `json:"roundResult,omitempty"`
	Result                  *Result       `json:"result,omitempty"`
	GameStartTime           time.Time     `json:"gameStartTime,omitempty"`
	TurnDeadline            time.Time     `json:"turnDeadline,omitempty"`
	TurnTimeout             time.Duration `json:"turnTimeout,omitempty"`
	RoundTransitionDeadline time.Time     `json:"roundTransitionDeadline,omitempty"`
	NoHumanChecks           int           `json:"noHumanChecks,omitempty"`
}

func cloneCardPointer(value *Card) *Card {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func clonePrivateEffect(value *PrivateEffect) *PrivateEffect {
	if value == nil {
		return nil
	}
	result := *value
	if value.TargetRank != nil {
		rank := *value.TargetRank
		result.TargetRank = &rank
	}
	if value.ActorRank != nil {
		rank := *value.ActorRank
		result.ActorRank = &rank
	}
	return &result
}

func cloneAction(value Action) Action {
	result := value
	if value.CardRank != nil {
		rank := *value.CardRank
		result.CardRank = &rank
	}
	if value.GuessRank != nil {
		rank := *value.GuessRank
		result.GuessRank = &rank
	}
	if value.Success != nil {
		success := *value.Success
		result.Success = &success
	}
	return result
}

func cloneRoundResult(value *RoundResult) *RoundResult {
	if value == nil {
		return nil
	}
	result := *value
	result.WinnerIDs = append([]string(nil), value.WinnerIDs...)
	result.MatchWinnerIDs = append(
		[]string(nil),
		value.MatchWinnerIDs...,
	)
	result.FavorsBefore = cloneIntMap(value.FavorsBefore)
	result.FavorsAfter = cloneIntMap(value.FavorsAfter)
	result.RevealedHands = make(map[string]Card, len(value.RevealedHands))
	for playerID, card := range value.RevealedHands {
		result.RevealedHands[playerID] = card
	}
	return &result
}

func cloneResult(value *Result) *Result {
	if value == nil {
		return nil
	}
	result := *value
	result.WinnerIDs = append([]string(nil), value.WinnerIDs...)
	result.Favors = cloneIntMap(value.Favors)
	result.FinalRank = cloneIntMap(value.FinalRank)
	return &result
}

func cloneIntMap(value map[string]int) map[string]int {
	result := make(map[string]int, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func cloneState(value *State) *State {
	if value == nil {
		return nil
	}
	result := *value
	result.PlayerOrder = append([]string(nil), value.PlayerOrder...)
	result.Roster = make(map[string]*Player, len(value.Roster))
	for playerID, player := range value.Roster {
		if player == nil {
			continue
		}
		copyPlayer := *player
		copyPlayer.Hand = append([]Card(nil), player.Hand...)
		copyPlayer.Discards = append([]Card(nil), player.Discards...)
		copyPlayer.Private = clonePrivateEffect(player.Private)
		copyPlayer.KnownCards = make(
			map[string]KnownCard,
			len(player.KnownCards),
		)
		for targetID, known := range player.KnownCards {
			copyPlayer.KnownCards[targetID] = known
		}
		result.Roster[playerID] = &copyPlayer
	}
	result.Deck = append([]Card(nil), value.Deck...)
	result.Reserve = cloneCardPointer(value.Reserve)
	result.Burn = append([]Card(nil), value.Burn...)
	result.Log = make([]Action, len(value.Log))
	for index, action := range value.Log {
		result.Log[index] = cloneAction(action)
	}
	if value.LastAction != nil {
		action := cloneAction(*value.LastAction)
		result.LastAction = &action
	}
	result.RoundResult = cloneRoundResult(value.RoundResult)
	result.Result = cloneResult(value.Result)
	return &result
}

func (p *Plugin) IsRecordableCommand(commandType string) bool {
	switch commandType {
	case CommandDrawCard, CommandPlayCard, CommandSettleChancellor,
		CommandNextRound, CommandTimeout, CommandRoundTransition:
		return true
	default:
		return false
	}
}

func (p *Plugin) ProjectHistory(
	_ context.Context,
	state game.State,
) (game.HistoryProjection, error) {
	typed, err := asState(state)
	if err != nil {
		return game.HistoryProjection{}, err
	}
	started := !typed.GameStartTime.IsZero() &&
		typed.Status != StatusMatch &&
		typed.Status != StatusWaiting
	if !started {
		return game.HistoryProjection{}, nil
	}
	stateBlob, err := json.Marshal(typed)
	if err != nil {
		return game.HistoryProjection{}, err
	}
	finished := typed.Status == StatusEnd
	players := make([]game.HistoryPlayer, 0, len(typed.PlayerOrder))
	for _, playerID := range typed.PlayerOrder {
		player := typed.Roster[playerID]
		if player == nil {
			continue
		}
		score := player.Favors
		var rank *int
		if finished && typed.Result != nil {
			value := typed.Result.FinalRank[playerID]
			rank = &value
		}
		players = append(players, game.HistoryPlayer{
			UserID:      player.UserID,
			PlayerID:    playerID,
			DisplayName: player.DisplayName,
			SeatIndex:   player.SeatIndex,
			IsAI:        player.AI,
			FinalScore:  &score,
			FinalRank:   rank,
			IsWinner: finished && typed.Result != nil &&
				containsString(typed.Result.WinnerIDs, playerID),
		})
	}
	winnerID := ""
	if finished && typed.Result != nil {
		winnerID = firstWinnerBySeat(typed)
	}
	return game.HistoryProjection{
		Started:             true,
		Finished:            finished,
		AbandonedEligible:   typed.RoundNo > 1 || hasPlayedCard(typed),
		StartedAt:           typed.GameStartTime,
		MaxPlayers:          typed.MaxPlayers,
		ReplaySchemaVersion: ReplaySchemaVersion,
		ReplayFrame:         replayFrame(typed),
		InitialState:        stateBlob,
		StateBlob:           stateBlob,
		FinalResult:         cloneResult(typed.Result),
		WinnerPlayerID:      winnerID,
		RoomDisplayName:     defaultRoomName,
		Players:             players,
	}, nil
}

func firstWinnerBySeat(state *State) string {
	if state.Result == nil {
		return ""
	}
	winners := append([]string(nil), state.Result.WinnerIDs...)
	sort.SliceStable(winners, func(i, j int) bool {
		return state.Roster[winners[i]].SeatIndex <
			state.Roster[winners[j]].SeatIndex
	})
	return firstString(winners)
}

func hasPlayedCard(state *State) bool {
	for _, action := range state.Log {
		if action.Kind == "play" {
			return true
		}
	}
	return false
}

func (p *Plugin) DecodeReplayState(
	_ context.Context,
	initialState,
	stateSnapshot,
	stateBlob []byte,
	players []game.HistoryPlayer,
) (game.State, error) {
	data := stateBlob
	if len(data) == 0 {
		data = stateSnapshot
	}
	if len(data) == 0 {
		data = initialState
	}
	if len(data) == 0 {
		return nil, errors.New("loveletter replay state is unavailable")
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	ensureState(&state)
	if len(state.PlayerOrder) == 0 {
		for _, historyPlayer := range players {
			state.PlayerOrder = append(
				state.PlayerOrder,
				historyPlayer.PlayerID,
			)
			state.Roster[historyPlayer.PlayerID] = &Player{
				UserID:      historyPlayer.UserID,
				PlayerID:    historyPlayer.PlayerID,
				DisplayName: historyPlayer.DisplayName,
				SeatIndex:   historyPlayer.SeatIndex,
				AI:          historyPlayer.IsAI,
				Ready:       true,
				GameReady:   true,
				Hand:        []Card{},
				Discards:    []Card{},
				KnownCards:  map[string]KnownCard{},
			}
		}
		sortPlayerOrder(&state)
	}
	if err := validateState(&state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (p *Plugin) BuildReplaySnapshot(
	_ context.Context,
	state game.State,
	seq,
	total int,
	commandType,
	playerID string,
	payload any,
) (any, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	frame := replayFrame(typed)
	frame["seq"] = seq
	frame["totalEvents"] = total
	if commandType != "" {
		payloadJSON, _ := json.Marshal(payload)
		frame["currentEvent"] = map[string]any{
			"seq":         seq,
			"playerID":    playerID,
			"cmdType":     commandType,
			"payloadJSON": string(payloadJSON),
		}
	}
	return frame, nil
}

func replayFrame(state *State) map[string]any {
	privateData := make(map[string]any, len(state.PlayerOrder))
	for _, playerID := range state.PlayerOrder {
		privateData[playerID] = privateDataView(state, playerID)
	}
	return map[string]any{
		"roomData": roomDataView(
			state,
			"",
			playerViews(state, nil),
		),
		"playersData": publicPlayerData(state),
		"privateData": privateData,
		"deck":        append([]Card(nil), state.Deck...),
		"reserve":     cloneCardPointer(state.Reserve),
		"result":      cloneResult(state.Result),
	}
}
