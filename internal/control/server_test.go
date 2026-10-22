package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/authclient"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/coordination"
	"github.com/nciyuan9264/game-platform/internal/davinci"
	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/store"
)

type adminTokenVerifier struct{}

func (adminTokenVerifier) VerifyToken(context.Context, authclient.VerifyTokenRequest) (authclient.VerifyTokenResponse, error) {
	return authclient.VerifyTokenResponse{
		Valid: true,
		User:  contracts.UserIdentity{UserID: "admin", Roles: []string{"admin"}},
	}, nil
}

type playerTokenVerifier struct{}

func (playerTokenVerifier) VerifyToken(context.Context, authclient.VerifyTokenRequest) (authclient.VerifyTokenResponse, error) {
	return authclient.VerifyTokenResponse{
		Valid: true,
		User:  contracts.UserIdentity{UserID: "player", Roles: []string{"player"}},
	}, nil
}

type failingTokenVerifier struct{}

func (failingTokenVerifier) VerifyToken(context.Context, authclient.VerifyTokenRequest) (authclient.VerifyTokenResponse, error) {
	return authclient.VerifyTokenResponse{}, errors.New("auth rpc unavailable")
}

func TestAdminRoutesRequireAdminRole(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		verifier   authclient.TokenVerifier
		wantStatus int
	}{
		{name: "missing token", verifier: adminTokenVerifier{}, wantStatus: http.StatusUnauthorized},
		{name: "player forbidden", token: "player-token", verifier: playerTokenVerifier{}, wantStatus: http.StatusForbidden},
		{name: "auth unavailable", token: "admin-token", verifier: failingTokenVerifier{}, wantStatus: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewServerWithConfig(nil, nil, Config{AdminAuth: tt.verifier})
			req := httptest.NewRequest(http.MethodGet, "/platform/topology", nil)
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			rec := httptest.NewRecorder()
			server.Routes().ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

type recordingRoomCommandSender struct {
	ownerNode string
	cmd       contracts.RoomCommand
	timeout   time.Duration
	ack       contracts.CommandAck
	hasAck    bool
	err       error
}

func (s *recordingRoomCommandSender) SendRoomCommand(ctx context.Context, ownerNode string, cmd contracts.RoomCommand, timeout time.Duration) (contracts.CommandAck, error) {
	s.ownerNode = ownerNode
	s.cmd = cmd
	s.timeout = timeout
	if s.err != nil {
		return contracts.CommandAck{}, s.err
	}
	if !s.hasAck {
		s.ack = contracts.CommandAck{CommandID: cmd.CommandID, RoomID: cmd.RoomID, Accepted: true}
	}
	return s.ack, nil
}

type recordingCommandLogReader struct {
	roomID   string
	afterSeq int64
	commands []contracts.CommandLogRecord
	err      error
}

func (r *recordingCommandLogReader) ListCommandLog(roomID string, afterSeq int64) ([]contracts.CommandLogRecord, error) {
	r.roomID = roomID
	r.afterSeq = afterSeq
	return r.commands, r.err
}

type roomCreationStore struct {
	controlStore
	setRoom       contracts.RoomRecord
	setErr        error
	reservedRoom  contracts.RoomRecord
	reserveExists bool
	reserveErr    error
	deletedRoomID string
	deleteErr     error
}

func (s *roomCreationStore) ListNodes(context.Context, string) ([]contracts.NodeInfo, error) {
	return []contracts.NodeInfo{{
		NodeID:       "roomhost-a",
		Type:         contracts.NodeTypeRoomHost,
		ActiveRooms:  0,
		GameVersions: map[string]string{contracts.GameDaVinci: davinci.EngineVersion},
	}}, nil
}

func (s *roomCreationStore) ListRooms(context.Context, string) ([]contracts.RoomRecord, error) {
	return nil, nil
}

func (s *roomCreationStore) ListAllPresence(context.Context) ([]contracts.PresenceRecord, error) {
	return nil, nil
}

func (s *roomCreationStore) RoomCountsByOwner(context.Context, time.Time) (map[string]int, map[string]int, error) {
	return map[string]int{}, map[string]int{}, nil
}

func (s *roomCreationStore) SetRoom(_ context.Context, room contracts.RoomRecord) error {
	s.setRoom = room
	return s.setErr
}

func (s *roomCreationStore) SaveRuntimeEvent(context.Context, contracts.RuntimeEvent) error {
	return nil
}

func (s *roomCreationStore) ReserveRoomCreation(_ context.Context, room contracts.RoomRecord, _, _ string, _ int) (contracts.RoomRecord, bool, error) {
	if s.reserveErr != nil {
		return contracts.RoomRecord{}, false, s.reserveErr
	}
	if s.reserveExists {
		return s.reservedRoom, true, nil
	}
	return room, false, nil
}

func (s *roomCreationStore) ReserveNextMatch(_ context.Context, roomID, _ string, randomSeed uint64) (contracts.RoomRecord, contracts.MatchRecord, bool, error) {
	room := s.setRoom
	if room.RoomID == "" {
		room = s.reservedRoom
	}
	room.RoomID = roomID
	roundNo := room.NextRoundNo
	if roundNo < 1 {
		roundNo = 1
	}
	room.CurrentMatchID = fmt.Sprintf("%s-r%d", roomID, roundNo)
	room.NextRoundNo = roundNo + 1
	match := contracts.MatchRecord{
		MatchID:          room.CurrentMatchID,
		RuntimeRoomID:    roomID,
		GameType:         room.GameType,
		Status:           contracts.MatchStatusPreparing,
		OwnerUserID:      room.OwnerUserID,
		OwnerPlayerID:    room.OwnerPlayerID,
		RuntimeOwnerNode: room.OwnerNode,
		Epoch:            room.Epoch,
		EngineVersion:    room.EngineVersion,
		RoundNo:          roundNo,
		RandomSeed:       randomSeed,
	}
	return room, match, false, nil
}

func (s *roomCreationStore) DeleteRoomCreation(_ context.Context, roomID string) error {
	s.deletedRoomID = roomID
	return s.deleteErr
}

type roomCreationCoordinator struct {
	roomCoordinator
	deletedRoomID    string
	deletedOwnerNode string
	deletedEpoch     int64
	deleteErr        error
}

func (c *roomCreationCoordinator) InitRoomOwner(_ context.Context, room contracts.RoomRecord, ttl time.Duration) (coordination.RoomOwner, error) {
	return coordination.RoomOwner{
		RoomID:         room.RoomID,
		OwnerNode:      room.OwnerNode,
		Epoch:          room.Epoch,
		LeaseExpiresAt: time.Now().UTC().Add(ttl),
	}, nil
}

func (c *roomCreationCoordinator) DeleteRoomOwner(_ context.Context, roomID, ownerNode string, epoch int64) error {
	c.deletedRoomID = roomID
	c.deletedOwnerNode = ownerNode
	c.deletedEpoch = epoch
	return c.deleteErr
}

func TestResolveGameDefinitionUsesRegisteredDefault(t *testing.T) {
	server := NewServerWithConfig(nil, nil, Config{Games: game.NewRegistry(davinci.Definition())})

	def, err := server.resolveGameDefinition("")
	if err != nil {
		t.Fatalf("resolveGameDefinition() error = %v", err)
	}
	if def.GameType != contracts.GameDaVinci {
		t.Fatalf("game type = %q, want %q", def.GameType, contracts.GameDaVinci)
	}
	if def.Version != davinci.EngineVersion {
		t.Fatalf("version = %q, want %q", def.Version, davinci.EngineVersion)
	}
	if def.DefaultMaxPlayers != 4 {
		t.Fatalf("default max players = %d, want 4", def.DefaultMaxPlayers)
	}
}

func TestResolveGameDefinitionRejectsUnknownGame(t *testing.T) {
	server := NewServerWithConfig(nil, nil, Config{Games: game.NewRegistry(davinci.Definition())})

	_, err := server.resolveGameDefinition("missing-game")
	if !errors.Is(err, game.ErrUnknownGame) {
		t.Fatalf("resolveGameDefinition() error = %v, want ErrUnknownGame", err)
	}
}

func TestNewRoomIDUsesShortRandomSuffix(t *testing.T) {
	first, err := newRoomID(contracts.GameAcquire)
	if err != nil {
		t.Fatalf("newRoomID() error = %v", err)
	}
	second, err := newRoomID(contracts.GameAcquire)
	if err != nil {
		t.Fatalf("newRoomID() second error = %v", err)
	}

	prefix := contracts.GameAcquire + "_"
	if !strings.HasPrefix(first, prefix) {
		t.Fatalf("room ID = %q, want prefix %q", first, prefix)
	}
	suffix := strings.TrimPrefix(first, prefix)
	if len(suffix) != roomIDSuffixLength {
		t.Fatalf("room ID suffix length = %d, want %d", len(suffix), roomIDSuffixLength)
	}
	for _, char := range suffix {
		if !strings.ContainsRune(roomIDAlphabet, char) {
			t.Fatalf("room ID suffix contains unsupported character %q: %q", char, suffix)
		}
	}
	if first == second {
		t.Fatalf("consecutive room IDs are identical: %q", first)
	}
}

func TestCreateRoomActorSendsRuntimeCreateCommand(t *testing.T) {
	sender := &recordingRoomCommandSender{}
	server := NewServerWithConfig(nil, nil, Config{RoomCommands: sender})
	rec := contracts.RoomRecord{
		RoomID:        "davinci-room-1",
		GameType:      contracts.GameDaVinci,
		OwnerUserID:   "owner-user",
		OwnerPlayerID: "owner-player",
		OwnerNode:     "roomhost-a",
		Epoch:         7,
	}

	match := contracts.MatchRecord{MatchID: "davinci-room-1-r1"}
	if err := server.createRoomActor(context.Background(), rec, match, contracts.RuntimeCommandRoomCreate); err != nil {
		t.Fatalf("createRoomActor() error = %v", err)
	}
	if sender.ownerNode != "roomhost-a" {
		t.Fatalf("owner node = %q, want roomhost-a", sender.ownerNode)
	}
	if sender.timeout != roomInitializationTimeout {
		t.Fatalf("timeout = %s, want %s", sender.timeout, roomInitializationTimeout)
	}
	cmd := sender.cmd
	if cmd.Type != contracts.RuntimeCommandRoomCreate {
		t.Fatalf("command type = %q, want %q", cmd.Type, contracts.RuntimeCommandRoomCreate)
	}
	if cmd.RoomID != rec.RoomID || cmd.GameType != rec.GameType || cmd.Epoch != rec.Epoch {
		t.Fatalf("unexpected command room fields: %+v", cmd)
	}
	if cmd.UserID != rec.OwnerUserID || cmd.PlayerID != rec.OwnerPlayerID {
		t.Fatalf("unexpected command identity: %+v", cmd)
	}
	if cmd.CommandID == "" || cmd.TraceID != rec.RoomID || cmd.SentAt.IsZero() {
		t.Fatalf("command metadata not populated: %+v", cmd)
	}
}

func TestCreateRoomActorReturnsRejectedAck(t *testing.T) {
	sender := &recordingRoomCommandSender{ack: contracts.CommandAck{Accepted: false, Message: "bad room"}, hasAck: true}
	server := NewServerWithConfig(nil, nil, Config{RoomCommands: sender})

	err := server.createRoomActor(
		context.Background(),
		contracts.RoomRecord{RoomID: "room-1", OwnerNode: "roomhost-a"},
		contracts.MatchRecord{MatchID: "room-1-r1"},
		contracts.RuntimeCommandRoomCreate,
	)
	if err == nil {
		t.Fatalf("createRoomActor() error = nil, want rejection")
	}
}

func TestStartNextMatchSendsRuntimeMatchCreate(t *testing.T) {
	st := &roomCreationStore{setRoom: contracts.RoomRecord{
		RoomID:        "room-1",
		GameType:      contracts.GameDaVinci,
		OwnerUserID:   "owner-user",
		OwnerPlayerID: "owner-player",
		OwnerNode:     "roomhost-a",
		Epoch:         3,
		Status:        contracts.RoomStatusOpen,
		EngineVersion: davinci.EngineVersion,
		NextRoundNo:   2,
	}}
	sender := &recordingRoomCommandSender{}
	server := NewServerWithConfig(st, nil, Config{RoomCommands: sender})
	room, match, err := server.startNextMatch(context.Background(), "room-1", "request-2")
	if err != nil {
		t.Fatal(err)
	}
	if room.CurrentMatchID != match.MatchID || match.MatchID == "" {
		t.Fatalf("room/match = %+v/%+v", room, match)
	}
	if sender.cmd.Type != contracts.RuntimeCommandMatchCreate || sender.cmd.MatchID != match.MatchID {
		t.Fatalf("runtime match command = %+v", sender.cmd)
	}
}

func TestCreateRoomRecordCompensatesPersistFailure(t *testing.T) {
	persistErr := errors.New("persist failed")
	st := &roomCreationStore{setErr: persistErr}
	coord := &roomCreationCoordinator{}
	sender := &recordingRoomCommandSender{}
	server := NewServerWithConfig(st, coord, Config{
		Games:        game.NewRegistry(davinci.Definition()),
		RoomCommands: sender,
	})

	_, err := server.createRoomRecord(context.Background(), createRoomRequest{
		GameType:      contracts.GameDaVinci,
		OwnerUserID:   "owner-user",
		OwnerPlayerID: "owner-player",
	})
	if !errors.Is(err, persistErr) {
		t.Fatalf("createRoomRecord() error = %v, want persist error", err)
	}
	if st.setRoom.RoomID == "" || st.deletedRoomID != st.setRoom.RoomID {
		t.Fatalf("room cleanup mismatch: stored=%q deleted=%q", st.setRoom.RoomID, st.deletedRoomID)
	}
	if coord.deletedRoomID != st.setRoom.RoomID || coord.deletedOwnerNode != "roomhost-a" || coord.deletedEpoch != 1 {
		t.Fatalf("owner cleanup mismatch: %+v", coord)
	}
	if sender.ownerNode != "" {
		t.Fatalf("room actor command sent after persist failure: owner=%q", sender.ownerNode)
	}
}

func TestCreateRoomRecordCompensatesActorFailure(t *testing.T) {
	actorErr := errors.New("roomhost unavailable")
	st := &roomCreationStore{}
	coord := &roomCreationCoordinator{}
	sender := &recordingRoomCommandSender{err: actorErr}
	server := NewServerWithConfig(st, coord, Config{
		Games:        game.NewRegistry(davinci.Definition()),
		RoomCommands: sender,
	})

	_, err := server.createRoomRecord(context.Background(), createRoomRequest{
		GameType:      contracts.GameDaVinci,
		OwnerUserID:   "owner-user",
		OwnerPlayerID: "owner-player",
	})
	if !errors.Is(err, actorErr) {
		t.Fatalf("createRoomRecord() error = %v, want actor error", err)
	}
	if st.setRoom.RoomID == "" || st.deletedRoomID != st.setRoom.RoomID {
		t.Fatalf("room cleanup mismatch: stored=%q deleted=%q", st.setRoom.RoomID, st.deletedRoomID)
	}
	if coord.deletedRoomID != st.setRoom.RoomID || coord.deletedOwnerNode != "roomhost-a" || coord.deletedEpoch != 1 {
		t.Fatalf("owner cleanup mismatch: %+v", coord)
	}
}

func TestCreateRoomRecordReturnsCompletedIdempotentRequest(t *testing.T) {
	existing := contracts.RoomRecord{
		RoomID:        "davinci_existing",
		GameType:      contracts.GameDaVinci,
		OwnerUserID:   "owner-user",
		OwnerPlayerID: "owner-player",
		OwnerNode:     "roomhost-a",
		Epoch:         1,
		Status:        contracts.RoomStatusMatch,
		EngineVersion: davinci.EngineVersion,
	}
	st := &roomCreationStore{reserveExists: true, reservedRoom: existing}
	sender := &recordingRoomCommandSender{}
	server := NewServerWithConfig(st, &roomCreationCoordinator{}, Config{
		Games:        game.NewRegistry(davinci.Definition()),
		RoomCommands: sender,
	})
	got, err := server.createRoomRecord(context.Background(), createRoomRequest{
		GameType:      contracts.GameDaVinci,
		OwnerUserID:   "owner-user",
		OwnerPlayerID: "owner-player",
		RequestID:     "request-1",
	})
	if err != nil {
		t.Fatalf("createRoomRecord() error = %v", err)
	}
	if got.RoomID != existing.RoomID || sender.ownerNode != "" {
		t.Fatalf("idempotent result = %+v, sender owner = %q", got, sender.ownerNode)
	}
}

func TestCreateRoomRecordRejectsQuotaExceeded(t *testing.T) {
	st := &roomCreationStore{reserveErr: store.ErrRoomQuotaExceeded}
	server := NewServerWithConfig(st, &roomCreationCoordinator{}, Config{
		Games: game.NewRegistry(davinci.Definition()),
	})
	_, err := server.createRoomRecord(context.Background(), createRoomRequest{
		GameType:      contracts.GameDaVinci,
		OwnerUserID:   "owner-user",
		OwnerPlayerID: "owner-player",
		RequestID:     "request-1",
	})
	if !errors.Is(err, store.ErrRoomQuotaExceeded) {
		t.Fatalf("createRoomRecord() error = %v, want quota error", err)
	}
}

func TestCommandLogReturnsRoomCommandsFromReader(t *testing.T) {
	reader := &recordingCommandLogReader{
		commands: []contracts.CommandLogRecord{
			{RoomID: "room-1", Seq: 7, CommandID: "cmd-7", Epoch: 2, PlayerID: "alice", Type: "game_ready", CreatedAt: time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)},
			{RoomID: "room-1", Seq: 8, CommandID: "cmd-8", Epoch: 2, PlayerID: "bob", Type: "game_guess_card", CreatedAt: time.Date(2026, 7, 29, 10, 0, 1, 0, time.UTC)},
		},
	}
	server := NewServerWithConfig(nil, nil, Config{CommandLogs: reader, AdminAuth: adminTokenVerifier{}})

	req := httptest.NewRequest(http.MethodGet, "/platform/command-log?roomID=room-1&afterSeq=6&limit=1", nil)
	req.Header.Set("Authorization", "Bearer test-admin")
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if reader.roomID != "room-1" || reader.afterSeq != 6 {
		t.Fatalf("unexpected reader input: roomID=%q afterSeq=%d", reader.roomID, reader.afterSeq)
	}

	var out struct {
		StatusCode int `json:"status_code"`
		Data       struct {
			RoomID   string                       `json:"roomID"`
			AfterSeq int64                        `json:"afterSeq"`
			Commands []contracts.CommandLogRecord `json:"commands"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.StatusCode != http.StatusOK || out.Data.RoomID != "room-1" || out.Data.AfterSeq != 6 {
		t.Fatalf("unexpected response envelope: %+v", out)
	}
	if len(out.Data.Commands) != 1 {
		t.Fatalf("commands len = %d, want 1", len(out.Data.Commands))
	}
	if got := out.Data.Commands[0]; got.Seq != 7 || got.CommandID != "cmd-7" || got.Type != "game_ready" {
		t.Fatalf("unexpected command: %+v", got)
	}
}

func TestCommandLogRequiresRoomID(t *testing.T) {
	server := NewServerWithConfig(nil, nil, Config{CommandLogs: &recordingCommandLogReader{}, AdminAuth: adminTokenVerifier{}})

	req := httptest.NewRequest(http.MethodGet, "/platform/command-log", nil)
	req.Header.Set("Authorization", "Bearer test-admin")
	rec := httptest.NewRecorder()
	server.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestSelectRoomHostLeastAssignedUsesAssignedAndActiveRooms(t *testing.T) {
	selection := selectRoomHostLeastAssigned([]roomHostCandidate{
		{NodeID: "roomhost-a", AssignedRooms: 20, ActiveRooms: 6, Score: 26},
		{NodeID: "roomhost-b", AssignedRooms: 75, ActiveRooms: 6, Score: 81},
	}, SchedulerLeastAssigned, "test")

	if selection.OwnerNode != "roomhost-a" {
		t.Fatalf("expected roomhost-a, got %+v", selection)
	}
	if selection.Policy != SchedulerLeastAssigned {
		t.Fatalf("unexpected policy: %s", selection.Policy)
	}
	if !selection.Candidates[0].Selected || selection.Candidates[1].Selected {
		t.Fatalf("unexpected selected flags: %+v", selection.Candidates)
	}
}

func TestSelectRoomHostLoadAwareIgnoresHistoricalAssignedRooms(t *testing.T) {
	selection := selectRoomHostLoadAware([]roomHostCandidate{
		{NodeID: "roomhost-a", AssignedRooms: 20, ActiveRooms: 6, PendingRooms: 3},
		{NodeID: "roomhost-b", AssignedRooms: 75, ActiveRooms: 5, PendingRooms: 0},
	}, 2*time.Minute)

	if selection.OwnerNode != "roomhost-b" {
		t.Fatalf("expected lower live load roomhost-b, got %+v", selection)
	}
	if selection.Policy != SchedulerLoadAware {
		t.Fatalf("unexpected policy: %s", selection.Policy)
	}
	if selection.Candidates[0].Score != 9 || selection.Candidates[1].Score != 5 {
		t.Fatalf("unexpected load-aware scores: %+v", selection.Candidates)
	}
	if selection.Candidates[0].Selected || !selection.Candidates[1].Selected {
		t.Fatalf("unexpected selected flags: %+v", selection.Candidates)
	}
}

func TestSelectRoomHostLoadAwareCountsRecentPendingRooms(t *testing.T) {
	selection := selectRoomHostLoadAware([]roomHostCandidate{
		{NodeID: "roomhost-a", AssignedRooms: 100, ActiveRooms: 0, PendingRooms: 2},
		{NodeID: "roomhost-b", AssignedRooms: 0, ActiveRooms: 0, PendingRooms: 0},
	}, 2*time.Minute)

	if selection.OwnerNode != "roomhost-b" {
		t.Fatalf("expected roomhost-b because roomhost-a has recent pending rooms, got %+v", selection)
	}
}

func TestNormalizeSchedulerPolicy(t *testing.T) {
	cases := map[string]string{
		"":               SchedulerLoadAware,
		"load":           SchedulerLoadAware,
		"load-aware":     SchedulerLoadAware,
		"load_aware":     SchedulerLoadAware,
		"least":          SchedulerLeastAssigned,
		"least-assigned": SchedulerLeastAssigned,
		"least_assigned": SchedulerLeastAssigned,
		"rr":             SchedulerRoundRobin,
		"round-robin":    SchedulerRoundRobin,
		"round_robin":    SchedulerRoundRobin,
		"bad":            SchedulerLoadAware,
	}
	for input, want := range cases {
		if got := normalizeSchedulerPolicy(input); got != want {
			t.Fatalf("normalizeSchedulerPolicy(%q) = %q, want %q", input, got, want)
		}
	}
}
