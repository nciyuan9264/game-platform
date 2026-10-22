package coordination

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform-server/internal/contracts"
)

func TestReadyRouteFollowsOwnerLeaseIntegration(t *testing.T) {
	endpoints := os.Getenv("ETCD_ENDPOINTS")
	if endpoints == "" {
		t.Skip("ETCD_ENDPOINTS is not configured")
	}
	ctx := context.Background()
	coordinator, err := Open(ctx, endpoints)
	if err != nil {
		t.Fatal(err)
	}
	defer coordinator.Close()

	roomID := fmt.Sprintf("ready-route-integration-%d", time.Now().UnixNano())
	owner, err := coordinator.InitRoomOwner(ctx, contracts.RoomRecord{
		RoomID:    roomID,
		OwnerNode: "roomhost-a",
		Epoch:     7,
	}, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.ReadyRoomOwner(ctx, roomID); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("unpublished route error = %v, want ErrNotOwner", err)
	}
	if err := coordinator.PublishRoomReady(ctx, roomID, owner.OwnerNode, owner.Epoch); err != nil {
		t.Fatal(err)
	}
	ready, err := coordinator.ReadyRoomOwner(ctx, roomID)
	if err != nil {
		t.Fatal(err)
	}
	if ready.OwnerNode != owner.OwnerNode || ready.Epoch != owner.Epoch {
		t.Fatalf("ready owner = %+v", ready)
	}
	renewed, err := coordinator.RenewRoomOwner(ctx, roomID, owner.OwnerNode, owner.Epoch, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ready, err = coordinator.ReadyRoomOwner(ctx, roomID)
	if err != nil {
		t.Fatal(err)
	}
	if ready.Epoch != renewed.Epoch || !ready.LeaseExpiresAt.Equal(renewed.LeaseExpiresAt) {
		t.Fatalf("ready route was not renewed with owner: ready=%+v owner=%+v", ready, renewed)
	}
	if err := coordinator.DeleteRoomOwner(ctx, roomID, owner.OwnerNode, owner.Epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.ReadyRoomOwner(ctx, roomID); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("deleted route error = %v, want ErrNotOwner", err)
	}

	claimed, ok, err := coordinator.ClaimExpiredRoom(ctx, contracts.RoomRecord{
		RoomID:    roomID,
		OwnerNode: "roomhost-b",
		Epoch:     8,
		Status:    contracts.RoomStatusOpen,
	}, "roomhost-b", 10*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim owner: owner=%+v claimed=%v err=%v", claimed, ok, err)
	}
	if claimed.Epoch != 8 {
		t.Fatalf("claimed epoch = %d, want exact reserved epoch 8", claimed.Epoch)
	}
	if err := coordinator.DeleteRoomOwner(ctx, roomID, claimed.OwnerNode, claimed.Epoch); err != nil {
		t.Fatal(err)
	}
}
