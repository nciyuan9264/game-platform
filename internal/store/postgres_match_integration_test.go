package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestPostgresRoomMatchLifecycleIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	store, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	roomID := fmt.Sprintf("lifecycle_%d", time.Now().UnixNano())
	room := contracts.RoomRecord{
		RoomID:        roomID,
		GameType:      contracts.GameAcquire,
		Visibility:    contracts.VisibilityPrivate,
		OwnerUserID:   "integration-owner",
		OwnerPlayerID: "integration-owner",
		OwnerNode:     "roomhost-integration",
		Epoch:         1,
		Status:        contracts.RoomStatusOpen,
		MaxPlayers:    6,
		EngineVersion: "acquire-v2",
		NextRoundNo:   1,
		CreatedAt:     time.Now().UTC(),
	}
	if _, _, err := store.ReserveRoomCreation(ctx, room, "create", "hash", 0); err != nil {
		t.Fatal(err)
	}
	storedRoom, err := store.GetRoom(ctx, roomID)
	if err != nil {
		t.Fatal(err)
	}
	if storedRoom.Visibility != contracts.VisibilityPrivate {
		t.Fatalf("stored visibility = %q, want private", storedRoom.Visibility)
	}
	room.LeaseExpiresAt = time.Now().UTC().Add(time.Minute)
	if err := store.SetRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkRoomOwnerReady(ctx, roomID, room.OwnerNode, room.Epoch, room.LeaseExpiresAt); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.DeleteRoomCreation(context.Background(), roomID)
		_ = store.Close()
	})

	room, first, existing, err := store.ReserveNextMatch(ctx, roomID, "round-1", 101)
	if err != nil || existing {
		t.Fatalf("reserve first match: existing=%v err=%v", existing, err)
	}
	if first.MatchID != roomID+"-r1" || first.RoundNo != 1 || first.RandomSeed != 101 || room.CurrentMatchID != first.MatchID {
		t.Fatalf("first room/match = %+v/%+v", room, first)
	}
	initialRoster := []contracts.MatchPlayerRecord{
		{UserID: "1", PlayerID: "1", Online: true, SeatIndex: 0, Ready: true},
		{UserID: "2", PlayerID: "2", Online: true, SeatIndex: 1, Ready: true},
		{PlayerID: "ai_001", DisplayName: "AI", Online: true, IsAI: true, SeatIndex: 2, Ready: true},
	}
	if err := store.SyncMatchRoster(ctx, first.MatchID, initialRoster); err != nil {
		t.Fatal(err)
	}
	if players, err := store.listMatchPlayers(ctx, first.MatchID); err != nil || len(players) != 3 {
		t.Fatalf("initial roster = %+v, err=%v", players, err)
	}
	retainedRoster := []contracts.MatchPlayerRecord{
		{UserID: "1", PlayerID: "1", Online: false, SeatIndex: 0, Ready: true},
		{PlayerID: "ai_001", DisplayName: "AI", Online: true, IsAI: true, SeatIndex: 2, Ready: true},
	}
	if err := store.SyncMatchRoster(ctx, first.MatchID, retainedRoster); err != nil {
		t.Fatal(err)
	}
	players, err := store.listMatchPlayers(ctx, first.MatchID)
	if err != nil {
		t.Fatal(err)
	}
	if len(players) != 2 || players[0].PlayerID != "1" || players[0].Online || players[1].PlayerID != "ai_001" {
		t.Fatalf("roster after human removal = %+v", players)
	}
	if err := store.SyncMatchRoster(ctx, first.MatchID, retainedRoster[:1]); err != nil {
		t.Fatal(err)
	}
	if players, err := store.listMatchPlayers(ctx, first.MatchID); err != nil || len(players) != 1 || players[0].PlayerID != "1" {
		t.Fatalf("roster after AI removal = %+v, err=%v", players, err)
	}
	if err := store.SyncMatchRoster(ctx, first.MatchID, nil); err != nil {
		t.Fatal(err)
	}
	if players, err := store.listMatchPlayers(ctx, first.MatchID); err != nil || len(players) != 0 {
		t.Fatalf("empty roster = %+v, err=%v", players, err)
	}
	if err := store.FinishMatch(ctx, roomID, first.MatchID, contracts.MatchStatusEnded, "completed", room.OwnerNode, room.Epoch, 7); err != nil {
		t.Fatal(err)
	}

	room, second, existing, err := store.ReserveNextMatch(ctx, roomID, "round-2", 202)
	if err != nil || existing {
		t.Fatalf("reserve second match: existing=%v err=%v", existing, err)
	}
	if second.MatchID != roomID+"-r2" || second.RoundNo != 2 || second.RandomSeed != 202 || room.CurrentMatchID != second.MatchID {
		t.Fatalf("second room/match = %+v/%+v", room, second)
	}
	retriedRoom, retried, existing, err := store.ReserveNextMatch(ctx, roomID, "round-2", 999)
	if err != nil || !existing || retried.MatchID != second.MatchID || retriedRoom.CurrentMatchID != second.MatchID {
		t.Fatalf("idempotent retry = %+v/%+v existing=%v err=%v", retriedRoom, retried, existing, err)
	}
}

