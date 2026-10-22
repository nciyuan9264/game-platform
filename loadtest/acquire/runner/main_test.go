package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebSocketURLAppendsEndpointAndQuery(t *testing.T) {
	got := webSocketURL("wss://api.gamebus.online/ws", "lt-room", "access-token")
	want := "wss://api.gamebus.online/ws?roomID=lt-room&token=access-token"

	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestHelperURLAppendsPath(t *testing.T) {
	got := helperURL("http://127.0.0.1:8000/", "/__loadtest/acquire/rooms")
	want := "http://127.0.0.1:8000/__loadtest/acquire/rooms"

	if got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

func TestUserIDForSeatMakesSeatOneTheCreatedOwner(t *testing.T) {
	cfg := config{Prefix: "lt-room"}

	ownerPrefix := ownerPrefixForRoom(cfg)
	seatOne := userIDForSeat(cfg, 0, 1)
	seatTwo := userIDForSeat(cfg, 0, 2)

	if ownerPrefix+"-000001" != seatOne {
		t.Fatalf("seat one = %q, want owner id %q", seatOne, ownerPrefix+"-000001")
	}
	if seatTwo == seatOne {
		t.Fatalf("seat two should not equal seat one owner id")
	}
}

func TestThroughputMbps(t *testing.T) {
	got := throughputMbps(250_000, time.Second)
	want := 2.0

	if got != want {
		t.Fatalf("throughputMbps = %v, want %v", got, want)
	}
}

func TestReadLoopExitsWhenContextCancelledWhileBlocked(t *testing.T) {
	upgrader := websocket.Upgrader{}
	releaseServerConn := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		<-releaseServerConn
	}))
	defer func() {
		close(releaseServerConn)
		server.Close()
	}()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	client := &loadClient{conn: conn, m: &metrics{}}
	var wg sync.WaitGroup
	wg.Add(1)
	done := make(chan struct{})
	go func() {
		client.readLoop(ctx, &wg, false)
		wg.Wait()
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("readLoop did not exit after context cancellation")
	}
}
