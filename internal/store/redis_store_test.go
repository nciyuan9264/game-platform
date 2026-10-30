package store

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestPlayerSessionLease(t *testing.T) {
	redisServer := miniredis.RunT(t)
	st := NewRedisStore(redisServer.Addr(), 0)
	t.Cleanup(func() {
		_ = st.Close()
	})

	ctx := context.Background()
	first := contracts.ConnectionRef{GatewayNode: "gateway-a", SessionID: "session-a"}
	second := contracts.ConnectionRef{GatewayNode: "gateway-b", SessionID: "session-b"}
	const (
		roomID   = "room-1"
		playerID = "player-1"
	)

	previous, err := st.ClaimPlayerSession(ctx, roomID, playerID, first, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if previous.Valid() {
		t.Fatalf("first claim returned previous owner: %+v", previous)
	}

	previous, err = st.ClaimPlayerSession(ctx, roomID, playerID, second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if previous != first {
		t.Fatalf("second claim previous owner = %+v, want %+v", previous, first)
	}

	if refreshed, err := st.RefreshPlayerSession(ctx, roomID, playerID, first, 2*time.Minute); err != nil {
		t.Fatal(err)
	} else if refreshed {
		t.Fatal("replaced session refreshed the active lease")
	}
	if released, err := st.ReleasePlayerSession(ctx, roomID, playerID, first); err != nil {
		t.Fatal(err)
	} else if released {
		t.Fatal("replaced session released the active lease")
	}
	if claimed, err := st.ClaimPlayerDisconnect(ctx, roomID, playerID, first, time.Minute); err != nil {
		t.Fatal(err)
	} else if claimed {
		t.Fatal("replaced session claimed the player disconnect")
	}

	current, found, err := st.IsPlayerSessionCurrent(ctx, roomID, playerID, second)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !current {
		t.Fatalf("active session current = %v, found = %v", current, found)
	}
	if refreshed, err := st.RefreshPlayerSession(ctx, roomID, playerID, second, 2*time.Minute); err != nil {
		t.Fatal(err)
	} else if !refreshed {
		t.Fatal("active session did not refresh the lease")
	}
	if claimed, err := st.ClaimPlayerDisconnect(ctx, roomID, playerID, second, time.Minute); err != nil {
		t.Fatal(err)
	} else if !claimed {
		t.Fatal("active session did not claim the player disconnect")
	}
	if current, found, err := st.IsPlayerSessionCurrent(ctx, roomID, playerID, second); err != nil {
		t.Fatal(err)
	} else if current || !found {
		t.Fatalf("disconnect tombstone current = %v, found = %v", current, found)
	}
	previous, err = st.ClaimPlayerSession(ctx, roomID, playerID, first, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if previous.Valid() {
		t.Fatalf("claim after disconnect tombstone returned owner: %+v", previous)
	}
}

func TestPlayerDisconnectCanBeClaimedAfterActiveSessionReleased(t *testing.T) {
	redisServer := miniredis.RunT(t)
	st := NewRedisStore(redisServer.Addr(), 0)
	t.Cleanup(func() {
		_ = st.Close()
	})

	ctx := context.Background()
	stale := contracts.ConnectionRef{GatewayNode: "gateway-a", SessionID: "session-a"}
	active := contracts.ConnectionRef{GatewayNode: "gateway-b", SessionID: "session-b"}
	if _, err := st.ClaimPlayerSession(ctx, "room-1", "player-1", active, time.Minute); err != nil {
		t.Fatal(err)
	}
	if released, err := st.ReleasePlayerSession(ctx, "room-1", "player-1", active); err != nil {
		t.Fatal(err)
	} else if !released {
		t.Fatal("active session did not release its lease")
	}
	if claimed, err := st.ClaimPlayerDisconnect(
		ctx,
		"room-1",
		"player-1",
		stale,
		time.Minute,
	); err != nil {
		t.Fatal(err)
	} else if !claimed {
		t.Fatal("last stale presence did not claim the player disconnect")
	}
	if claimed, err := st.ClaimPlayerDisconnect(
		ctx,
		"room-1",
		"player-1",
		active,
		time.Minute,
	); err != nil {
		t.Fatal(err)
	} else if claimed {
		t.Fatal("player disconnect was claimed more than once")
	}
}

func TestPlayerSessionLeaseExpiresAndIsScoped(t *testing.T) {
	redisServer := miniredis.RunT(t)
	st := NewRedisStore(redisServer.Addr(), 0)
	t.Cleanup(func() {
		_ = st.Close()
	})

	ctx := context.Background()
	first := contracts.ConnectionRef{GatewayNode: "gateway-a", SessionID: "session-a"}
	second := contracts.ConnectionRef{GatewayNode: "gateway-b", SessionID: "session-b"}

	if _, err := st.ClaimPlayerSession(ctx, "room-1", "player-1", first, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimPlayerSession(ctx, "room-2", "player-1", second, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimPlayerSession(ctx, "room-1", "player-2", second, time.Minute); err != nil {
		t.Fatal(err)
	}

	redisServer.FastForward(2 * time.Second)
	if current, found, err := st.IsPlayerSessionCurrent(ctx, "room-1", "player-1", first); err != nil {
		t.Fatal(err)
	} else if current || found {
		t.Fatalf("expired session current = %v, found = %v", current, found)
	}

	previous, err := st.ClaimPlayerSession(ctx, "room-1", "player-1", second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if previous.Valid() {
		t.Fatalf("claim after expiration returned previous owner: %+v", previous)
	}

	for _, scope := range []struct {
		roomID   string
		playerID string
	}{
		{roomID: "room-2", playerID: "player-1"},
		{roomID: "room-1", playerID: "player-2"},
	} {
		current, found, err := st.IsPlayerSessionCurrent(ctx, scope.roomID, scope.playerID, second)
		if err != nil {
			t.Fatal(err)
		}
		if !found || !current {
			t.Fatalf("scoped lease %s/%s current = %v, found = %v", scope.roomID, scope.playerID, current, found)
		}
	}
}
