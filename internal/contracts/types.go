package contracts

import (
	"encoding/json"
	"time"
)

const (
	GameDaVinci  = "davinci"
	GameAcquire  = "acquire"
	GameSplendor = "splendor"
	GameYinsh    = "yinsh"
	GameOthello  = "othello"
	GameFugitive = "fugitive"
	GameWarChest = "warchest"
	GameQuoridor = "quoridor"
	GameHex      = "hex"

	MaxOwnedRooms         = 5
	RoomStatusCreating    = "creating"
	RoomStatusOpen        = "open"
	RoomStatusClosing     = "closing"
	RoomStatusClosed      = "closed"
	RoomStatusMatch       = "match"
	RoomStatusPlaying     = "playing"
	RoomStatusEnded       = "ended"
	RoomStatusInterrupted = "interrupted"
	VisibilityPublic      = "public"
	VisibilityPrivate     = "private"

	MatchStatusPreparing = "preparing"
	MatchStatusRunning   = "running"
	MatchStatusEnding    = "ending"
	MatchStatusEnded     = "ended"
	MatchStatusAborted   = "aborted"

	NodeTypeGateway    = "gateway"
	NodeTypeRoomHost   = "roomhost"
	NodeStatusReady    = "ready"
	NodeStatusDraining = "draining"

	GameCatalogActionRegister = "register"
	GameCatalogActionPromote  = "promote"
	GameCatalogActionDisable  = "disable"
	GameCatalogActionRollback = "rollback"

	MessageTypeMatchSync     = "MATCH_SYNC"
	MessageTypeRoomSync      = "ROOM_SYNC"
	MessageTypeError         = "ERROR"
	MessageTypeCommandResult = "COMMAND_RESULT"
	RealtimeSchemaVersion    = 2

	RuntimeCommandRoomCreate  = "runtime_room_create"
	RuntimeCommandRoomClose   = "runtime_room_close"
	RuntimeCommandMatchCreate = "runtime_match_create"

	CommandLogKindCommand    = "COMMAND"
	CommandLogKindOwnerFence = "OWNER_FENCE"

	ExecutionModeLive   = "live"
	ExecutionModeReplay = "replay"
)

const SpectatorDelay = 5 * time.Minute

func IsTerminalRoomStatus(status string) bool {
	return status == RoomStatusClosed || status == RoomStatusEnded || status == RoomStatusInterrupted
}

func IsTerminalMatchStatus(status string) bool {
	return status == MatchStatusEnded || status == MatchStatusAborted
}

type APIResponse struct {
	StatusCode int         `json:"status_code"`
	Message    string      `json:"message"`
	Data       interface{} `json:"data,omitempty"`
	Error      string      `json:"error,omitempty"`
}

type UserIdentity struct {
	UserID      string   `json:"userID"`
	Name        string   `json:"name,omitempty"`
	DisplayName string   `json:"displayName"`
	Email       string   `json:"email,omitempty"`
	Avatar      string   `json:"avatar,omitempty"`
	Roles       []string `json:"roles,omitempty"`
}

type NodeInfo struct {
	NodeID         string            `json:"nodeID"`
	Type           string            `json:"type"`
	Addr           string            `json:"addr"`
	Version        string            `json:"version"`
	GameVersions   map[string]string `json:"gameVersions,omitempty"`
	Status         string            `json:"status"`
	ActiveRooms    int               `json:"activeRooms"`
	ActiveSessions int               `json:"activeSessions"`
	LastHeartbeat  time.Time         `json:"lastHeartbeat"`
}

type GameCatalogEntry struct {
	GameType            string    `json:"gameType"`
	ActiveEngineVersion string    `json:"activeEngineVersion"`
	DefaultMaxPlayers   int       `json:"defaultMaxPlayers"`
	ReplaySchemaVersion int       `json:"replaySchemaVersion"`
	EnabledForNewRooms  bool      `json:"enabledForNewRooms"`
	IsDefault           bool      `json:"isDefault"`
	Revision            int64     `json:"revision"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

type GameCatalogEvent struct {
	EventID   int64             `json:"eventID"`
	GameType  string            `json:"gameType"`
	Action    string            `json:"action"`
	Previous  *GameCatalogEntry `json:"previous,omitempty"`
	Current   *GameCatalogEntry `json:"current,omitempty"`
	Artifact  string            `json:"artifact,omitempty"`
	Actor     string            `json:"actor,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
}

