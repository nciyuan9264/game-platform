package history

import (
	"encoding/json"
	"time"
)

const (
	EndReasonCompleted = "completed"
	EndReasonAbandoned = "abandoned"
)

type Game struct {
	ID                   int64           `json:"id"`
	RoomID               string          `json:"roomID"`
	GameType             string          `json:"gameType"`
	EngineVersion        string          `json:"engineVersion"`
	ReplaySchemaVersion  int             `json:"replaySchemaVersion"`
	StartedAt            time.Time       `json:"startedAt"`
	EndedAt              *time.Time      `json:"endedAt,omitempty"`
	DurationSeconds      int             `json:"durationSeconds"`
	WinnerUserID         *int64          `json:"winnerUserID,omitempty"`
	WinnerPlayerID       string          `json:"winnerPlayerID,omitempty"`
	EndReason            string          `json:"endReason,omitempty"`
	MaxPlayers           int             `json:"maxPlayers"`
	InitialState         json.RawMessage `json:"initialState,omitempty"`
	InitialReplayPayload []byte          `json:"-"`
	FinalResult          json.RawMessage `json:"finalResult,omitempty"`
	CreatedAt            time.Time       `json:"createdAt"`
	UpdatedAt            time.Time       `json:"updatedAt"`
	Players              []Player        `json:"players,omitempty"`
}

type Player struct {
	ID             int64     `json:"id"`
	GameID         int64     `json:"gameID"`
	UserID         *int64    `json:"userID,omitempty"`
	PlayerID       string    `json:"playerID"`
	DisplayName    string    `json:"displayName"`
	SeatIndex      int       `json:"seatIndex"`
	IsAI           bool      `json:"isAI"`
	FinalScore     *int      `json:"finalScore,omitempty"`
	FinalMoney     *int      `json:"finalMoney,omitempty"`
	FinalStocks    int       `json:"finalStocks"`
	FinalRank      *int      `json:"finalRank,omitempty"`
	IsWinner       bool      `json:"isWinner"`
	CreatedAt      time.Time `json:"createdAt"`
	SourcePlayerID string    `json:"-"`
}

type Event struct {
	ID            int64           `json:"id"`
	GameID        int64           `json:"gameID"`
	Seq           int             `json:"seq"`
	OccurredAt    time.Time       `json:"occurredAt"`
	PlayerID      string          `json:"playerID"`
	DisplayName   string          `json:"displayName"`
	CommandType   string          `json:"cmdType"`
	Payload       json.RawMessage `json:"payload"`
	StateSnapshot json.RawMessage `json:"stateSnapshot,omitempty"`
	StateBlob     []byte          `json:"-"`
	ReplayPayload []byte          `json:"-"`
}

type FinalizeInput struct {
	EndedAt         time.Time
	DurationSeconds int
	WinnerUserID    *int64
	WinnerPlayerID  string
	EndReason       string
	FinalResult     json.RawMessage
	Players         []Player
}

type Stats struct {
	TotalGames int     `json:"totalGames"`
	Wins       int     `json:"wins"`
	WinRate    float64 `json:"winRate"`
	AvgScore   float64 `json:"avgScore"`
}

type LeaderboardEntry struct {
	UserID      int64    `json:"userID"`
	PlayerID    string   `json:"playerID"`
	DisplayName string   `json:"displayName"`
	TotalGames  int      `json:"totalGames"`
	Wins        *int     `json:"wins,omitempty"`
	WinRate     *float64 `json:"winRate,omitempty"`
	AvgRank     *float64 `json:"avgRank,omitempty"`
}

type EventMeta struct {
	Seq         int    `json:"seq"`
	PlayerID    string `json:"playerID"`
	DisplayName string `json:"displayName"`
	CommandType string `json:"cmdType"`
}

type ReplaySnapshot struct {
	Seq          int            `json:"seq"`
	TotalEvents  int            `json:"totalEvents"`
	CurrentEvent *Event         `json:"currentEvent,omitempty"`
	RoomData     map[string]any `json:"roomData"`
	PlayersData  map[string]any `json:"playersData"`
	Result       map[string]any `json:"result"`
}

type ReplayMissingCounts struct {
	Games  int `json:"games"`
	Events int `json:"events"`
}