func TestPostgresOwnerGenerationNeverReusesEpochIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	store, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	roomID := fmt.Sprintf("owner_generation_%d", time.Now().UnixNano())
	expired := time.Now().UTC().Add(-time.Minute)
	room := contracts.RoomRecord{
		RoomID:         roomID,
		GameType:       contracts.GameAcquire,
		OwnerUserID:    "integration-owner",
		OwnerPlayerID:  "integration-owner",
		OwnerNode:      "roomhost-a",
		Epoch:          1,
		Status:         contracts.RoomStatusOpen,
		MaxPlayers:     6,
		EngineVersion:  "acquire-v2",
		NextRoundNo:    1,
		CreatedAt:      expired,
		LeaseExpiresAt: expired,
	}
	if _, _, err := store.ReserveRoomCreation(ctx, room, "create", "hash", 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.DeleteRoomCreation(context.Background(), roomID)
		_ = store.Close()
	})
	if err := store.SetRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkRoomOwnerReady(ctx, roomID, room.OwnerNode, room.Epoch, expired); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	second, err := store.ReserveRoomOwner(ctx, roomID, 1, "roomhost-b", now, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if second.Epoch != 2 {
		t.Fatalf("second epoch = %d, want 2", second.Epoch)
	}
	if err := store.SaveSnapshot(ctx, contracts.SnapshotRecord{
		RoomID:        roomID,
		MatchID:       roomID + "-r1",
		Seq:           1,
		Epoch:         1,
		EngineVersion: "acquire-v2",
		State:         []byte("{}"),
	}); !errors.Is(err, ErrStaleRoomWrite) {
		t.Fatalf("old epoch snapshot error = %v, want stale write", err)
	}
	if err := store.SaveCommandAck(ctx, contracts.CommandAck{
		CommandID:  "old-command",
		RoomID:     roomID,
		MatchID:    roomID + "-r1",
		Epoch:      1,
		OwnerNode:  "roomhost-a",
		IntentHash: "old-intent",
		Accepted:   true,
		Seq:        1,
	}); !errors.Is(err, ErrStaleOwnerWrite) {
		t.Fatalf("old epoch ack error = %v, want stale owner", err)
	}
	if err := store.SaveOutboundMessage(ctx, contracts.OutboundMessage{
		MessageID:      "old-outbound-" + roomID,
		RoomID:         roomID,
		MatchID:        roomID + "-r1",
		Seq:            1,
		Epoch:          1,
		OwnerNode:      "roomhost-a",
		TargetUserID:   "user-1",
		TargetPlayerID: "player-1",
		TargetConn:     contracts.ConnectionRef{SessionID: "session-1", GatewayNode: "gateway-1"},
		Reliable:       true,
		Payload:        map[string]any{"type": "sync"},
	}); !errors.Is(err, ErrStaleOwnerWrite) {
		t.Fatalf("old epoch outbound error = %v, want stale owner", err)
	}
	if err := store.SaveSpectatorSnapshot(ctx, contracts.SpectatorSnapshotRecord{
		RoomID:    roomID,
		MatchID:   roomID + "-r1",
		GameType:  contracts.GameAcquire,
		Seq:       1,
		Epoch:     1,
		OwnerNode: "roomhost-a",
		Payload:   map[string]any{"type": "spectator"},
	}); !errors.Is(err, ErrStaleOwnerWrite) {
		t.Fatalf("old epoch spectator snapshot error = %v, want stale owner", err)
	}

	if err := store.SaveCommandAck(ctx, contracts.CommandAck{
		CommandID:  "current-command",
		RoomID:     roomID,
		MatchID:    roomID + "-r1",
		Epoch:      second.Epoch,
		OwnerNode:  second.OwnerNode,
		IntentHash: "current-intent",
		Accepted:   true,
		Seq:        1,
	}); err != nil {
		t.Fatalf("current epoch ack: %v", err)
	}
	if err := store.SaveOutboundMessage(ctx, contracts.OutboundMessage{
		MessageID:      "current-outbound-" + roomID,
		RoomID:         roomID,
		MatchID:        roomID + "-r1",
		Seq:            1,
		Epoch:          second.Epoch,
		OwnerNode:      second.OwnerNode,
		TargetUserID:   "user-1",
		TargetPlayerID: "player-1",
		TargetConn:     contracts.ConnectionRef{SessionID: "session-1", GatewayNode: "gateway-1"},
		Reliable:       true,
		Payload:        map[string]any{"type": "sync"},
	}); err != nil {
		t.Fatalf("current epoch outbound: %v", err)
	}
	if err := store.SaveSpectatorSnapshot(ctx, contracts.SpectatorSnapshotRecord{
		RoomID:    roomID,
		MatchID:   roomID + "-r1",
		GameType:  contracts.GameAcquire,
		Seq:       1,
		Epoch:     second.Epoch,
		OwnerNode: second.OwnerNode,
		Payload:   map[string]any{"type": "spectator"},
	}); err != nil {
		t.Fatalf("current epoch spectator snapshot: %v", err)
	}

	time.Sleep(15 * time.Millisecond)
	third, err := store.ReserveRoomOwner(ctx, roomID, 2, "roomhost-c", time.Now().UTC(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if third.Epoch != 3 {
		t.Fatalf("third epoch = %d, want 3", third.Epoch)
	}
}
