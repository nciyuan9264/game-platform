package warchest

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Side string

const (
	SideOak    Side = "oak"
	SideFalcon Side = "falcon"
)

type UnitType string

const (
	UnitSword     UnitType = "sword"
	UnitPike      UnitType = "pike"
	UnitCrossbow  UnitType = "crossbow"
	UnitLight     UnitType = "light"
	UnitArcher    UnitType = "archer"
	UnitCavalry   UnitType = "cavalry"
	UnitLancer    UnitType = "lancer"
	UnitScout     UnitType = "scout"
	UnitBerserker UnitType = "berserker"
	UnitEnsign    UnitType = "ensign"
	UnitFootman   UnitType = "footman"
	UnitKnight    UnitType = "knight"
	UnitMarshall  UnitType = "marshall"
	UnitMercenary UnitType = "mercenary"
	UnitGuard     UnitType = "guard"
	UnitPriest    UnitType = "priest"
	UnitRoyal     UnitType = "royal"
)

type SetupMode string

const (
	SetupRecommended SetupMode = "recommended"
	SetupRandom      SetupMode = "random"
	SetupDraft       SetupMode = "draft"
)

const (
	StatusMatch   = "match"
	StatusWaiting = "waiting"
	StatusPlaying = "playing"
	StatusPending = "pending"
	StatusEnd     = "end"
)

type ActionType string

const (
	ActionDeploy     ActionType = "deploy"
	ActionBolster    ActionType = "bolster"
	ActionMove       ActionType = "move"
	ActionAttack     ActionType = "attack"
	ActionControl    ActionType = "control"
	ActionTactic     ActionType = "tactic"
	ActionRecruit    ActionType = "recruit"
	ActionInitiative ActionType = "initiative"
	ActionPass       ActionType = "pass"
	ActionFinish     ActionType = "finish"
	ActionDefend     ActionType = "defend"
)

type Player struct {
	UserID      string     `json:"userID,omitempty"`
	PlayerID    string     `json:"playerID"`
	DisplayName string     `json:"displayName,omitempty"`
	Online      bool       `json:"online"`
	Ready       bool       `json:"ready"`
	GameReady   bool       `json:"gameReady,omitempty"`
	AI          bool       `json:"ai"`
	SeatIndex   int        `json:"seatIndex"`
	Side        Side       `json:"side,omitempty"`
	Roster      []UnitType `json:"roster"`
	Bag         []string   `json:"bag"`
	Hand        []string   `json:"hand"`
	Discard     []Discard  `json:"discard"`
	Supply      UnitCoins  `json:"supply"`
	Lost        UnitCoins  `json:"lost"`
}

type UnitCoins map[UnitType][]string

type Discard struct {
	CoinID string `json:"coinID"`
	FaceUp bool   `json:"faceUp"`
}

type UnitStack struct {
	UnitID   string   `json:"unitID"`
	UnitType UnitType `json:"unitType"`
	Side     Side     `json:"side"`
	Cell     string   `json:"cell"`
	CoinIDs  []string `json:"coinIDs"`
}

type DraftState struct {
	Pool               []UnitType            `json:"pool"`
	PickedUnits        []UnitType            `json:"pickedUnits"`
	Rosters            map[string][]UnitType `json:"rosters"`
	FirstPickerID      string                `json:"firstPickerID"`
	CurrentPickerID    string                `json:"currentPickerID,omitempty"`
	CompletedPickCount int                   `json:"completedPickCount"`
	Complete           bool                  `json:"complete"`
	TurnDeadline       time.Time             `json:"turnDeadline,omitempty"`
}

type LegalAction struct {
	ActionID     string     `json:"actionID"`
	Action       ActionType `json:"action"`
	CoinID       string     `json:"coinID,omitempty"`
	UnitType     UnitType   `json:"unitType,omitempty"`
	ActorUnitID  string     `json:"actorUnitID,omitempty"`
	SourceUnitID string     `json:"sourceUnitID,omitempty"`
	From         string     `json:"from,omitempty"`
	To           string     `json:"to,omitempty"`
	TargetUnitID string     `json:"targetUnitID,omitempty"`
	RecruitUnit  UnitType   `json:"recruitUnit,omitempty"`
	After        string     `json:"after,omitempty"`
	Defense      string     `json:"defense,omitempty"`
	Maneuver     ActionType `json:"maneuver,omitempty"`
	Path         []string   `json:"path,omitempty"`
	Free         bool       `json:"free,omitempty"`
}

type PendingDecision struct {
	Kind             string           `json:"kind"`
	PlayerID         string           `json:"playerID"`
	Side             Side             `json:"side"`
	ActorUnitID      string           `json:"actorUnitID,omitempty"`
	SourceUnitID     string           `json:"sourceUnitID,omitempty"`
	TargetUnitID     string           `json:"targetUnitID,omitempty"`
	CoinID           string           `json:"coinID,omitempty"`
	UnitType         UnitType         `json:"unitType,omitempty"`
	RemainingUnitIDs []string         `json:"remainingUnitIDs,omitempty"`
	Description      string           `json:"description,omitempty"`
	Attack           *LegalAction     `json:"attack,omitempty"`
	Resume           *PendingDecision `json:"resume,omitempty"`
}

