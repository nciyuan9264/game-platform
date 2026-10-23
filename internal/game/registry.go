package game

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

var (
	ErrInvalidDefinition = errors.New("invalid game definition")
	ErrUnknownGame       = errors.New("unknown game")
)

type State any

type RoomConfig struct {
	RoomID        string
	GameType      string
	OwnerUserID   string
	OwnerPlayerID string
	MaxPlayers    int
	RandomSeed    uint64
	Visibility    string
}

type ViewRequest struct {
	RoomID     string
	MatchID    string
	GameType   string
	PlayerID   string
	Visibility string
	MaxPlayers int
	Seq        int64
}

type AIRequest struct {
	RoomID          string
	GameType        string
	PlayerID        string
	State           State
	FallbackCommand *contracts.RoomCommand
	Delay           time.Duration
	Timeout         time.Duration
}

type ApplyResult struct {
	Status     string
	Finished   bool
	AIRequests []AIRequest
	Effects    []DeliveryEffect
}

type ScheduledCommand struct {
	Key       string
	ExecuteAt time.Time
	PlayerID  string
	Type      string
	Payload   any
}

type DeliveryEffect struct {
	Audience       string
	TargetPlayerID string
	Payload        any
	Reliable       bool
	CloseAfter     bool
}

const (
	AudienceAll    = "all"
	AudiencePlayer = "player"
)

type LogVisibility string

const (
	LogPublic  LogVisibility = "public"
	LogPrivate LogVisibility = "private"
	LogSystem  LogVisibility = "system"
)

type OperationEvent struct {
	Type       string
	PlayerID   string
	Visibility LogVisibility
	Payload    any
}

type SnapshotHint struct {
	Reason string
	Force  bool
}

type LogSink interface {
	AppendOperation(ctx context.Context, event OperationEvent) error
	SaveSnapshotHint(ctx context.Context, hint SnapshotHint) error
}

type LogContext interface {
	LogSink() LogSink
}

type Engine interface {
	GameType() string
	Version() string
	InitRoom(ctx context.Context, cfg RoomConfig) (State, error)
	EncodeState(ctx context.Context, state State) ([]byte, error)
	DecodeState(ctx context.Context, data []byte) (State, error)
	ApplyCommand(ctx context.Context, state State, cmd contracts.RoomCommand) (ApplyResult, error)
	BuildPlayerView(ctx context.Context, state State, req ViewRequest) (any, error)
}

type RandomSeedProvider interface {
	RandomSeed(ctx context.Context, state State) (uint64, error)
}

type AIPlanner interface {
	Decide(ctx context.Context, req AIRequest) (contracts.RoomCommand, error)
}

type Scheduler interface {
	PendingCommands(ctx context.Context, state State) ([]ScheduledCommand, error)
}

type AutomationStateResetter interface {
	ResetAutomationState(ctx context.Context, state State) error
}

type RoomMetadata struct {
	Visibility string
}

type RoomMetadataProvider interface {
	RoomMetadata(ctx context.Context, state State) (RoomMetadata, error)
}

type RosterPlayer struct {
	UserID      string
	PlayerID    string
	DisplayName string
	Online      bool
	IsAI        bool
	SeatIndex   int
	Ready       bool
}

type RosterProvider interface {
	Roster(ctx context.Context, state State) ([]RosterPlayer, error)
}

type RoomProgress struct {
	Current int
	Total   int
}

type ProgressProvider interface {
	Progress(ctx context.Context, state State) (RoomProgress, error)
}

type SpectatorViewProvider interface {
	BuildSpectatorView(ctx context.Context, state State, seq int64) (any, error)
}

type HumanPresenceCheck struct {
	ConsecutiveEmpty int
	Limit            int
	ShouldClose      bool
}

type HumanPresenceObserver interface {
	ObserveHumanPresence(ctx context.Context, state State, hasHuman bool) (HumanPresenceCheck, error)
}

type HistoryPlayer struct {
	UserID      string
	PlayerID    string
	SeatIndex   int
	IsAI        bool
	FinalScore  *int
	FinalMoney  *int
	FinalStocks int
	FinalRank   *int
	IsWinner    bool
}

type HistoryProjection struct {
	Started           bool
	Finished          bool
	AbandonedEligible bool
	StartedAt         time.Time
	MaxPlayers        int
	InitialState      []byte
	StateBlob         []byte
	FinalResult       any
	WinnerUserID      string
	WinnerPlayerID    string
	Players           []HistoryPlayer
}

type HistoryProvider interface {
	ProjectHistory(ctx context.Context, state State) (HistoryProjection, error)
	IsRecordableCommand(commandType string) bool
	DecodeReplayState(
		ctx context.Context,
		initialState,
		stateSnapshot,
		stateBlob []byte,
		players []HistoryPlayer,
	) (State, error)
	BuildReplaySnapshot(ctx context.Context, state State, seq, total int, commandType, playerID string, payload any) (any, error)
}

type Definition struct {
	GameType          string
	Version           string
	DefaultMaxPlayers int
	Engine            Engine
	AI                AIPlanner
	Scheduler         Scheduler
	History           HistoryProvider
}

type Registry struct {
	mu          sync.RWMutex
	defs        map[string]Definition
	defaultGame string
}

func NewRegistry(defs ...Definition) *Registry {
	r := &Registry{defs: map[string]Definition{}}
	for _, def := range defs {
		r.MustRegister(def)
	}
	return r
}

func (r *Registry) Register(def Definition) error {
	if def.Engine == nil {
		return fmt.Errorf("%w: engine is required", ErrInvalidDefinition)
	}
	if def.GameType == "" {
		def.GameType = def.Engine.GameType()
	}
	if def.Version == "" {
		def.Version = def.Engine.Version()
	}
	if def.GameType == "" {
		return fmt.Errorf("%w: game type is required", ErrInvalidDefinition)
	}
	if def.Version == "" {
		return fmt.Errorf("%w: version is required", ErrInvalidDefinition)
	}
	if def.DefaultMaxPlayers <= 0 {
		def.DefaultMaxPlayers = 4
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.defs == nil {
		r.defs = map[string]Definition{}
	}
	r.defs[def.GameType] = def
	if r.defaultGame == "" {
		r.defaultGame = def.GameType
	}
	return nil
}

func (r *Registry) MustRegister(def Definition) {
	if err := r.Register(def); err != nil {
		panic(err)
	}
}

func (r *Registry) Versions() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	versions := make(map[string]string, len(r.defs))
	for gameType, def := range r.defs {
		versions[gameType] = def.Version
	}
	return versions
}

func (r *Registry) Get(gameType string) (Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, ok := r.defs[gameType]
	return def, ok
}

func (r *Registry) DefaultDefinition() (Definition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.defaultGame == "" {
		return Definition{}, false
	}
	def, ok := r.defs[r.defaultGame]
	return def, ok
}

func (r *Registry) Definitions() []Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]Definition, 0, len(r.defs))
	for _, def := range r.defs {
		defs = append(defs, def)
	}
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].GameType < defs[j].GameType
	})
	return defs
}
