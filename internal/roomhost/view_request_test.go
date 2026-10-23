package roomhost

import (
	"context"
	"errors"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

type metadataCaptureEngine struct {
	requests          []game.ViewRequest
	desiredVisibility string
}

func (e *metadataCaptureEngine) GameType() string { return "metadata-capture" }

func (e *metadataCaptureEngine) Version() string { return "v1" }

func (e *metadataCaptureEngine) InitRoom(context.Context, game.RoomConfig) (game.State, error) {
	return struct{}{}, nil
}

func (e *metadataCaptureEngine) EncodeState(context.Context, game.State) ([]byte, error) {
	return nil, nil
}

func (e *metadataCaptureEngine) DecodeState(context.Context, []byte) (game.State, error) {
	return struct{}{}, nil
}

func (e *metadataCaptureEngine) ApplyCommand(context.Context, game.State, contracts.RoomCommand) (game.ApplyResult, error) {
	return game.ApplyResult{}, nil
}

func (e *metadataCaptureEngine) BuildPlayerView(_ context.Context, _ game.State, req game.ViewRequest) (any, error) {
	e.requests = append(e.requests, req)
	return map[string]any{
		"type":        contracts.MessageTypeMatchSync,
		"players":     map[string]contracts.RoomPlayer{},
		"playerOrder": []string{},
	}, nil
}

func (e *metadataCaptureEngine) RoomMetadata(context.Context, game.State) (game.RoomMetadata, error) {
	return game.RoomMetadata{Visibility: e.desiredVisibility}, nil
}

func TestRoomActorPassesRoomMetadataToPlayerViews(t *testing.T) {
	store := &fakeRoomhostStore{
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
	engine := &metadataCaptureEngine{}
	actor := &RoomActor{
		host: NewHost("roomhost-1", store, nil, router, nil),
		record: contracts.RoomRecord{
			RoomID:     "room-1",
			GameType:   engine.GameType(),
			Visibility: contracts.VisibilityPrivate,
			MaxPlayers: 6,
		},
		match: contracts.MatchRecord{MatchID: "match-1"},
		definition: game.Definition{
			GameType: engine.GameType(),
			Version:  engine.Version(),
			Engine:   engine,
		},
		state: struct{}{},
		seq:   8,
	}

	if err := actor.broadcast(context.Background(), "broadcast-trace"); err != nil {
		t.Fatal(err)
	}
	if err := actor.deliverEffects(context.Background(), "effect-trace", []game.DeliveryEffect{{
		Audience: game.AudienceAll,
		Payload:  map[string]any{"type": "notice"},
	}}); err != nil {
		t.Fatal(err)
	}

	want := game.ViewRequest{
		RoomID:     "room-1",
		MatchID:    "match-1",
		GameType:   engine.GameType(),
		PlayerID:   "player-1",
		Visibility: contracts.VisibilityPrivate,
		MaxPlayers: 6,
		Seq:        8,
	}
	if len(engine.requests) != 2 {
		t.Fatalf("BuildPlayerView request count = %d, want 2", len(engine.requests))
	}
	for index, request := range engine.requests {
		if request != want {
			t.Fatalf("BuildPlayerView request %d = %+v, want %+v", index, request, want)
		}
	}
}

func TestRoomActorPersistsMetadataBeforePublishingIt(t *testing.T) {
	persistErr := errors.New("database unavailable")
	store := &fakeRoomhostStore{visibilityErr: persistErr}
	engine := &metadataCaptureEngine{desiredVisibility: contracts.VisibilityPublic}
	actor := &RoomActor{
		host: NewHost("roomhost-1", store, nil, nil, nil),
		record: contracts.RoomRecord{
			RoomID:     "room-1",
			GameType:   engine.GameType(),
			Visibility: contracts.VisibilityPrivate,
			OwnerNode:  "roomhost-1",
			Epoch:      7,
		},
		definition: game.Definition{Engine: engine},
		state:      struct{}{},
	}

	if err := actor.syncRoomMetadataLocked(context.Background()); !errors.Is(err, persistErr) {
		t.Fatalf("syncRoomMetadataLocked() error = %v, want %v", err, persistErr)
	}
	if actor.record.Visibility != contracts.VisibilityPrivate || !actor.metadataDirty {
		t.Fatalf("failed metadata projection leaked into room record: visibility=%q dirty=%v", actor.record.Visibility, actor.metadataDirty)
	}

	store.visibilityErr = nil
	if err := actor.syncRoomMetadataLocked(context.Background()); err != nil {
		t.Fatal(err)
	}
	if actor.record.Visibility != contracts.VisibilityPublic || actor.metadataDirty || !actor.outboundDirty {
		t.Fatalf(
			"retried metadata projection = visibility %q metadataDirty=%v outboundDirty=%v",
			actor.record.Visibility,
			actor.metadataDirty,
			actor.outboundDirty,
		)
	}
	wantCall := "room-1:public:roomhost-1:7"
	if len(store.visibilityCalls) != 2 || store.visibilityCalls[1] != wantCall {
		t.Fatalf("visibility calls = %+v, want two calls ending in %q", store.visibilityCalls, wantCall)
	}
}
