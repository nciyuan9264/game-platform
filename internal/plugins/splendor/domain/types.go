package domain

import (
	"encoding/json"
	"time"

	"github.com/nciyuan9264/game-platform/internal/plugins/splendor/entities"
)

type RoomStatus string

const (
	RoomStatusMatch    RoomStatus = "match"
	RoomStatusWaiting  RoomStatus = "waiting"
	RoomStatusPlaying  RoomStatus = "playing"
	RoomStatusLastTurn RoomStatus = "last_turn"
	RoomStatusEnd      RoomStatus = "end"
)

type Player struct {
	UserID      string `json:"userID,omitempty"`
	PlayerID    string `json:"playerID"`
	DisplayName string `json:"displayName,omitempty"`
	Avatar      string `json:"avatar,omitempty"`
	Online      bool   `json:"online"`
	Ready       bool   `json:"ready"`
	AI          bool   `json:"ai"`
	SeatIndex   int    `json:"seatIndex"`
}

type PlayerState struct {
	NormalCard  []entities.NormalCard `json:"normalCard"`
	NobleCard   []entities.NobleCard  `json:"nobleCard"`
	Gem         map[string]int        `json:"gem"`
	Score       int                   `json:"score"`
	ReserveCard []entities.NormalCard `json:"reserveCard"`
}

type LastAction struct {
	Action   string          `json:"action"`
	PlayerID string          `json:"playerID"`
	Payload  json.RawMessage `json:"payload"`
}

type GameState struct {
	RoomID        string        `json:"roomID"`
	CurrentPlayer string        `json:"currentPlayer"`
	FirstPlayer   string        `json:"firstPlayer"`
	GameStartTime time.Time     `json:"gameStartTime"`
	RoomStatus    RoomStatus    `json:"roomStatus"`
	OwnerID       string        `json:"ownerID"`
	MaxPlayers    int           `json:"maxPlayers"`
	Visibility    string        `json:"visibility"`
	RandomSeed    uint64        `json:"randomSeed"`
	RandomCounter uint64        `json:"randomCounter"`
	TurnDeadline  time.Time     `json:"turnDeadline,omitempty"`
	TurnTimeout   time.Duration `json:"turnTimeout,omitempty"`

	PlayerOrder []string                        `json:"playerOrder"`
	Roster      map[string]*Player              `json:"roster"`
	NormalCards map[string]*entities.NormalCard `json:"normalCards"`
	NobleCards  map[string]*entities.NobleCard  `json:"nobleCards"`
	Gems        map[string]int                  `json:"gems"`
	Players     map[string]*PlayerState         `json:"players"`
	LastData    *LastAction                     `json:"lastData,omitempty"`
}

type Command struct {
	Type     string
	PlayerID string
	Payload  json.RawMessage
}

type Room struct {
	ID          string
	PlayerSeq   []string
	Connections map[string]*Player
	State       *GameState
}

func NewRoom(state *GameState) *Room {
	return &Room{
		ID:          state.RoomID,
		PlayerSeq:   state.PlayerOrder,
		Connections: state.Roster,
		State:       state,
	}
}
