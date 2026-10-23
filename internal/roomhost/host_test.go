package roomhost

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	acquiregame "github.com/nciyuan9264/game-platform/internal/acquire"
	acquiredomain "github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/coordination"
	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/gamelog"
	"github.com/nciyuan9264/game-platform/internal/history"
	"github.com/nciyuan9264/game-platform/internal/transport"
)

type roomhostTestEngine struct {
	gameType string
	version  string
	cfg      game.RoomConfig
	roster   []game.RosterPlayer
}

func (e *roomhostTestEngine) GameType() string { return e.gameType }

func (e *roomhostTestEngine) Version() string { return e.version }

func (e *roomhostTestEngine) InitRoom(ctx context.Context, cfg game.RoomConfig) (game.State, error) {
	e.cfg = cfg
	return map[string]any{"roomID": cfg.RoomID}, nil
}

func (e *roomhostTestEngine) EncodeState(ctx context.Context, state game.State) ([]byte, error) {
	return []byte(`{}`), nil
}

func (e *roomhostTestEngine) DecodeState(ctx context.Context, data []byte) (game.State, error) {
	return map[string]any{}, nil
}

func (e *roomhostTestEngine) ApplyCommand(ctx context.Context, state game.State, cmd contracts.RoomCommand) (game.ApplyResult, error) {
	return game.ApplyResult{}, nil
}

func (e *roomhostTestEngine) BuildPlayerView(ctx context.Context, state game.State, req game.ViewRequest) (any, error) {
	return map[string]any{"playerID": req.PlayerID}, nil
}

func (e *roomhostTestEngine) Roster(context.Context, game.State) ([]game.RosterPlayer, error) {
	return e.roster, nil
}

type countingState struct {
	Count int `json:"count"`
}

type countingEngine struct {
	applied []contracts.RoomCommand
}

func (e *countingEngine) GameType() string { return "counting" }

func (e *countingEngine) Version() string { return "counting-v1" }

func (e *countingEngine) InitRoom(ctx context.Context, cfg game.RoomConfig) (game.State, error) {
	return &countingState{}, nil
}

func (e *countingEngine) EncodeState(ctx context.Context, state game.State) ([]byte, error) {
	return json.Marshal(state)
}