type ActionEntry struct {
	ID             int        `json:"id"`
	RoundNo        int        `json:"roundNo"`
	PlayerID       string     `json:"playerID"`
	Side           Side       `json:"side"`
	Action         ActionType `json:"action"`
	CoinID         string     `json:"coinID,omitempty"`
	UnitType       UnitType   `json:"unitType,omitempty"`
	ActorUnitID    string     `json:"actorUnitID,omitempty"`
	SourceUnitID   string     `json:"sourceUnitID,omitempty"`
	From           string     `json:"from,omitempty"`
	To             string     `json:"to,omitempty"`
	TargetUnitID   string     `json:"targetUnitID,omitempty"`
	TargetUnitType UnitType   `json:"targetUnitType,omitempty"`
	RecruitUnit    UnitType   `json:"recruitUnit,omitempty"`
	After          string     `json:"after,omitempty"`
	Defense        string     `json:"defense,omitempty"`
	Maneuver       ActionType `json:"maneuver,omitempty"`
	Path           []string   `json:"path,omitempty"`
	Automatic      bool       `json:"automatic"`
	Private        bool       `json:"private,omitempty"`
	PrivateFor     string     `json:"privateFor,omitempty"`
	Summary        string     `json:"summary,omitempty"`
}

type State struct {
	RoomID             string                `json:"roomID"`
	OwnerID            string                `json:"ownerID"`
	Status             string                `json:"status"`
	MaxPlayers         int                   `json:"maxPlayers"`
	Visibility         string                `json:"visibility"`
	TurnTimeoutSeconds int                   `json:"turnTimeoutSeconds"`
	RandomSeed         uint64                `json:"randomSeed"`
	RandomCounter      uint64                `json:"randomCounter"`
	MatchRoundNo       int                   `json:"matchRoundNo"`
	RoundNo            int                   `json:"roundNo"`
	SetupMode          SetupMode             `json:"setupMode"`
	Roster             map[string]*Player    `json:"roster"`
	PlayerOrder        []string              `json:"playerOrder"`
	Draft              *DraftState           `json:"draft,omitempty"`
	InitialPlayerID    string                `json:"initialPlayerID,omitempty"`
	InitialSide        Side                  `json:"initialSide,omitempty"`
	Coins              map[string]UnitType   `json:"coins"`
	CoinOwners         map[string]string     `json:"coinOwners"`
	Board              map[string]*UnitStack `json:"board"`
	Controls           map[string]Side       `json:"controls"`
	CurrentPlayer      string                `json:"currentPlayer,omitempty"`
	InitiativePlayer   string                `json:"initiativePlayer,omitempty"`
	InitiativeChanged  bool                  `json:"initiativeChanged"`
	Pending            *PendingDecision      `json:"pending,omitempty"`
	Log                []ActionEntry         `json:"log"`
	LastAction         *ActionEntry          `json:"lastAction,omitempty"`
	WinnerID           string                `json:"winnerID,omitempty"`
	WinnerSide         Side                  `json:"winnerSide,omitempty"`
	EndReason          string                `json:"endReason,omitempty"`
	GameStartTime      time.Time             `json:"gameStartTime,omitempty"`
	TurnStartedAt      time.Time             `json:"turnStartedAt,omitempty"`
	TurnDeadline       time.Time             `json:"turnDeadline,omitempty"`
	TurnTimeout        time.Duration         `json:"turnTimeout,omitempty"`
	ActionCounter      int                   `json:"actionCounter"`
	UnitSerial         int                   `json:"unitSerial"`
	NoHumanChecks      int                   `json:"noHumanChecks,omitempty"`
}

func (s *State) playerForSide(side Side) *Player {
	for _, playerID := range s.PlayerOrder {
		if player := s.Roster[playerID]; player != nil && player.Side == side {
			return player
		}
	}
	return nil
}

func (s *State) playerIDForSide(side Side) string {
	if player := s.playerForSide(side); player != nil {
		return player.PlayerID
	}
	return ""
}

func (s *State) sideForPlayer(playerID string) Side {
	if player := s.Roster[playerID]; player != nil {
		return player.Side
	}
	return ""
}

func otherSide(side Side) Side {
	if side == SideOak {
		return SideFalcon
	}
	return SideOak
}

func cloneState(state *State) *State {
	if state == nil {
		return nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		panic(err)
	}
	var result State
	if err := json.Unmarshal(data, &result); err != nil {
		panic(err)
	}
	ensureState(&result)
	return &result
}

func ensureState(state *State) {
	if state.Roster == nil {
		state.Roster = map[string]*Player{}
	}
	if state.PlayerOrder == nil {
		state.PlayerOrder = []string{}
	}
	if state.Coins == nil {
		state.Coins = map[string]UnitType{}
	}
	if state.CoinOwners == nil {
		state.CoinOwners = map[string]string{}
	}
	if state.Board == nil {
		state.Board = map[string]*UnitStack{}
	}
	if state.Controls == nil {
		state.Controls = map[string]Side{}
	}
	if state.Log == nil {
		state.Log = []ActionEntry{}
	}
	if state.SetupMode == "" {
		state.SetupMode = SetupRecommended
	}
	for _, player := range state.Roster {
		if player == nil {
			continue
		}
		if player.Roster == nil {
			player.Roster = []UnitType{}
		}
		if player.Bag == nil {
			player.Bag = []string{}
		}
		if player.Hand == nil {
			player.Hand = []string{}
		}
		if player.Discard == nil {
			player.Discard = []Discard{}
		}
		if player.Supply == nil {
			player.Supply = UnitCoins{}
		}
		if player.Lost == nil {
			player.Lost = UnitCoins{}
		}
	}
}

func asState(value any) (*State, error) {
	switch typed := value.(type) {
	case *State:
		if typed == nil {
			return nil, errors.New("warchest state is nil")
		}
		return typed, nil
	case State:
		return &typed, nil
	default:
		return nil, fmt.Errorf("unexpected warchest state %T", value)
	}
}