type RoomRecord struct {
	RoomID                 string    `json:"roomID"`
	GameType               string    `json:"gameType"`
	Visibility             string    `json:"visibility"`
	DisplayName            string    `json:"displayName,omitempty"`
	TurnTimeoutSeconds     int       `json:"turnTimeoutSeconds,omitempty"`
	OwnerUserID            string    `json:"ownerUserID"`
	OwnerPlayerID          string    `json:"ownerPlayerID"`
	OwnerNode              string    `json:"ownerNode"`
	Epoch                  int64     `json:"epoch"`
	Status                 string    `json:"status"`
	MaxPlayers             int       `json:"maxPlayers"`
	PlayerCount            int       `json:"playerCount"`
	ProgressCurrent        int       `json:"progressCurrent,omitempty"`
	ProgressTotal          int       `json:"progressTotal,omitempty"`
	LastAppliedSeq         int64     `json:"lastAppliedSeq,omitempty"`
	EngineVersion          string    `json:"engineVersion"`
	CreatedAt              time.Time `json:"createdAt"`
	UpdatedAt              time.Time `json:"updatedAt"`
	LeaseExpiresAt         time.Time `json:"leaseExpiresAt,omitempty"`
	CurrentMatchID         string    `json:"currentMatchID,omitempty"`
	CurrentMatchStatus     string    `json:"currentMatchStatus,omitempty"`
	NextRoundNo            int       `json:"nextRoundNo"`
	SpectatorAvailable     bool      `json:"spectatorAvailable,omitempty"`
	CurrentUserParticipant bool      `json:"currentUserParticipant,omitempty"`
}

type ConnectionRef struct {
	GatewayNode string `json:"gatewayNode"`
	SessionID   string `json:"sessionID"`
}

func (c ConnectionRef) Valid() bool {
	return c.GatewayNode != "" && c.SessionID != ""
}