func (e *countingEngine) DecodeState(ctx context.Context, data []byte) (game.State, error) {
	var state countingState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (e *countingEngine) ApplyCommand(ctx context.Context, state game.State, cmd contracts.RoomCommand) (game.ApplyResult, error) {
	counting := state.(*countingState)
	switch payload := cmd.Payload.(type) {
	case map[string]any:
		if delta, ok := payload["delta"].(int); ok {
			counting.Count += delta
		} else if delta, ok := payload["delta"].(float64); ok {
			counting.Count += int(delta)
		}
	case map[string]int:
		counting.Count += payload["delta"]
	}
	e.applied = append(e.applied, cmd)
	return game.ApplyResult{}, nil
}

func (e *countingEngine) BuildPlayerView(ctx context.Context, state game.State, req game.ViewRequest) (any, error) {
	return state, nil
}

type automationResetEngine struct {
	countingEngine
	resetCount int
}

func (e *automationResetEngine) ResetAutomationState(context.Context, game.State) error {
	e.resetCount++
	return nil
}

type loggingEngine struct {
	countingEngine
}

func (e *loggingEngine) ApplyCommand(ctx context.Context, state game.State, cmd contracts.RoomCommand) (game.ApplyResult, error) {
	result, err := e.countingEngine.ApplyCommand(ctx, state, cmd)
	if err != nil {
		return result, err
	}
	err = gamelog.FromContext(ctx).AppendOperation(ctx, game.OperationEvent{
		Type:       "count_changed",
		PlayerID:   cmd.PlayerID,
		Visibility: game.LogPublic,
		Payload:    cmd.Payload,
	})
	return result, err
}

func (e *loggingEngine) BuildSpectatorView(_ context.Context, state game.State, seq int64) (any, error) {
	return map[string]any{"seq": seq, "state": state}, nil
}

type fakeHistoryLookup struct {
	id       int64
	nextSeq  int
	finished bool
	err      error
	calls    []time.Time
}

type fakeRoomhostStore struct {
	snapshot        contracts.SnapshotRecord
	snapshotErr     error
	room            contracts.RoomRecord
	roomErr         error
	match           contracts.MatchRecord
	session         contracts.SessionRecord
	sessionErr      error
	presences       []contracts.PresenceRecord
	presenceErr     error
	outboundErr     error
	savedOutbound   []contracts.OutboundMessage
	savedSnapshots  []contracts.SnapshotRecord
	savedSpectator  []contracts.SpectatorSnapshotRecord
	setRooms        []contracts.RoomRecord
	rosterMatchID   string
	rosterPlayers   []contracts.MatchPlayerRecord
	matchPlayers    []contracts.MatchPlayerRecord
	matchPlayersErr error
	operationEvents []contracts.GameLogEvent
	startedHistory  []history.Game
	finishedHistory []int64
	historyLookup   fakeHistoryLookup
	visibilityErr   error
	visibilityCalls []string
}

func (s *fakeRoomhostStore) SaveRuntimeEvent(context.Context, contracts.RuntimeEvent) error {
	return nil
}

func (s *fakeRoomhostStore) GetRoom(context.Context, string) (contracts.RoomRecord, error) {
	if s.roomErr != nil {
		return contracts.RoomRecord{}, s.roomErr
	}
	if s.room.RoomID != "" {
		return s.room, nil
	}
	return contracts.RoomRecord{}, errors.New("not implemented")
}

func (s *fakeRoomhostStore) ListRooms(context.Context, string) ([]contracts.RoomRecord, error) {
	if s.room.RoomID == "" {
		return nil, nil
	}
	return []contracts.RoomRecord{s.room}, nil
}

func (s *fakeRoomhostStore) GetMatch(_ context.Context, matchID string) (contracts.MatchRecord, error) {
	if s.match.MatchID != "" {
		return s.match, nil
	}
	return contracts.MatchRecord{
		MatchID:       matchID,
		RuntimeRoomID: s.room.RoomID,
		Status:        contracts.MatchStatusRunning,
		EngineVersion: s.room.EngineVersion,
		RandomSeed:    1,
	}, nil
}

func (s *fakeRoomhostStore) SetRoom(_ context.Context, room contracts.RoomRecord) error {
	s.setRooms = append(s.setRooms, room)
	return nil
}

func (s *fakeRoomhostStore) SetRoomVisibilityIfOwner(_ context.Context, roomID, visibility, ownerNode string, epoch int64) error {
	s.visibilityCalls = append(s.visibilityCalls, fmt.Sprintf("%s:%s:%s:%d", roomID, visibility, ownerNode, epoch))
	return s.visibilityErr
}

func (s *fakeRoomhostStore) SetMatchRuntime(context.Context, string, string, string, int64, int64, int) error {
	return nil
}

func (s *fakeRoomhostStore) ClaimMatchRuntime(context.Context, string, string, int64) error {
	return nil
}

func (s *fakeRoomhostStore) FinishMatch(context.Context, string, string, string, string, string, int64, int64) error {
	return nil
}

func (s *fakeRoomhostStore) ListExpiredRooms(context.Context, time.Time, int) ([]contracts.RoomRecord, error) {
	return nil, nil
}

func (s *fakeRoomhostStore) ReserveRoomOwner(_ context.Context, _ string, _ int64, nodeID string, now time.Time, recoveryTimeout time.Duration) (contracts.RoomRecord, error) {
	rec := s.room
	rec.OwnerNode = nodeID
	rec.Epoch++
	rec.LeaseExpiresAt = now.Add(recoveryTimeout)
	return rec, nil
}

func (s *fakeRoomhostStore) VerifyRoomOwnerCandidate(context.Context, string, string, int64) error {
	return nil
}

func (s *fakeRoomhostStore) MarkRoomOwnerReady(context.Context, string, string, int64, time.Time) error {
	return nil
}

func (s *fakeRoomhostStore) RenewRoomOwnerProjection(context.Context, string, string, int64, time.Time) error {
	return nil
}

func (s *fakeRoomhostStore) GetSession(context.Context, string) (contracts.SessionRecord, error) {
	if s.sessionErr != nil {
		return contracts.SessionRecord{}, s.sessionErr
	}
	if s.session.SessionID == "" {
		return contracts.SessionRecord{}, sql.ErrNoRows
	}
	return s.session, nil
}

func (s *fakeRoomhostStore) ListPresence(context.Context, string) ([]contracts.PresenceRecord, error) {
	if s.presenceErr != nil {
		return nil, s.presenceErr
	}
	return append([]contracts.PresenceRecord(nil), s.presences...), nil
}

func (s *fakeRoomhostStore) LatestSnapshot(context.Context, string) (contracts.SnapshotRecord, error) {
	if s.snapshotErr != nil {
		return contracts.SnapshotRecord{}, s.snapshotErr
	}
	if s.snapshot.RoomID == "" {
		return contracts.SnapshotRecord{}, sql.ErrNoRows
	}
	return s.snapshot, nil
}

func (s *fakeRoomhostStore) SaveSnapshot(_ context.Context, snapshot contracts.SnapshotRecord) error {
	s.savedSnapshots = append(s.savedSnapshots, snapshot)
	return nil
}

func (s *fakeRoomhostStore) SaveSpectatorSnapshot(_ context.Context, snapshot contracts.SpectatorSnapshotRecord) error {
	s.savedSpectator = append(s.savedSpectator, snapshot)
	return nil
}

func (s *fakeRoomhostStore) GetCommandAck(context.Context, string, string, string, string) (contracts.CommandAck, error) {
	return contracts.CommandAck{}, sql.ErrNoRows
}

func (s *fakeRoomhostStore) SaveCommandAck(context.Context, contracts.CommandAck) error {
	return nil
}

func (s *fakeRoomhostStore) AppendGameLogEvents(_ context.Context, events []contracts.GameLogEvent) error {
	s.operationEvents = append(s.operationEvents, events...)
	return nil
}

func (s *fakeRoomhostStore) SaveOutboundMessage(_ context.Context, out contracts.OutboundMessage) error {
	s.savedOutbound = append(s.savedOutbound, out)
	return s.outboundErr
}

func (s *fakeRoomhostStore) SyncMatchRoster(_ context.Context, matchID string, players []contracts.MatchPlayerRecord) error {
	s.rosterMatchID = matchID
	s.rosterPlayers = append([]contracts.MatchPlayerRecord(nil), players...)
	return nil
}

func (s *fakeRoomhostStore) ListMatchPlayers(context.Context, string) ([]contracts.MatchPlayerRecord, error) {
	if s.matchPlayersErr != nil {
		return nil, s.matchPlayersErr
	}
	return append([]contracts.MatchPlayerRecord(nil), s.matchPlayers...), nil
}

func (s *fakeRoomhostStore) StartHistoryGame(_ context.Context, game history.Game, _ []history.Player) (int64, error) {
	s.startedHistory = append(s.startedHistory, game)
	return int64(len(s.startedHistory) + 100), nil
}

func (s *fakeRoomhostStore) OpenHistoryGame(context.Context, string, string) (int64, int, error) {
	return 0, 0, sql.ErrNoRows
}

func (s *fakeRoomhostStore) FindHistoryGameByStart(
	_ context.Context,
	_ string,
	_ string,
	startedAt time.Time,
) (int64, int, bool, error) {
	s.historyLookup.calls = append(s.historyLookup.calls, startedAt)
	if s.historyLookup.err != nil {
		return 0, 0, false, s.historyLookup.err
	}
	if s.historyLookup.id == 0 {
		return 0, 0, false, sql.ErrNoRows
	}
	return s.historyLookup.id, s.historyLookup.nextSeq, s.historyLookup.finished, nil
}

func (s *fakeRoomhostStore) ResetOpenHistoryGame(context.Context, string, string) error {
	return nil
}

func (s *fakeRoomhostStore) AppendHistoryEvent(context.Context, history.Event) error {
	return nil
}

func (s *fakeRoomhostStore) FinishHistoryGame(_ context.Context, gameID int64, _ history.FinalizeInput) error {
	s.finishedHistory = append(s.finishedHistory, gameID)
	return nil
}

func (s *fakeRoomhostStore) DiscardHistoryGame(context.Context, int64) error {
	return nil
}

type fakeRoomhostCoordinator struct {
	ensureErr error
	verifyErr error
}

func (c fakeRoomhostCoordinator) EnsureRoomOwner(_ context.Context, rec contracts.RoomRecord, nodeID string, ttl time.Duration) (coordination.RoomOwner, error) {
	if c.ensureErr != nil {
		return coordination.RoomOwner{}, c.ensureErr
	}
	return coordination.RoomOwner{
		RoomID:         rec.RoomID,
		OwnerNode:      nodeID,
		Epoch:          rec.Epoch,
		LeaseExpiresAt: time.Now().UTC().Add(ttl),
	}, nil
}

func (c fakeRoomhostCoordinator) ClaimExpiredRoom(context.Context, contracts.RoomRecord, string, time.Duration) (coordination.RoomOwner, bool, error) {
	return coordination.RoomOwner{}, false, nil
}

func (c fakeRoomhostCoordinator) RenewRoomOwner(_ context.Context, roomID, nodeID string, epoch int64, ttl time.Duration) (coordination.RoomOwner, error) {
	return coordination.RoomOwner{
		RoomID:         roomID,
		OwnerNode:      nodeID,
		Epoch:          epoch,
		LeaseExpiresAt: time.Now().UTC().Add(ttl),
	}, nil
}

func (c fakeRoomhostCoordinator) VerifyRoomOwner(context.Context, string, string, int64) error {
	return c.verifyErr
}

func (c fakeRoomhostCoordinator) PublishRoomReady(context.Context, string, string, int64) error {
	return nil
}

func (c fakeRoomhostCoordinator) DeleteRoomOwner(context.Context, string, string, int64) error {
	return nil
}

type shutdownCoordinator struct {
	fakeRoomhostCoordinator
	deletedRoomID string
	deletedNodeID string
	deletedEpoch  int64
}

func (c *shutdownCoordinator) DeleteRoomOwner(_ context.Context, roomID, nodeID string, epoch int64) error {
	c.deletedRoomID = roomID
	c.deletedNodeID = nodeID
	c.deletedEpoch = epoch
	return nil
}

type fakeRoomhostRouter struct {
	afterSeq     int64
	logs         []contracts.CommandLogRecord
	listErr      error
	appendErr    error
	appendResult contracts.CommandLogAppendResult
	appended     []contracts.CommandLogRecord
	deliverErr   error
	delivered    []contracts.OutboundMessage
}

func (r *fakeRoomhostRouter) SubscribeRoomCommands(string, func(context.Context, contracts.RoomCommand) contracts.CommandAck) (*nats.Subscription, error) {
	return nil, nil
}

func (r *fakeRoomhostRouter) AppendCommandLog(rec contracts.CommandLogRecord) (contracts.CommandLogAppendResult, error) {
	if r.appendErr != nil {
		return contracts.CommandLogAppendResult{}, r.appendErr
	}
	r.appended = append(r.appended, rec)
	if r.appendResult.Seq == 0 {
		return contracts.CommandLogAppendResult{Seq: rec.Seq}, nil
	}
	return r.appendResult, nil
}

func (r *fakeRoomhostRouter) AppendOwnerFence(rec contracts.CommandLogRecord) (contracts.CommandLogAppendResult, error) {
	rec.Kind = contracts.CommandLogKindOwnerFence
	rec.Seq = 0
	for _, existing := range r.logs {
		if existing.MatchID == rec.MatchID && existing.Seq > rec.Seq {
			rec.Seq = existing.Seq
		}
	}
	r.logs = append(r.logs, rec)
	return contracts.CommandLogAppendResult{Seq: rec.Seq}, nil
}

func (r *fakeRoomhostRouter) ListCommandLog(matchID string, afterSeq int64) ([]contracts.CommandLogRecord, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	r.afterSeq = afterSeq
	out := []contracts.CommandLogRecord{}
	for _, rec := range r.logs {
		recordMatchID := rec.MatchID
		if recordMatchID == "" {
			recordMatchID = rec.RoomID
		}
		if recordMatchID == matchID &&
			(rec.Kind == contracts.CommandLogKindOwnerFence || rec.Seq > afterSeq) {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (r *fakeRoomhostRouter) Deliver(out contracts.OutboundMessage) error {
	r.delivered = append(r.delivered, out)
	return r.deliverErr
}

func TestBroadcastDoesNotPublishWhenReliablePersistenceFails(t *testing.T) {
	persistErr := errors.New("postgres unavailable")
	st := &fakeRoomhostStore{
		outboundErr: persistErr,
		presences: []contracts.PresenceRecord{{
			ConnectionRef: contracts.ConnectionRef{
				GatewayNode: "gateway-1",
				SessionID:   "session-1",
			},
			RoomID:   "room-1",
			UserID:   "user-1",
			PlayerID: "player-1",
			Online:   true,
		}},
	}
	router := &fakeRoomhostRouter{}
	engine := &roomhostTestEngine{gameType: "test-game", version: "test-v1"}
	host := NewHost("roomhost-1", st, nil, router, nil)
	actor := &RoomActor{
		host:   host,
		record: contracts.RoomRecord{RoomID: "room-1", GameType: "test-game"},
		definition: game.Definition{
			GameType: "test-game",
			Version:  "test-v1",
			Engine:   engine,
		},
		state: map[string]any{},
		seq:   3,
	}

	err := actor.broadcast(context.Background(), "trace-1")
	if !errors.Is(err, persistErr) {
		t.Fatalf("broadcast() error = %v, want %v", err, persistErr)
	}
	if len(st.savedOutbound) != 1 {
		t.Fatalf("saved outbound count = %d, want 1", len(st.savedOutbound))
	}
	if len(router.delivered) != 0 {
		t.Fatalf("delivered count = %d, want 0", len(router.delivered))
	}
}

func TestValidateGatewayCommandBindsSessionIdentity(t *testing.T) {
	session := contracts.SessionRecord{
		ConnectionRef: contracts.ConnectionRef{GatewayNode: "gateway-1", SessionID: "session-1"},
		UserID:        "user-1",
		PlayerID:      "player-1",
	}
	host := NewHost("roomhost-1", &fakeRoomhostStore{session: session}, nil, nil, nil)

	valid := contracts.RoomCommand{
		ConnectionRef: session.ConnectionRef,
		UserID:        session.UserID,
		PlayerID:      session.PlayerID,
	}
	if err := host.validateGatewayCommand(context.Background(), valid); err != nil {
		t.Fatalf("valid command rejected: %v", err)
	}

	mismatch := valid
	mismatch.UserID = "attacker"
	if err := host.validateGatewayCommand(context.Background(), mismatch); err == nil {
		t.Fatal("identity mismatch should be rejected")
	}

	missingConnection := contracts.RoomCommand{UserID: "user-1", PlayerID: "player-1"}
	if err := host.validateGatewayCommand(context.Background(), missingConnection); err == nil {
		t.Fatal("gateway command without connection reference should be rejected")
	}

	runtimeCommand := contracts.RoomCommand{Type: contracts.RuntimeCommandRoomClose}
	if err := host.validateCommandSource(context.Background(), runtimeCommand); err != nil {
		t.Fatalf("internal runtime command rejected: %v", err)
	}
	runtimeCommand.ConnectionRef = session.ConnectionRef
	if err := host.validateCommandSource(context.Background(), runtimeCommand); err == nil {
		t.Fatal("gateway session should not be allowed to issue runtime commands")
	}
}

func TestRuntimeRoomCreatePersistsInitialRandomState(t *testing.T) {
	engine := &roomhostTestEngine{gameType: "test-game", version: "test-v1"}
	st := &fakeRoomhostStore{room: contracts.RoomRecord{
		RoomID:         "room-1",
		GameType:       "test-game",
		Visibility:     contracts.VisibilityPrivate,
		OwnerPlayerID:  "alice-1",
		OwnerNode:      "roomhost-1",
		Epoch:          1,
		Status:         contracts.RoomStatusCreating,
		EngineVersion:  "test-v1",
		LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
	}}
	host := NewHost(
		"roomhost-1",
		st,
		fakeRoomhostCoordinator{},
		&fakeRoomhostRouter{},
		game.NewRegistry(game.Definition{GameType: "test-game", Version: "test-v1", Engine: engine}),
	)
	ack := host.HandleCommand(context.Background(), contracts.RoomCommand{
		CommandID: "create-1",
		RoomID:    "room-1",
		GameType:  "test-game",
		Epoch:     1,
		Type:      contracts.RuntimeCommandRoomCreate,
	})
	if !ack.Accepted {
		t.Fatalf("create ack = %+v", ack)
	}
	if engine.cfg.RandomSeed == 0 {
		t.Fatal("room actor received a zero random seed")
	}
	if engine.cfg.Visibility != contracts.VisibilityPrivate {
		t.Fatalf("room actor visibility = %q, want private", engine.cfg.Visibility)
	}
	if len(st.savedSnapshots) != 1 || st.savedSnapshots[0].Seq != 0 {
		t.Fatalf("initial snapshots = %+v", st.savedSnapshots)
	}
}

func TestStartMatchReplacesRuntimeInsideExistingRoomActor(t *testing.T) {
	engine := &roomhostTestEngine{
		gameType: "test-game",
		version:  "test-v1",
		roster: []game.RosterPlayer{
			{UserID: "1", PlayerID: "alice-1", Online: true},
			{UserID: "2", PlayerID: "bob-2", Online: true},
		},
	}
	st := &fakeRoomhostStore{
		room: contracts.RoomRecord{
			RoomID:         "room-1",
			GameType:       "test-game",
			OwnerPlayerID:  "alice-1",
			OwnerNode:      "roomhost-1",
			Epoch:          1,
			Status:         contracts.RoomStatusOpen,
			EngineVersion:  "test-v1",
			CurrentMatchID: "room-1-r1",
		},
		match: contracts.MatchRecord{
			MatchID:       "room-1-r1",
			RuntimeRoomID: "room-1",
			Status:        contracts.MatchStatusEnded,
			EngineVersion: "test-v1",
			RandomSeed:    1,
		},
		presences: []contracts.PresenceRecord{
			{RoomID: "room-1", UserID: "1", PlayerID: "alice-1", Online: true, ConnectionRef: contracts.ConnectionRef{GatewayNode: "gateway-1", SessionID: "s1"}},
			{RoomID: "room-1", UserID: "2", PlayerID: "bob-2", Online: true, ConnectionRef: contracts.ConnectionRef{GatewayNode: "gateway-1", SessionID: "s2"}},
		},
	}
	host := NewHost(
		"roomhost-1",
		st,
		fakeRoomhostCoordinator{},
		&fakeRoomhostRouter{},
		game.NewRegistry(game.Definition{GameType: "test-game", Version: "test-v1", Engine: engine}),
	)
	actor, err := host.newRoomActor(context.Background(), st.room)
	if err != nil {
		t.Fatal(err)
	}
	actor.seq = 8
	actor.processed["old-command"] = contracts.CommandAck{CommandID: "old-command", MatchID: "room-1-r1", Accepted: true, Seq: 8}
	st.match = contracts.MatchRecord{
		MatchID:       "room-1-r2",
		RuntimeRoomID: "room-1",
		Status:        contracts.MatchStatusPreparing,
		EngineVersion: "test-v1",
		RandomSeed:    2,
		RoundNo:       2,
	}

	if err := actor.StartMatch(context.Background(), "room-1-r2"); err != nil {
		t.Fatalf("StartMatch() error = %v", err)
	}
	if actor.match.MatchID != "room-1-r2" || actor.seq != 0 || actor.record.CurrentMatchID != "room-1-r2" {
		t.Fatalf("actor match boundary not reset: match=%+v seq=%d room=%+v", actor.match, actor.seq, actor.record)
	}
	if len(actor.processed) != 0 {
		t.Fatalf("old command idempotency cache leaked into new match: %+v", actor.processed)
	}
	lastSnapshot := st.savedSnapshots[len(st.savedSnapshots)-1]
	if lastSnapshot.MatchID != "room-1-r2" || lastSnapshot.Seq != 0 {
		t.Fatalf("new match initial snapshot = %+v", lastSnapshot)
	}
	if st.rosterMatchID != "room-1-r2" || len(st.rosterPlayers) != 2 {
		t.Fatalf("new match roster = %s/%+v", st.rosterMatchID, st.rosterPlayers)
	}
}

func TestHostShutdownSnapshotsAndReleasesRooms(t *testing.T) {
	st := &fakeRoomhostStore{}
	coord := &shutdownCoordinator{}
	engine := &roomhostTestEngine{gameType: "test-game", version: "test-v1"}
	host := NewHost("roomhost-1", st, coord, &fakeRoomhostRouter{}, nil)
	actor := &RoomActor{
		host: host,
		record: contracts.RoomRecord{
			RoomID:         "room-1",
			GameType:       "test-game",
			OwnerNode:      "roomhost-1",
			Epoch:          4,
			EngineVersion:  "test-v1",
			LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
		},
		definition: game.Definition{GameType: "test-game", Version: "test-v1", Engine: engine},
		state:      map[string]any{"turn": 2},
		seq:        9,
		processed:  map[string]contracts.CommandAck{},
	}
	host.rooms[actor.record.RoomID] = actor

	before := time.Now().UTC()
	if err := host.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if len(st.savedSnapshots) != 1 || st.savedSnapshots[0].Seq != 9 {
		t.Fatalf("saved snapshots = %+v", st.savedSnapshots)
	}
	if len(st.setRooms) != 1 || st.setRooms[0].LeaseExpiresAt.Before(before) {
		t.Fatalf("expired room projection = %+v", st.setRooms)
	}
	if coord.deletedRoomID != "room-1" || coord.deletedNodeID != "roomhost-1" || coord.deletedEpoch != 4 {
		t.Fatalf("released owner = %+v", coord)
	}
	ack := host.HandleCommand(context.Background(), contracts.RoomCommand{CommandID: "cmd-after-shutdown", RoomID: "room-1"})
	if ack.Accepted || ack.ErrorCode != "roomhost_draining" {
		t.Fatalf("post-shutdown ack = %+v", ack)
	}
}

func TestNewRoomActorInitializesStateThroughGamePlugin(t *testing.T) {
	engine := &roomhostTestEngine{gameType: "test-game", version: "test-v1"}
	host := NewHost("roomhost-a", &fakeRoomhostStore{}, nil, nil, game.NewRegistry(game.Definition{
		GameType:          "test-game",
		Version:           "test-v1",
		DefaultMaxPlayers: 3,
		Engine:            engine,
	}))
	rec := contracts.RoomRecord{
		RoomID:        "test-room-1",
		GameType:      "test-game",
		OwnerUserID:   "owner-user",
		OwnerPlayerID: "owner-player",
		MaxPlayers:    3,
	}

	actor, err := host.newRoomActor(context.Background(), rec)
	if err != nil {
		t.Fatalf("newRoomActor() error = %v", err)
	}
	if actor.definition.GameType != "test-game" || actor.definition.Version != "test-v1" {
		t.Fatalf("unexpected actor definition: %+v", actor.definition)
	}
	if actor.state == nil {
		t.Fatalf("actor state is nil")
	}
	if engine.cfg.RoomID != rec.RoomID || engine.cfg.GameType != rec.GameType {
		t.Fatalf("unexpected init room config: %+v", engine.cfg)
	}
	if engine.cfg.OwnerUserID != rec.OwnerUserID || engine.cfg.OwnerPlayerID != rec.OwnerPlayerID || engine.cfg.MaxPlayers != rec.MaxPlayers {
		t.Fatalf("unexpected init identity/config: %+v", engine.cfg)
	}
}

func TestSyncRosterUsesLogicalPlayersInsteadOfPresenceSessions(t *testing.T) {
	engine := &roomhostTestEngine{
		gameType: "roster",
		version:  "roster-v1",
		roster: []game.RosterPlayer{
			{UserID: "1", PlayerID: "alice-1", Online: true},
			{UserID: "ai_001", PlayerID: "ai_001", DisplayName: "ai_001", Online: true, IsAI: true},
		},
	}
	st := &fakeRoomhostStore{}
	host := NewHost("roomhost-a", st, fakeRoomhostCoordinator{}, &fakeRoomhostRouter{}, game.NewRegistry(game.Definition{
		GameType:          "roster",
		Version:           "roster-v1",
		DefaultMaxPlayers: 6,
		Engine:            engine,
	}))
	actor, err := host.newRoomActor(context.Background(), contracts.RoomRecord{
		RoomID:        "room-1",
		GameType:      "roster",
		OwnerNode:     "roomhost-a",
		OwnerPlayerID: "alice-1",
		MaxPlayers:    6,
	})
	if err != nil {
		t.Fatal(err)
	}
	presences := make([]contracts.PresenceRecord, 7)
	for index := range presences {
		presences[index] = contracts.PresenceRecord{RoomID: "room-1", PlayerID: "alice-1"}
	}
	if err := actor.syncRosterLocked(context.Background(), presences); err != nil {
		t.Fatal(err)
	}
	if actor.record.PlayerCount != 2 {
		t.Fatalf("player count = %d, want 2", actor.record.PlayerCount)
	}
	if st.rosterMatchID != "room-1" || len(st.rosterPlayers) != 2 || !st.rosterPlayers[1].IsAI {
		t.Fatalf("unexpected synced roster: match=%q players=%+v", st.rosterMatchID, st.rosterPlayers)
	}
}

func TestRoomActorRestoreReplaysCommandLogAfterLatestSnapshot(t *testing.T) {
	engine := &automationResetEngine{}
	snapshotState, err := json.Marshal(countingState{Count: 2})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	router := &fakeRoomhostRouter{
		logs: []contracts.CommandLogRecord{
			{RoomID: "room-1", Seq: 2, CommandID: "cmd-2", Epoch: 1, PlayerID: "alice", Type: "add", Payload: map[string]any{"delta": 100}},
			{RoomID: "room-1", Seq: 3, CommandID: "cmd-3", Epoch: 1, PlayerID: "alice", Type: "add", Payload: map[string]any{"delta": 1}},
			{RoomID: "room-1", Seq: 4, CommandID: "cmd-4", Epoch: 1, PlayerID: "bob", Type: "add", Payload: map[string]any{"delta": 5}},
			{Kind: contracts.CommandLogKindOwnerFence, RoomID: "room-1", Seq: 4, Epoch: 1, OwnerNode: "roomhost-a"},
		},
	}
	host := NewHost(
		"roomhost-a",
		&fakeRoomhostStore{snapshot: contracts.SnapshotRecord{RoomID: "room-1", Seq: 2, Epoch: 1, EngineVersion: "counting-v1", State: snapshotState}},
		fakeRoomhostCoordinator{},
		router,
		game.NewRegistry(game.Definition{GameType: "counting", Version: "counting-v1", Engine: engine}),
	)
	actor, err := host.newRoomActor(context.Background(), contracts.RoomRecord{
		RoomID:        "room-1",
		GameType:      "counting",
		OwnerNode:     "roomhost-a",
		Epoch:         1,
		EngineVersion: "counting-v1",
	})
	if err != nil {
		t.Fatalf("newRoomActor() error = %v", err)
	}

	if err := actor.restore(context.Background()); err != nil {
		t.Fatalf("restore() error = %v", err)
	}
	state := actor.state.(*countingState)
	if state.Count != 8 {
		t.Fatalf("restored count = %d, want 8", state.Count)
	}
	if actor.seq != 4 {
		t.Fatalf("restored seq = %d, want 4", actor.seq)
	}
	if router.afterSeq != 0 {
		t.Fatalf("last projection command log afterSeq = %d, want 0", router.afterSeq)
	}
	if len(engine.applied) != 2 || engine.applied[0].CommandID != "cmd-3" || engine.applied[1].CommandID != "cmd-4" {
		t.Fatalf("unexpected replayed commands: %+v", engine.applied)
	}
	if !engine.applied[0].Replay || !engine.applied[1].Replay {
		t.Fatalf("restored commands are not marked as replay: %+v", engine.applied)
	}
	if ack, ok := actor.processed["cmd-4"]; !ok || !ack.Accepted || ack.Seq != 4 {
		t.Fatalf("replayed command was not restored into idempotency cache: %+v", actor.processed)
	}
	if ack, ok := actor.processed["cmd-2"]; !ok || !ack.Accepted || ack.Seq != 2 {
		t.Fatalf("snapshot-covered command was not restored into idempotency cache: %+v", actor.processed)
	}
	if engine.resetCount != 1 {
		t.Fatalf("automation reset count = %d, want 1", engine.resetCount)
	}
}

func TestRoomActorPostEndCommandDoesNotReopenFinishedHistory(t *testing.T) {
	ctx := context.Background()
	plugin := &acquiregame.Plugin{}
	state, err := plugin.InitRoom(ctx, game.RoomConfig{
		RoomID:        "finished-history-room",
		GameType:      acquiregame.GameType,
		OwnerPlayerID: "1",
		MaxPlayers:    1,
		RandomSeed:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	room := state.(*acquiredomain.Room)
	room.Connections["1"] = &acquiredomain.PlayerConn{PlayerID: "1", Online: true, SeatIndex: 0}
	room.PlayerSeq = []string{"1"}
	room.State.Players["1"] = &acquiredomain.PlayerState{Money: 6000, Stocks: map[string]int{}}
	room.State.GameStartTime = time.Now().Add(-time.Minute)
	room.State.RoomStatus = acquiredomain.RoomStatusEnd

	st := &fakeRoomhostStore{}
	actor := &RoomActor{
		host:       &Host{store: st},
		record:     contracts.RoomRecord{RoomID: room.ID, GameType: acquiregame.GameType},
		match:      contracts.MatchRecord{MatchID: room.ID + "-r1"},
		definition: acquiregame.Definition(),
		state:      state,
		historyID:  42,
	}
	if err := actor.syncHistoryLocked(ctx, contracts.RoomCommand{
		RoomID:   room.ID,
		MatchID:  actor.match.MatchID,
		PlayerID: "1",
		Type:     "game_end_vote",
		Payload:  map[string]any{"allow": true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := actor.syncHistoryLocked(ctx, contracts.RoomCommand{
		RoomID:   room.ID,
		MatchID:  actor.match.MatchID,
		PlayerID: "1",
		Type:     "disconnect",
	}); err != nil {
		t.Fatal(err)
	}
	if len(st.startedHistory) != 0 {
		t.Fatalf("post-end command started %d duplicate histories, want 0", len(st.startedHistory))
	}
	if len(st.finishedHistory) != 1 || st.finishedHistory[0] != 42 {
		t.Fatalf("finished history IDs = %v, want [42]", st.finishedHistory)
	}
	if len(st.historyLookup.calls) != 0 {
		t.Fatalf("post-end command performed %d history identity lookups, want 0", len(st.historyLookup.calls))
	}
}

func TestRoomActorHistoryRebuildReusesFinishedGameByStart(t *testing.T) {
	startedAt := time.Date(2026, time.October, 23, 20, 52, 40, 0, time.UTC)
	st := &fakeRoomhostStore{
		historyLookup: fakeHistoryLookup{
			id:       598,
			nextSeq:  87,
			finished: true,
		},
	}
	actor := &RoomActor{
		host:           &Host{store: st},
		record:         contracts.RoomRecord{RoomID: "acquire-room", GameType: acquiregame.GameType},
		definition:     acquiregame.Definition(),
		historyRebuild: true,
	}
	projection := game.HistoryProjection{StartedAt: startedAt}

	if err := actor.ensureHistoryLocked(context.Background(), projection); err != nil {
		t.Fatal(err)
	}
	if actor.historyID != 598 {
		t.Fatalf("history ID = %d, want 598", actor.historyID)
	}
	if actor.historySeq != 0 {
		t.Fatalf("history seq = %d, want 0 for deterministic rebuild", actor.historySeq)
	}
	if len(st.startedHistory) != 0 {
		t.Fatalf("rebuild started %d duplicate histories, want 0", len(st.startedHistory))
	}
	if len(st.historyLookup.calls) != 1 || !st.historyLookup.calls[0].Equal(startedAt) {
		t.Fatalf("history identity lookups = %v, want [%v]", st.historyLookup.calls, startedAt)
	}
}

func TestRoomActorRejectsCommandWhenCommandLogIsUnavailable(t *testing.T) {
	engine := &countingEngine{}
	router := &fakeRoomhostRouter{appendErr: errors.New("jetstream unavailable")}
	host := NewHost(
		"roomhost-a",
		&fakeRoomhostStore{},
		fakeRoomhostCoordinator{},
		router,
		game.NewRegistry(game.Definition{GameType: "counting", Version: "counting-v1", Engine: engine}),
	)
	actor, err := host.newRoomActor(context.Background(), contracts.RoomRecord{
		RoomID:        "room-1",
		GameType:      "counting",
		OwnerNode:     "roomhost-a",
		Epoch:         1,
		EngineVersion: "counting-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer actor.shutdown()

	ack := actor.Apply(context.Background(), contracts.RoomCommand{
		CommandID: "cmd-1",
		RoomID:    "room-1",
		GameType:  "counting",
		Epoch:     1,
		PlayerID:  "alice",
		Type:      "add",
		Payload:   map[string]any{"delta": 5},
	})
	if ack.Accepted || ack.ErrorCode != "command_log_unavailable" {
		t.Fatalf("ack = %+v, want command_log_unavailable", ack)
	}
	if actor.seq != 0 || actor.state.(*countingState).Count != 0 {
		t.Fatalf("state committed without command log: seq=%d state=%+v", actor.seq, actor.state)
	}
	if _, ok := actor.processed["cmd-1"]; ok {
		t.Fatal("retryable command-log failure was cached as processed")
	}
}

func TestRoomActorHandlesDuplicateCommandLogAppend(t *testing.T) {
	newActor := func(router *fakeRoomhostRouter) *RoomActor {
		t.Helper()
		host := NewHost(
			"roomhost-a",
			&fakeRoomhostStore{},
			fakeRoomhostCoordinator{},
			router,
			game.NewRegistry(game.Definition{GameType: "counting", Version: "counting-v1", Engine: &countingEngine{}}),
		)
		actor, err := host.newRoomActor(context.Background(), contracts.RoomRecord{
			RoomID:         "room-1",
			GameType:       "counting",
			OwnerNode:      "roomhost-a",
			Epoch:          1,
			EngineVersion:  "counting-v1",
			LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(actor.shutdown)
		return actor
	}
	command := contracts.RoomCommand{
		CommandID: "cmd-1",
		RoomID:    "room-1",
		GameType:  "counting",
		Epoch:     1,
		PlayerID:  "alice",
		Type:      "add",
		Payload:   map[string]any{"delta": 5},
	}

	t.Run("duplicate at next seq commits staged state", func(t *testing.T) {
		actor := newActor(&fakeRoomhostRouter{
			appendResult: contracts.CommandLogAppendResult{Seq: 1, Duplicate: true},
		})
		ack := actor.Apply(context.Background(), command)
		if !ack.Accepted || ack.Seq != 1 || actor.seq != 1 || actor.state.(*countingState).Count != 5 {
			t.Fatalf("unexpected duplicate recovery result: ack=%+v seq=%d state=%+v", ack, actor.seq, actor.state)
		}
	})

	t.Run("duplicate at old seq does not apply again", func(t *testing.T) {
		actor := newActor(&fakeRoomhostRouter{
			appendResult: contracts.CommandLogAppendResult{Seq: 1, Duplicate: true},
		})
		actor.state = &countingState{Count: 5}
		actor.seq = 2
		ack := actor.Apply(context.Background(), command)
		if !ack.Accepted || ack.Seq != 1 || actor.seq != 2 || actor.state.(*countingState).Count != 5 {
			t.Fatalf("duplicate command changed live state: ack=%+v seq=%d state=%+v", ack, actor.seq, actor.state)
		}
	})
}

func TestRoomActorRejectsCommandIDWithDifferentIntent(t *testing.T) {
	host := NewHost(
		"roomhost-a",
		&fakeRoomhostStore{},
		fakeRoomhostCoordinator{},
		&fakeRoomhostRouter{},
		game.NewRegistry(game.Definition{GameType: "counting", Version: "counting-v1", Engine: &countingEngine{}}),
	)
	actor, err := host.newRoomActor(context.Background(), contracts.RoomRecord{
		RoomID:        "room-1",
		GameType:      "counting",
		OwnerNode:     "roomhost-a",
		Epoch:         1,
		EngineVersion: "counting-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer actor.shutdown()

	original := contracts.RoomCommand{
		CommandID: "cmd-1",
		RoomID:    "room-1",
		MatchID:   "room-1",
		PlayerID:  "alice",
		Type:      "add",
		Payload:   map[string]any{"delta": 1},
	}
	intentHash, err := commandIntentHash(original)
	if err != nil {
		t.Fatal(err)
	}
	actor.processed[original.CommandID] = contracts.CommandAck{
		CommandID:  original.CommandID,
		RoomID:     original.RoomID,
		MatchID:    original.MatchID,
		IntentHash: intentHash,
		Accepted:   true,
		Seq:        1,
	}
	reused := original
	reused.Epoch = 1
	reused.Payload = map[string]any{"delta": 2}
	ack := actor.Apply(context.Background(), reused)
	if ack.Accepted || ack.ErrorCode != "command_id_reused" {
		t.Fatalf("ack = %+v, want command_id_reused", ack)
	}
}

func TestRoomActorPersistsPluginOperationEventsWithCommandLog(t *testing.T) {
	engine := &loggingEngine{}
	st := &fakeRoomhostStore{}
	router := &fakeRoomhostRouter{}
	host := NewHost(
		"roomhost-a",
		st,
		fakeRoomhostCoordinator{},
		router,
		game.NewRegistry(game.Definition{GameType: "logging", Version: "logging-v1", Engine: engine}),
	)
	actor, err := host.newRoomActor(context.Background(), contracts.RoomRecord{
		RoomID:        "room-1",
		GameType:      "logging",
		OwnerNode:     "roomhost-a",
		Epoch:         1,
		EngineVersion: "logging-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer actor.shutdown()

	ack := actor.Apply(context.Background(), contracts.RoomCommand{
		CommandID: "cmd-1",
		RoomID:    "room-1",
		GameType:  "logging",
		Epoch:     1,
		PlayerID:  "alice",
		Type:      "add",
		Payload:   map[string]any{"delta": 1},
	})
	if !ack.Accepted {
		t.Fatalf("ack = %+v", ack)
	}
	if len(router.appended) != 1 || len(router.appended[0].Operations) != 1 {
		t.Fatalf("command log operations = %+v", router.appended)
	}
	if len(st.operationEvents) != 1 {
		t.Fatalf("persisted operation events = %+v", st.operationEvents)
	}
	event := st.operationEvents[0]
	if event.EventID != "room-1:1:0" || event.Type != "count_changed" || event.Visibility != string(game.LogPublic) {
		t.Fatalf("unexpected operation event: %+v", event)
	}
	if len(st.savedSpectator) != 1 {
		t.Fatalf("saved spectator snapshots = %+v", st.savedSpectator)
	}
	spectator := st.savedSpectator[0]
	if spectator.Seq != 1 || spectator.AvailableAt.Sub(spectator.CapturedAt) != contracts.SpectatorDelay {
		t.Fatalf("unexpected spectator snapshot: %+v", spectator)
	}
}

func TestRoomActorRejectsCommandAfterOwnershipIsLost(t *testing.T) {
	router := &fakeRoomhostRouter{}
	host := NewHost(
		"roomhost-a",
		&fakeRoomhostStore{},
		fakeRoomhostCoordinator{verifyErr: coordination.ErrNotOwner},
		router,
		game.NewRegistry(game.Definition{GameType: "counting", Version: "counting-v1", Engine: &countingEngine{}}),
	)
	actor, err := host.newRoomActor(context.Background(), contracts.RoomRecord{
		RoomID:        "room-1",
		GameType:      "counting",
		OwnerNode:     "roomhost-a",
		Epoch:         1,
		EngineVersion: "counting-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer actor.shutdown()

	ack := actor.Apply(context.Background(), contracts.RoomCommand{
		CommandID: "cmd-1",
		RoomID:    "room-1",
		GameType:  "counting",
		Epoch:     1,
		PlayerID:  "alice",
		Type:      "add",
		Payload:   map[string]any{"delta": 5},
	})
	if ack.Accepted || ack.ErrorCode != "stale_epoch" {
		t.Fatalf("ack = %+v, want stale_epoch", ack)
	}
	if len(router.appended) != 0 || actor.seq != 0 {
		t.Fatalf("lost owner committed command: appended=%+v seq=%d", router.appended, actor.seq)
	}
}

func TestRoomActorFailsClosedOnCommandLogHeadConflict(t *testing.T) {
	router := &fakeRoomhostRouter{appendErr: transport.ErrCommandLogSequenceConflict}
	host := NewHost(
		"roomhost-a",
		&fakeRoomhostStore{},
		fakeRoomhostCoordinator{},
		router,
		game.NewRegistry(game.Definition{GameType: "counting", Version: "counting-v1", Engine: &countingEngine{}}),
	)
	actor, err := host.newRoomActor(context.Background(), contracts.RoomRecord{
		RoomID:        "room-1",
		GameType:      "counting",
		OwnerNode:     "roomhost-a",
		Epoch:         1,
		EngineVersion: "counting-v1",
	})
	if err != nil {
		t.Fatal(err)
	}

	ack := actor.Apply(context.Background(), contracts.RoomCommand{
		CommandID: "cmd-1",
		RoomID:    "room-1",
		MatchID:   "room-1",
		GameType:  "counting",
		Epoch:     1,
		PlayerID:  "alice",
		Type:      "add",
		Payload:   map[string]any{"delta": 5},
	})
	if ack.Accepted || ack.ErrorCode != "command_log_unavailable" {
		t.Fatalf("ack = %+v, want retryable command log failure", ack)
	}
	if !actor.closed || actor.seq != 0 || actor.state.(*countingState).Count != 0 {
		t.Fatalf("actor did not fail closed: closed=%v seq=%d state=%+v", actor.closed, actor.seq, actor.state)
	}
}

func TestHostRoomFailsClosedWhenRestoreFails(t *testing.T) {
	restoreErr := errors.New("snapshot unavailable")
	st := &fakeRoomhostStore{
		room: contracts.RoomRecord{
			RoomID:         "room-1",
			GameType:       "counting",
			OwnerNode:      "roomhost-a",
			Epoch:          1,
			EngineVersion:  "counting-v1",
			LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
		},
		snapshotErr: restoreErr,
	}
	host := NewHost(
		"roomhost-a",
		st,
		fakeRoomhostCoordinator{},
		&fakeRoomhostRouter{},
		game.NewRegistry(game.Definition{GameType: "counting", Version: "counting-v1", Engine: &countingEngine{}}),
	)

	_, err := host.room(context.Background(), contracts.RoomCommand{RoomID: "room-1"})
	if !errors.Is(err, restoreErr) {
		t.Fatalf("room() error = %v, want restore error", err)
	}
	if _, loaded := host.rooms["room-1"]; loaded {
		t.Fatal("room with failed recovery was registered")
	}
}

func TestHostRoomRejectsTerminalRoom(t *testing.T) {
	host := NewHost(
		"roomhost-a",
		&fakeRoomhostStore{room: contracts.RoomRecord{
			RoomID:    "room-1",
			GameType:  "counting",
			OwnerNode: "roomhost-a",
			Epoch:     1,
			Status:    contracts.RoomStatusEnded,
		}},
		fakeRoomhostCoordinator{},
		&fakeRoomhostRouter{},
		game.NewRegistry(game.Definition{GameType: "counting", Version: "counting-v1", Engine: &countingEngine{}}),
	)
	if _, err := host.room(context.Background(), contracts.RoomCommand{RoomID: "room-1"}); err == nil {
		t.Fatal("terminal room was lazily loaded")
	}
}

func TestShouldRecover(t *testing.T) {
	presenceErr := errors.New("presence unavailable")
	tests := []struct {
		name        string
		room        contracts.RoomRecord
		match       contracts.MatchRecord
		presences   []contracts.PresenceRecord
		presenceErr error
		want        bool
	}{
		{
			name: "empty open room",
			room: contracts.RoomRecord{RoomID: "empty-open-room", Status: contracts.RoomStatusOpen},
			want: true,
		},
		{
			name: "terminal room",
			room: contracts.RoomRecord{RoomID: "closed-room", Status: contracts.RoomStatusClosed},
		},
		{
			name:  "terminal current match",
			room:  contracts.RoomRecord{RoomID: "ended-match-room", CurrentMatchID: "match-1", Status: contracts.RoomStatusOpen, PlayerCount: 2},
			match: contracts.MatchRecord{MatchID: "match-1", Status: contracts.MatchStatusEnded},
		},
		{
			name: "active player count",
			room: contracts.RoomRecord{RoomID: "active-room", Status: contracts.RoomStatusCreating, PlayerCount: 1},
			want: true,
		},
		{
			name:      "online presence",
			room:      contracts.RoomRecord{RoomID: "online-room", Status: contracts.RoomStatusCreating},
			presences: []contracts.PresenceRecord{{Online: true}},
			want:      true,
		},
		{
			name:        "presence unavailable",
			room:        contracts.RoomRecord{RoomID: "unknown-room", Status: contracts.RoomStatusOpen},
			presenceErr: presenceErr,
		},
		{
			name: "empty non-open room",
			room: contracts.RoomRecord{RoomID: "creating-room", Status: contracts.RoomStatusCreating},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := NewHost(
				"roomhost-a",
				&fakeRoomhostStore{match: tt.match, presences: tt.presences, presenceErr: tt.presenceErr},
				fakeRoomhostCoordinator{},
				&fakeRoomhostRouter{},
				nil,
			)
			if got := host.shouldRecover(context.Background(), tt.room); got != tt.want {
				t.Fatalf("shouldRecover() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRoomActorCloseContinuesWhenHistoryReconciliationFails(t *testing.T) {
	ctx := context.Background()
	state, err := (&acquiregame.Plugin{}).InitRoom(ctx, game.RoomConfig{
		RoomID:        "history-dirty-room",
		GameType:      acquiregame.GameType,
		OwnerPlayerID: "1",
		MaxPlayers:    2,
		RandomSeed:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	st := &fakeRoomhostStore{}
	actor := &RoomActor{
		host: &Host{
			nodeID: "roomhost-a",
			store:  st,
			router: &fakeRoomhostRouter{listErr: errors.New("history replay unavailable")},
		},
		record: contracts.RoomRecord{
			RoomID:    "history-dirty-room",
			GameType:  acquiregame.GameType,
			Status:    contracts.RoomStatusOpen,
			OwnerNode: "roomhost-a",
			Epoch:     1,
		},
		match:        contracts.MatchRecord{MatchID: "history-dirty-room-r1", Status: contracts.MatchStatusEnded},
		definition:   acquiregame.Definition(),
		state:        state,
		processed:    map[string]contracts.CommandAck{},
		historyDirty: true,
	}

	ack := actor.Close(ctx, contracts.RoomCommand{
		CommandID: "close-empty-room",
		RoomID:    actor.record.RoomID,
		MatchID:   actor.match.MatchID,
		Epoch:     actor.record.Epoch,
		Type:      contracts.RuntimeCommandRoomClose,
		Payload:   map[string]any{"status": contracts.RoomStatusClosed},
	})

	if !ack.Accepted {
		t.Fatalf("close ack = %+v, want accepted", ack)
	}
	if len(st.setRooms) != 1 || st.setRooms[0].Status != contracts.RoomStatusClosed {
		t.Fatalf("persisted rooms = %+v, want one closed room", st.setRooms)
	}
	if !actor.closed {
		t.Fatal("actor remained open after lifecycle close")
	}
}

func TestBootstrapOwnedRoomsSkipsTerminalCurrentMatch(t *testing.T) {
	st := &fakeRoomhostStore{
		room: contracts.RoomRecord{
			RoomID:         "ended-match-room",
			CurrentMatchID: "match-1",
			OwnerNode:      "roomhost-a",
			Epoch:          7,
			Status:         contracts.RoomStatusOpen,
			PlayerCount:    2,
			LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
		},
		match: contracts.MatchRecord{
			MatchID: "match-1",
			Status:  contracts.MatchStatusEnded,
		},
	}
	router := &fakeRoomhostRouter{}
	host := NewHost("roomhost-a", st, fakeRoomhostCoordinator{}, router, nil)

	host.bootstrapOwnedRooms(context.Background())

	if host.ActiveRooms() != 0 {
		t.Fatalf("active rooms = %d, want 0", host.ActiveRooms())
	}
	if len(router.logs) != 0 {
		t.Fatalf("owner fences = %d, want 0", len(router.logs))
	}
}

func TestCleanupRoomsClosesExpiredEmptyRoom(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeRoomhostStore{}
	coord := &shutdownCoordinator{}
	engine := &roomhostTestEngine{gameType: "test-game", version: "test-v1"}
	host := NewHost("roomhost-a", st, coord, &fakeRoomhostRouter{}, nil)
	actor := &RoomActor{
		host: host,
		record: contracts.RoomRecord{
			RoomID:        "empty-open-room",
			GameType:      "test-game",
			OwnerNode:     "roomhost-a",
			Epoch:         7,
			Status:        contracts.RoomStatusOpen,
			EngineVersion: "test-v1",
		},
		definition: game.Definition{GameType: "test-game", Version: "test-v1", Engine: engine},
		state:      map[string]any{},
		processed:  map[string]contracts.CommandAck{},
	}
	host.rooms[actor.record.RoomID] = actor
	host.emptySince[actor.record.RoomID] = now.Add(-3 * time.Minute)

	host.cleanupRooms(context.Background(), now)

	if host.ActiveRooms() != 0 {
		t.Fatalf("active rooms = %d, want 0", host.ActiveRooms())
	}
	if _, tracked := host.emptySince[actor.record.RoomID]; tracked {
		t.Fatal("empty room remained in cleanup tracking")
	}
	if coord.deletedRoomID != actor.record.RoomID || coord.deletedNodeID != host.nodeID || coord.deletedEpoch != actor.record.Epoch {
		t.Fatalf("released owner = %+v", coord)
	}
	if len(st.setRooms) != 1 || st.setRooms[0].Status != contracts.RoomStatusClosed {
		t.Fatalf("persisted rooms = %+v, want one closed room record", st.setRooms)
	}
	if !actor.closed {
		t.Fatal("room actor was not closed")
	}
}

func TestCleanupRoomsClosesAcquireAfterThreeChecksWhenHistoryDirty(t *testing.T) {
	ctx := context.Background()
	state, err := (&acquiregame.Plugin{}).InitRoom(ctx, game.RoomConfig{
		RoomID:        "history-dirty-acquire-room",
		GameType:      acquiregame.GameType,
		OwnerPlayerID: "1",
		MaxPlayers:    2,
		RandomSeed:    1,
	})
	if err != nil {
		t.Fatal(err)
	}
	st := &fakeRoomhostStore{}
	coord := &shutdownCoordinator{}
	host := NewHost(
		"roomhost-a",
		st,
		coord,
		&fakeRoomhostRouter{listErr: errors.New("history replay unavailable")},
		nil,
	)
	actor := &RoomActor{
		host: host,
		record: contracts.RoomRecord{
			RoomID:    "history-dirty-acquire-room",
			GameType:  acquiregame.GameType,
			Status:    contracts.RoomStatusOpen,
			OwnerNode: "roomhost-a",
			Epoch:     1,
		},
		match:        contracts.MatchRecord{MatchID: "history-dirty-acquire-room-r1", Status: contracts.MatchStatusEnded},
		definition:   acquiregame.Definition(),
		state:        state,
		processed:    map[string]contracts.CommandAck{},
		historyDirty: true,
	}
	host.rooms[actor.record.RoomID] = actor
	startedAt := time.Now().UTC()

	for check := 1; check <= 3; check++ {
		host.cleanupRooms(ctx, startedAt.Add(time.Duration(check)*time.Minute))
		if check < 3 && host.ActiveRooms() != 1 {
			t.Fatalf("active rooms after check %d = %d, want 1", check, host.ActiveRooms())
		}
	}

	if host.ActiveRooms() != 0 {
		t.Fatalf("active rooms after third check = %d, want 0", host.ActiveRooms())
	}
	if len(st.setRooms) != 1 || st.setRooms[0].Status != contracts.RoomStatusClosed {
		t.Fatalf("persisted rooms = %+v, want one closed room", st.setRooms)
	}
	if coord.deletedRoomID != actor.record.RoomID {
		t.Fatalf("deleted owner room = %q, want %q", coord.deletedRoomID, actor.record.RoomID)
	}
}

func TestCloseOrphanedEmptyRoom(t *testing.T) {
	st := &fakeRoomhostStore{}
	coord := &shutdownCoordinator{}
	host := NewHost("roomhost-a", st, coord, &fakeRoomhostRouter{}, nil)
	room := contracts.RoomRecord{
		RoomID:      "orphaned-open-room",
		OwnerNode:   "roomhost-a",
		Epoch:       9,
		Status:      contracts.RoomStatusOpen,
		PlayerCount: 0,
	}

	closed, err := host.closeOrphanedEmptyRoom(context.Background(), room)
	if err != nil {
		t.Fatalf("closeOrphanedEmptyRoom() error = %v", err)
	}
	if !closed {
		t.Fatal("orphaned empty room was not closed")
	}
	if len(st.setRooms) != 1 || st.setRooms[0].Status != contracts.RoomStatusClosed {
		t.Fatalf("persisted rooms = %+v, want one closed room record", st.setRooms)
	}
	if coord.deletedRoomID != room.RoomID || coord.deletedNodeID != host.nodeID || coord.deletedEpoch != room.Epoch {
		t.Fatalf("released owner = %+v", coord)
	}
}

func TestCloseOrphanedEmptyRoomRejectsUnsafeCandidates(t *testing.T) {
	presenceErr := errors.New("presence unavailable")
	tests := []struct {
		name        string
		room        contracts.RoomRecord
		presences   []contracts.PresenceRecord
		presenceErr error
		wantErr     bool
	}{
		{
			name: "non-open room",
			room: contracts.RoomRecord{RoomID: "creating-room", Status: contracts.RoomStatusCreating},
		},
		{
			name: "room with projected players",
			room: contracts.RoomRecord{RoomID: "occupied-room", Status: contracts.RoomStatusOpen, PlayerCount: 1},
		},
		{
			name:      "room with online presence",
			room:      contracts.RoomRecord{RoomID: "online-room", Status: contracts.RoomStatusOpen},
			presences: []contracts.PresenceRecord{{Online: true}},
		},
		{
			name:        "presence unavailable",
			room:        contracts.RoomRecord{RoomID: "unknown-room", Status: contracts.RoomStatusOpen},
			presenceErr: presenceErr,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := &fakeRoomhostStore{presences: tt.presences, presenceErr: tt.presenceErr}
			coord := &shutdownCoordinator{}
			host := NewHost("roomhost-a", st, coord, &fakeRoomhostRouter{}, nil)

			closed, err := host.closeOrphanedEmptyRoom(context.Background(), tt.room)
			if closed {
				t.Fatal("unsafe orphan candidate was closed")
			}
			if (err != nil) != tt.wantErr {
				t.Fatalf("closeOrphanedEmptyRoom() error = %v, wantErr %v", err, tt.wantErr)
			}
			if len(st.setRooms) != 0 || coord.deletedRoomID != "" {
				t.Fatalf("unsafe candidate mutated: rooms=%+v owner=%+v", st.setRooms, coord)
			}
		})
	}
}

func TestRoomActorRejectsEngineVersionMismatch(t *testing.T) {
	host := NewHost(
		"roomhost-a",
		&fakeRoomhostStore{},
		fakeRoomhostCoordinator{},
		&fakeRoomhostRouter{},
		game.NewRegistry(game.Definition{GameType: "counting", Version: "counting-v2", Engine: &countingEngine{}}),
	)
	_, err := host.newRoomActor(context.Background(), contracts.RoomRecord{
		RoomID:        "room-1",
		GameType:      "counting",
		EngineVersion: "counting-v1",
	})
	if !errors.Is(err, ErrEngineVersionMismatch) {
		t.Fatalf("newRoomActor() error = %v, want ErrEngineVersionMismatch", err)
	}
}

func TestRoomActorRestoreRejectsCommandLogGap(t *testing.T) {
	engine := &countingEngine{}
	snapshotState, err := json.Marshal(countingState{Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	router := &fakeRoomhostRouter{logs: []contracts.CommandLogRecord{
		{RoomID: "room-1", Seq: 4, CommandID: "cmd-4", Type: "add", Payload: map[string]any{"delta": 1}},
	}}
	host := NewHost(
		"roomhost-a",
		&fakeRoomhostStore{snapshot: contracts.SnapshotRecord{RoomID: "room-1", Seq: 2, EngineVersion: "counting-v1", State: snapshotState}},
		fakeRoomhostCoordinator{},
		router,
		game.NewRegistry(game.Definition{GameType: "counting", Version: "counting-v1", Engine: engine}),
	)
	actor, err := host.newRoomActor(context.Background(), contracts.RoomRecord{
		RoomID:        "room-1",
		GameType:      "counting",
		EngineVersion: "counting-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer actor.shutdown()
	if err := actor.restore(context.Background()); err == nil || !strings.Contains(err.Error(), "command log gap") {
		t.Fatalf("restore() error = %v, want command log gap", err)
	}
}

func TestRoomActorRestorePreservesAcquireRandomTileState(t *testing.T) {
	ctx := context.Background()
	plugin := &acquiregame.Plugin{}
	state, err := plugin.InitRoom(ctx, game.RoomConfig{
		RoomID:        "acquire-random-restore",
		GameType:      acquiregame.GameType,
		OwnerPlayerID: "alice-1",
		MaxPlayers:    2,
		RandomSeed:    0x123456789abcdef0,
	})
	if err != nil {
		t.Fatal(err)
	}
	baseTime := time.Date(2100, time.January, 2, 3, 4, 5, 0, time.UTC)
	prefix := []contracts.RoomCommand{
		{CommandID: "cmd-1", RoomID: "acquire-random-restore", PlayerID: "alice-1", Type: "connect"},
		{CommandID: "cmd-2", RoomID: "acquire-random-restore", PlayerID: "bob-2", Type: "connect"},
		{CommandID: "cmd-3", RoomID: "acquire-random-restore", PlayerID: "bob-2", Type: "match_ready", Payload: map[string]any{"ready": true}},
		{CommandID: "cmd-4", RoomID: "acquire-random-restore", PlayerID: "alice-1", Type: "match_begin"},
		{CommandID: "cmd-5", RoomID: "acquire-random-restore", PlayerID: "alice-1", Type: "game_ready"},
	}
	for index := range prefix {
		prefix[index].SentAt = baseTime.Add(time.Duration(index) * time.Second)
		cmd := prefix[index]
		if _, err := plugin.ApplyCommand(ctx, state, cmd); err != nil {
			t.Fatalf("apply %s: %v", cmd.Type, err)
		}
	}
	snapshotState, err := plugin.EncodeState(ctx, state)
	if err != nil {
		t.Fatal(err)
	}

	room := state.(*acquiredomain.Room)
	alicePlace := contracts.RoomCommand{
		CommandID: "cmd-6",
		RoomID:    "acquire-random-restore",
		PlayerID:  "alice-1",
		Type:      "game_place_tile",
		Payload:   map[string]any{"tileKey": room.State.Players["alice-1"].Tiles[0]},
		SentAt:    baseTime.Add(5 * time.Second),
	}
	if _, err := plugin.ApplyCommand(ctx, state, alicePlace); err != nil {
		t.Fatal(err)
	}
	bobPlace := contracts.RoomCommand{
		CommandID: "cmd-7",
		RoomID:    "acquire-random-restore",
		PlayerID:  "bob-2",
		Type:      "game_place_tile",
		Payload:   map[string]any{"tileKey": room.State.Players["bob-2"].Tiles[0]},
		SentAt:    baseTime.Add(6 * time.Second),
	}
	if _, err := plugin.ApplyCommand(ctx, state, bobPlace); err != nil {
		t.Fatal(err)
	}
	expected := normalizedAcquireRestoreState(t, plugin, state)

	router := &fakeRoomhostRouter{logs: []contracts.CommandLogRecord{
		{RoomID: "acquire-random-restore", Seq: 5, CommandID: "cmd-5-duplicate", Epoch: 1, PlayerID: "alice-1", Type: "game_ready", CreatedAt: prefix[4].SentAt},
		{RoomID: "acquire-random-restore", Seq: 6, CommandID: alicePlace.CommandID, Epoch: 1, PlayerID: alicePlace.PlayerID, Type: alicePlace.Type, Payload: alicePlace.Payload, CreatedAt: alicePlace.SentAt},
		{RoomID: "acquire-random-restore", Seq: 7, CommandID: bobPlace.CommandID, Epoch: 1, PlayerID: bobPlace.PlayerID, Type: bobPlace.Type, Payload: bobPlace.Payload, CreatedAt: bobPlace.SentAt},
		{Kind: contracts.CommandLogKindOwnerFence, RoomID: "acquire-random-restore", Seq: 7, Epoch: 2, OwnerNode: "roomhost-b"},
	}}
	host := NewHost(
		"roomhost-b",
		&fakeRoomhostStore{snapshot: contracts.SnapshotRecord{
			RoomID:        "acquire-random-restore",
			Seq:           5,
			Epoch:         1,
			EngineVersion: acquiregame.EngineVersion,
			State:         snapshotState,
		}},
		fakeRoomhostCoordinator{},
		router,
		game.NewRegistry(acquiregame.Definition()),
	)
	actor, err := host.newRoomActor(ctx, contracts.RoomRecord{
		RoomID:        "acquire-random-restore",
		GameType:      acquiregame.GameType,
		OwnerNode:     "roomhost-b",
		Epoch:         2,
		EngineVersion: acquiregame.EngineVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer actor.shutdown()

	if err := actor.restore(ctx); err != nil {
		t.Fatalf("restore() error = %v", err)
	}
	if actor.seq != 7 || router.afterSeq != 0 {
		t.Fatalf("restore seq/last projection afterSeq = %d/%d, want 7/0", actor.seq, router.afterSeq)
	}
	actor.mu.Lock()
	restored := normalizedAcquireRestoreState(t, plugin, actor.state)
	actor.mu.Unlock()
	if !bytes.Equal(restored, expected) {
		t.Fatalf("restored Acquire state differs\n got: %s\nwant: %s", restored, expected)
	}
	restoredRoom := actor.state.(*acquiredomain.Room)
	if restoredRoom.State.RandomCounter != 4 {
		t.Fatalf("restored random counter = %d, want 4", restoredRoom.State.RandomCounter)
	}
}

func normalizedAcquireRestoreState(t *testing.T, plugin *acquiregame.Plugin, state game.State) []byte {
	t.Helper()
	normalized, err := plugin.EncodeState(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	return normalized
}
