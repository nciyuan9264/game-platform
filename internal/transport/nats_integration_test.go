package transport

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestOwnerFenceRejectsOldEpochIntegration(t *testing.T) {
	url := os.Getenv("NATS_URL")
	if url == "" {
		t.Skip("NATS_URL is not configured")
	}
	router, err := NewRouter(url)
	if err != nil {
		t.Fatal(err)
	}
	defer router.Close()
	if err := router.EnsureCommandStream(); err != nil {
		t.Fatal(err)
	}

	matchID := fmt.Sprintf("fence-integration-%d", time.Now().UnixNano())
	base := contracts.CommandLogRecord{
		RoomID:    "fence-integration-room",
		CreatedAt: time.Now().UTC(),
	}
	if _, err := router.AppendOwnerFence(contracts.CommandLogRecord{
		RoomID:    base.RoomID,
		MatchID:   matchID,
		Epoch:     7,
		OwnerNode: "roomhost-a",
		CreatedAt: base.CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := router.AppendCommandLog(contracts.CommandLogRecord{
		RoomID:    base.RoomID,
		MatchID:   matchID,
		Seq:       1,
		CommandID: "cmd-1",
		Epoch:     7,
		OwnerNode: "roomhost-a",
		PlayerID:  "alice",
		Type:      "add",
		CreatedAt: base.CreatedAt.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := router.AppendOwnerFence(contracts.CommandLogRecord{
		RoomID:    base.RoomID,
		MatchID:   matchID,
		Epoch:     8,
		OwnerNode: "roomhost-b",
		CreatedAt: base.CreatedAt.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	_, err = router.AppendCommandLog(contracts.CommandLogRecord{
		RoomID:    base.RoomID,
		MatchID:   matchID,
		Seq:       2,
		CommandID: "cmd-old",
		Epoch:     7,
		OwnerNode: "roomhost-a",
		PlayerID:  "alice",
		Type:      "add",
		CreatedAt: base.CreatedAt.Add(3 * time.Second),
	})
	if !errors.Is(err, ErrCommandLogStaleEpoch) {
		t.Fatalf("old owner append error = %v, want stale epoch", err)
	}
	if _, err := router.AppendCommandLog(contracts.CommandLogRecord{
		RoomID:    base.RoomID,
		MatchID:   matchID,
		Seq:       2,
		CommandID: "cmd-2",
		Epoch:     8,
		OwnerNode: "roomhost-b",
		PlayerID:  "bob",
		Type:      "add",
		CreatedAt: base.CreatedAt.Add(4 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}

	records, err := router.ListCommandLog(matchID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 {
		t.Fatalf("records = %+v, want two fences and two commands", records)
	}
	for index := 1; index < len(records); index++ {
		if records[index].StreamSequence <= records[index-1].StreamSequence {
			t.Fatalf("stream sequence is not increasing: %+v", records)
		}
	}
}