type SessionRecord struct {
	ConnectionRef
	UserID      string    `json:"userID"`
	PlayerID    string    `json:"playerID"`
	Name        string    `json:"name,omitempty"`
	Email       string    `json:"email,omitempty"`
	Avatar      string    `json:"avatar,omitempty"`
	DisplayName string    `json:"displayName"`
	ConnectedAt time.Time `json:"connectedAt"`
	LastSeen    time.Time `json:"lastSeen"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

type PresenceRecord struct {
	ConnectionRef
	RoomID   string    `json:"roomID"`
	UserID   string    `json:"userID"`
	PlayerID string    `json:"playerID"`
	Online   bool      `json:"online"`
	JoinedAt time.Time `json:"joinedAt"`
	LastSeen time.Time `json:"lastSeen"`
}

type AdminUserSummary struct {
	UserID         string    `json:"userID"`
	Email          string    `json:"email,omitempty"`
	Name           string    `json:"name,omitempty"`
	Avatar         string    `json:"avatar,omitempty"`
	DisplayName    string    `json:"displayName"`
	Online         bool      `json:"online"`
	ActiveSessions int       `json:"activeSessions"`
	ActiveRooms    int       `json:"activeRooms"`
	ActiveMatches  int       `json:"activeMatches"`
	GatewayNodes   []string  `json:"gatewayNodes"`
	RoomIDs        []string  `json:"roomIDs"`
	OwnedRoomIDs   []string  `json:"ownedRoomIDs"`
	MatchIDs       []string  `json:"matchIDs"`
	OwnedMatchIDs  []string  `json:"ownedMatchIDs"`
	FirstSeen      time.Time `json:"firstSeen,omitempty"`
	LastSeen       time.Time `json:"lastSeen,omitempty"`
}

type AuthUserRecord struct {
	UserID    string    `json:"userID"`
	Email     string    `json:"email,omitempty"`
	Name      string    `json:"name,omitempty"`
	Avatar    string    `json:"avatar,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

type MatchPlayerRecord struct {
	UserID      string    `json:"userID"`
	PlayerID    string    `json:"playerID"`
	DisplayName string    `json:"displayName,omitempty"`
	Name        string    `json:"name,omitempty"`
	Email       string    `json:"email,omitempty"`
	Avatar      string    `json:"avatar,omitempty"`
	GatewayNode string    `json:"gatewayNode,omitempty"`
	Online      bool      `json:"online"`
	IsAI        bool      `json:"isAI"`
	SeatIndex   int       `json:"seatIndex"`
	Ready       bool      `json:"ready"`
	GameRole    string    `json:"gameRole,omitempty"`
	JoinedAt    time.Time `json:"joinedAt,omitempty"`
	LastSeen    time.Time `json:"lastSeen,omitempty"`
}

type MatchRecord struct {
	MatchID            string              `json:"matchID"`
	RuntimeRoomID      string              `json:"runtimeRoomID"`
	GameType           string              `json:"gameType"`
	DisplayName        string              `json:"displayName,omitempty"`
	TurnTimeoutSeconds int                 `json:"turnTimeoutSeconds,omitempty"`
	Status             string              `json:"status"`
	OwnerUserID        string              `json:"ownerUserID"`
	OwnerPlayerID      string              `json:"ownerPlayerID"`
	RuntimeOwnerNode   string              `json:"runtimeOwnerNode"`
	Epoch              int64               `json:"epoch"`
	MaxPlayers         int                 `json:"maxPlayers"`
	PlayerCount        int                 `json:"playerCount"`
	EngineVersion      string              `json:"engineVersion"`
	RoundNo            int                 `json:"roundNo"`
	LastAppliedSeq     int64               `json:"lastAppliedSeq"`
	RandomSeed         uint64              `json:"-"`
	StartedAt          time.Time           `json:"startedAt"`
	UpdatedAt          time.Time           `json:"updatedAt"`
	EndedAt            time.Time           `json:"endedAt,omitempty"`
	DurationSeconds    int64               `json:"durationSeconds"`
	WinnerUserID       string              `json:"winnerUserID,omitempty"`
	WinnerPlayerID     string              `json:"winnerPlayerID,omitempty"`
	EndReason          string              `json:"endReason,omitempty"`
	Players            []MatchPlayerRecord `json:"players,omitempty"`
}

type NATSSubjectInfo struct {
	Name        string `json:"name"`
	Subject     string `json:"subject"`
	Mode        string `json:"mode"`
	Description string `json:"description"`
}

type ClientCommand struct {
	SchemaVersion int         `json:"schemaVersion"`
	CommandID     string      `json:"commandID"`
	MatchID       string      `json:"matchID"`
	Type          string      `json:"type"`
	Payload       interface{} `json:"payload"`
}

type RoomCommand struct {
	SchemaVersion int    `json:"schemaVersion"`
	CommandID     string `json:"commandID"`
	TraceID       string `json:"traceID"`
	RoomID        string `json:"roomID"`
	MatchID       string `json:"matchID"`
	GameType      string `json:"gameType"`
	Epoch         int64  `json:"epoch"`
	UserID        string `json:"userID"`
	PlayerID      string `json:"playerID"`
	ConnectionRef
	Type    string      `json:"type"`
	Payload interface{} `json:"payload,omitempty"`
	SentAt  time.Time   `json:"sentAt"`
	Replay  bool        `json:"-"`
}

func (c RoomCommand) MarshalJSON() ([]byte, error) {
	type Alias RoomCommand
	return json.Marshal(struct {
		Alias
		SessionID   string `json:"sessionID"`
		GatewayNode string `json:"gatewayNode"`
	}{
		Alias:       Alias(c),
		SessionID:   c.ConnectionRef.SessionID,
		GatewayNode: c.ConnectionRef.GatewayNode,
	})
}

func (c *RoomCommand) UnmarshalJSON(data []byte) error {
	type Alias RoomCommand
	aux := struct {
		Alias
		SessionID   string `json:"sessionID"`
		GatewayNode string `json:"gatewayNode"`
	}{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*c = RoomCommand(aux.Alias)
	if c.ConnectionRef.SessionID == "" {
		c.ConnectionRef.SessionID = aux.SessionID
	}
	if c.ConnectionRef.GatewayNode == "" {
		c.ConnectionRef.GatewayNode = aux.GatewayNode
	}
	return nil
}

type CommandAck struct {
	CommandID  string `json:"commandID"`
	RoomID     string `json:"roomID"`
	MatchID    string `json:"matchID,omitempty"`
	Epoch      int64  `json:"epoch,omitempty"`
	OwnerNode  string `json:"ownerNode,omitempty"`
	IntentHash string `json:"-"`
	Accepted   bool   `json:"accepted"`
	Seq        int64  `json:"seq,omitempty"`
	ErrorCode  string `json:"errorCode,omitempty"`
	Message    string `json:"message,omitempty"`
}

type OutboundMessage struct {
	SchemaVersion  int           `json:"schemaVersion"`
	MessageID      string        `json:"messageID"`
	TraceID        string        `json:"traceID"`
	RoomID         string        `json:"roomID"`
	MatchID        string        `json:"matchID"`
	Seq            int64         `json:"seq"`
	Epoch          int64         `json:"epoch"`
	OwnerNode      string        `json:"ownerNode"`
	TargetUserID   string        `json:"targetUserID"`
	TargetPlayerID string        `json:"targetPlayerID"`
	TargetConn     ConnectionRef `json:"targetConn"`
	Reliable       bool          `json:"reliable"`
	CloseAfter     bool          `json:"closeAfter,omitempty"`
	Payload        interface{}   `json:"payload"`
	CreatedAt      time.Time     `json:"createdAt"`
}

func (m OutboundMessage) TargetSessionIDValue() string {
	return m.TargetConn.SessionID
}

func (m OutboundMessage) TargetGatewayNodeValue() string {
	return m.TargetConn.GatewayNode
}

func (m OutboundMessage) MarshalJSON() ([]byte, error) {
	type Alias OutboundMessage
	return json.Marshal(struct {
		Alias
		TargetSessionID   string `json:"targetSessionID"`
		TargetGatewayNode string `json:"targetGatewayNode"`
	}{
		Alias:             Alias(m),
		TargetSessionID:   m.TargetConn.SessionID,
		TargetGatewayNode: m.TargetConn.GatewayNode,
	})
}

func (m *OutboundMessage) UnmarshalJSON(data []byte) error {
	type Alias OutboundMessage
	aux := struct {
		Alias
		TargetSessionID   string `json:"targetSessionID"`
		TargetGatewayNode string `json:"targetGatewayNode"`
	}{}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*m = OutboundMessage(aux.Alias)
	if m.TargetConn.SessionID == "" {
		m.TargetConn.SessionID = aux.TargetSessionID
	}
	if m.TargetConn.GatewayNode == "" {
		m.TargetConn.GatewayNode = aux.TargetGatewayNode
	}
	return nil
}

type RoomPlayer struct {
	UserID      string `json:"userID,omitempty"`
	PlayerID    string `json:"playerID"`
	DisplayName string `json:"displayName,omitempty"`
	Avatar      string `json:"avatar,omitempty"`
	Online      bool   `json:"online"`
	AI          bool   `json:"ai"`
	Ready       bool   `json:"ready"`
	SeatIndex   int    `json:"seatIndex"`
}

type MatchSync struct {
	Type        string                `json:"type"`
	RoomID      string                `json:"roomID"`
	MatchID     string                `json:"matchID"`
	OwnerID     string                `json:"ownerID"`
	Status      string                `json:"status"`
	PlayerID    string                `json:"playerID"`
	Visibility  string                `json:"visibility"`
	MaxPlayers  int                   `json:"maxPlayers"`
	Players     map[string]RoomPlayer `json:"players"`
	PlayerOrder []string              `json:"playerOrder"`
	Message     string                `json:"message,omitempty"`
}

type RoomSync struct {
	Type     string      `json:"type"`
	RoomID   string      `json:"roomID"`
	MatchID  string      `json:"matchID"`
	PlayerID string      `json:"playerId"`
	OwnerID  string      `json:"ownerID"`
	Seq      int64       `json:"seq"`
	RoomData interface{} `json:"roomData"`
	CardData interface{} `json:"cardData,omitempty"`
	Result   interface{} `json:"result,omitempty"`
}

type ErrorMessage struct {
	SchemaVersion int    `json:"schemaVersion"`
	Type          string `json:"type"`
	RoomID        string `json:"roomID"`
	MatchID       string `json:"matchID"`
	Seq           int64  `json:"seq"`
	CommandID     string `json:"commandID,omitempty"`
	Code          string `json:"code"`
	Message       string `json:"message"`
}

type CommandResult struct {
	SchemaVersion int    `json:"schemaVersion"`
	Type          string `json:"type"`
	RoomID        string `json:"roomID"`
	MatchID       string `json:"matchID"`
	Seq           int64  `json:"seq"`
	CommandID     string `json:"commandID"`
	Accepted      bool   `json:"accepted"`
	ErrorCode     string `json:"errorCode,omitempty"`
	Message       string `json:"message,omitempty"`
	Retryable     bool   `json:"retryable,omitempty"`
}

type CommandLogRecord struct {
	Kind       string         `json:"kind"`
	RoomID     string         `json:"roomID"`
	MatchID    string         `json:"matchID"`
	Seq        int64          `json:"seq"`
	CommandID  string         `json:"commandID"`
	Epoch      int64          `json:"epoch"`
	OwnerNode  string         `json:"ownerNode,omitempty"`
	PlayerID   string         `json:"playerID"`
	Type       string         `json:"type"`
	Payload    interface{}    `json:"payload,omitempty"`
	Operations []GameLogEvent `json:"operations,omitempty"`
	CreatedAt  time.Time      `json:"createdAt"`

	StreamSequence uint64 `json:"-"`
}

type CommandLogAppendResult struct {
	Seq       int64
	Duplicate bool
}

type GameLogEvent struct {
	RoomID     string      `json:"roomID"`
	MatchID    string      `json:"matchID"`
	GameType   string      `json:"gameType"`
	Seq        int64       `json:"seq"`
	EventID    string      `json:"eventID"`
	CommandID  string      `json:"commandID,omitempty"`
	PlayerID   string      `json:"playerID,omitempty"`
	Visibility string      `json:"visibility"`
	Type       string      `json:"type"`
	Payload    interface{} `json:"payload,omitempty"`
	CreatedAt  time.Time   `json:"createdAt"`
}

type SnapshotRecord struct {
	RoomID        string    `json:"roomID"`
	MatchID       string    `json:"matchID"`
	Seq           int64     `json:"seq"`
	Epoch         int64     `json:"epoch"`
	EngineVersion string    `json:"engineVersion"`
	State         []byte    `json:"state"`
	CreatedAt     time.Time `json:"createdAt"`
}

type SpectatorSnapshotRecord struct {
	RoomID      string    `json:"roomID"`
	MatchID     string    `json:"matchID"`
	GameType    string    `json:"gameType"`
	Seq         int64     `json:"seq"`
	Epoch       int64     `json:"epoch"`
	OwnerNode   string    `json:"ownerNode"`
	Payload     any       `json:"payload"`
	CapturedAt  time.Time `json:"capturedAt"`
	AvailableAt time.Time `json:"availableAt"`
}

type SpectatorSession struct {
	SessionID string    `json:"sessionID"`
	RoomID    string    `json:"roomID"`
	MatchID   string    `json:"matchID"`
	UserID    string    `json:"userID"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type RuntimeEvent struct {
	ID        int64       `json:"id"`
	RoomID    string      `json:"roomID"`
	MatchID   string      `json:"matchID,omitempty"`
	TraceID   string      `json:"traceID,omitempty"`
	NodeID    string      `json:"nodeID"`
	Component string      `json:"component"`
	Event     string      `json:"event"`
	Message   string      `json:"message"`
	Payload   interface{} `json:"payload,omitempty"`
	CreatedAt time.Time   `json:"createdAt"`
}
