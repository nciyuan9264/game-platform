package acquire

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/plugins/acquire/domain/data"
)

func TestAcquireRandomTileReplayAcrossSnapshotBoundaries(t *testing.T) {
	plugin := &Plugin{}
	commands, expected := acquireRandomTileCommandLog(t, plugin, "replay-random-tiles")
	expectedState := encodedAcquireState(t, plugin, expected)
	expectedHash := stateHash(expectedState)
	expectedRoom, err := asRoom(expected)
	if err != nil {
		t.Fatal(err)
	}
	if expectedRoom.State.RandomCounter != 4 {
		t.Fatalf("baseline random counter = %d, want 4", expectedRoom.State.RandomCounter)
	}
	if !expectedRoom.State.GameStartTime.Equal(commands[4].SentAt) {
		t.Fatalf("game start time = %s, want command time %s", expectedRoom.State.GameStartTime, commands[4].SentAt)
	}
	wantDeadline := commands[6].SentAt.Add(30 * time.Second)
	if !expectedRoom.TurnDeadline.Equal(wantDeadline) {
		t.Fatalf("turn deadline = %s, want %s", expectedRoom.TurnDeadline, wantDeadline)
	}

	tests := []struct {
		name        string
		boundary    int
		useSnapshot bool
	}{
		{name: "no snapshot full replay", boundary: 0},
		{name: "snapshot before any command", boundary: 0, useSnapshot: true},
		{name: "snapshot before initial tile deal", boundary: 3, useSnapshot: true},
		{name: "snapshot after initial tile deal", boundary: 4, useSnapshot: true},
		{name: "snapshot before first turn draw", boundary: 5, useSnapshot: true},
		{name: "snapshot after first turn draw", boundary: 6, useSnapshot: true},
		{name: "snapshot after second turn draw", boundary: 7, useSnapshot: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for run := 0; run < 20; run++ {
				state := newAcquireReplayState(t, plugin, "replay-random-tiles")
				applyAcquireCommands(t, plugin, state, commands[:tt.boundary])
				if tt.useSnapshot {
					state = snapshotRoundTrip(t, plugin, state)
				}
				applyAcquireCommands(t, plugin, state, commands[tt.boundary:])

				gotState := encodedAcquireState(t, plugin, state)
				if !bytes.Equal(gotState, expectedState) {
					t.Fatalf(
						"run %d state hash = %s, want %s\n got: %s\nwant: %s",
						run,
						stateHash(gotState),
						expectedHash,
						gotState,
						expectedState,
					)
				}

				gotRoom, err := asRoom(state)
				if err != nil {
					t.Fatal(err)
				}
				if gotRoom.State.RandomSeed != expectedRoom.State.RandomSeed ||
					gotRoom.State.RandomCounter != expectedRoom.State.RandomCounter {
					t.Fatalf(
						"run %d random state = (%d,%d), want (%d,%d)",
						run,
						gotRoom.State.RandomSeed,
						gotRoom.State.RandomCounter,
						expectedRoom.State.RandomSeed,
						expectedRoom.State.RandomCounter,
					)
				}

				wantNext := nextAvailableTiles(t, plugin, expected, 8)
				gotNext := nextAvailableTiles(t, plugin, state, 8)
				if !reflect.DeepEqual(gotNext, wantNext) {
					t.Fatalf("run %d next tiles = %v, want %v", run, gotNext, wantNext)
				}
			}
		})
	}
}

func TestAcquireRandomTileSnapshotEdgeCases(t *testing.T) {
	ctx := context.Background()
	plugin := &Plugin{}
	commands, _ := acquireRandomTileCommandLog(t, plugin, "replay-random-edge")
	base := newAcquireReplayState(t, plugin, "replay-random-edge")
	applyAcquireCommands(t, plugin, base, commands[:5])

	t.Run("random counter wraps deterministically", func(t *testing.T) {
		state := snapshotRoundTrip(t, plugin, base)
		room, err := asRoom(state)
		if err != nil {
			t.Fatal(err)
		}
		room.State.RandomCounter = math.MaxUint64
		raw, err := plugin.EncodeState(ctx, state)
		if err != nil {
			t.Fatal(err)
		}

		var want []string
		for run := 0; run < 100; run++ {
			decoded, err := plugin.DecodeState(ctx, raw)
			if err != nil {
				t.Fatal(err)
			}
			decodedRoom, err := asRoom(decoded)
			if err != nil {
				t.Fatal(err)
			}
			got, err := data.GenerateAvailableTiles(decodedRoom, 8)
			if err != nil {
				t.Fatal(err)
			}
			if decodedRoom.State.RandomCounter != 0 {
				t.Fatalf("run %d wrapped counter = %d, want 0", run, decodedRoom.State.RandomCounter)
			}
			if run == 0 {
				want = got
			} else if !reflect.DeepEqual(got, want) {
				t.Fatalf("run %d tiles = %v, want %v", run, got, want)
			}
		}
	})

	t.Run("legacy zero seed derives deterministically", func(t *testing.T) {
		state := snapshotRoundTrip(t, plugin, base)
		room, err := asRoom(state)
		if err != nil {
			t.Fatal(err)
		}
		room.State.RandomSeed = 0
		room.State.RandomCounter = 0
		raw, err := plugin.EncodeState(ctx, state)
		if err != nil {
			t.Fatal(err)
		}

		var want []string
		var wantSeed uint64
		for run := 0; run < 100; run++ {
			decoded, err := plugin.DecodeState(ctx, raw)
			if err != nil {
				t.Fatal(err)
			}
			decodedRoom, err := asRoom(decoded)
			if err != nil {
				t.Fatal(err)
			}
			got, err := data.GenerateAvailableTiles(decodedRoom, 8)
			if err != nil {
				t.Fatal(err)
			}
			if decodedRoom.State.RandomSeed == 0 || decodedRoom.State.RandomCounter != 1 {
				t.Fatalf(
					"run %d random state = (%d,%d), want non-zero seed and counter 1",
					run,
					decodedRoom.State.RandomSeed,
					decodedRoom.State.RandomCounter,
				)
			}
			if run == 0 {
				want = got
				wantSeed = decodedRoom.State.RandomSeed
			} else if !reflect.DeepEqual(got, want) || decodedRoom.State.RandomSeed != wantSeed {
				t.Fatalf(
					"run %d tiles/seed = %v/%d, want %v/%d",
					run,
					got,
					decodedRoom.State.RandomSeed,
					want,
					wantSeed,
				)
			}
		}
	})
}

func TestAcquireRandomTileReplayAcrossRoomSeeds(t *testing.T) {
	plugin := &Plugin{}
	for seedCase := 0; seedCase < 64; seedCase++ {
		roomID := fmt.Sprintf("replay-room-seed-%03d", seedCase)
		commands, expected := acquireRandomTileCommandLog(t, plugin, roomID)
		expectedState := encodedAcquireState(t, plugin, expected)

		replayed := newAcquireReplayState(t, plugin, roomID)
		applyAcquireCommands(t, plugin, replayed, commands[:5])
		replayed = snapshotRoundTrip(t, plugin, replayed)
		applyAcquireCommands(t, plugin, replayed, commands[5:])

		gotState := encodedAcquireState(t, plugin, replayed)
		if !bytes.Equal(gotState, expectedState) {
			t.Fatalf(
				"seed case %d state hash = %s, want %s",
				seedCase,
				stateHash(gotState),
				stateHash(expectedState),
			)
		}
		if got, want := nextAvailableTiles(t, plugin, replayed, 8), nextAvailableTiles(t, plugin, expected, 8); !reflect.DeepEqual(got, want) {
			t.Fatalf("seed case %d next tiles = %v, want %v", seedCase, got, want)
		}
	}
}

func acquireRandomTileCommandLog(t *testing.T, plugin *Plugin, roomID string) ([]contracts.RoomCommand, game.State) {
	t.Helper()
	state := newAcquireReplayState(t, plugin, roomID)
	commands := []contracts.RoomCommand{
		{PlayerID: "alice-1", Type: "connect"},
		{PlayerID: "bob-2", Type: "connect"},
		{PlayerID: "bob-2", Type: "match_ready", Payload: map[string]any{"ready": true}},
		{PlayerID: "alice-1", Type: "match_begin"},
		{PlayerID: "alice-1", Type: "game_ready"},
	}
	baseTime := time.Date(2100, time.January, 2, 3, 4, 5, 0, time.UTC)
	for index := range commands {
		commands[index].SentAt = baseTime.Add(time.Duration(index) * time.Second)
	}
	applyAcquireCommands(t, plugin, state, commands)

	room, err := asRoom(state)
	if err != nil {
		t.Fatal(err)
	}
	aliceTile := room.State.Players["alice-1"].Tiles[0]
	alicePlace := contracts.RoomCommand{
		PlayerID: "alice-1",
		Type:     "game_place_tile",
		Payload:  map[string]any{"tileKey": aliceTile},
		SentAt:   baseTime.Add(5 * time.Second),
	}
	commands = append(commands, alicePlace)
	applyAcquireCommands(t, plugin, state, []contracts.RoomCommand{alicePlace})

	bobTile := room.State.Players["bob-2"].Tiles[0]
	bobPlace := contracts.RoomCommand{
		PlayerID: "bob-2",
		Type:     "game_place_tile",
		Payload:  map[string]any{"tileKey": bobTile},
		SentAt:   baseTime.Add(6 * time.Second),
	}
	commands = append(commands, bobPlace)
	applyAcquireCommands(t, plugin, state, []contracts.RoomCommand{bobPlace})
	return commands, state
}

func newAcquireReplayState(t *testing.T, plugin *Plugin, roomID string) game.State {
	t.Helper()
	state, err := plugin.InitRoom(context.Background(), game.RoomConfig{
		RoomID:        roomID,
		OwnerPlayerID: "alice-1",
		MaxPlayers:    2,
		RandomSeed:    replaySeed(roomID),
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func replaySeed(roomID string) uint64 {
	sum := sha256.Sum256([]byte(roomID))
	return binary.LittleEndian.Uint64(sum[:8])
}

func applyAcquireCommands(t *testing.T, plugin *Plugin, state game.State, commands []contracts.RoomCommand) {
	t.Helper()
	for index, command := range commands {
		if _, err := plugin.ApplyCommand(context.Background(), state, command); err != nil {
			t.Fatalf("command %d (%s) failed: %v", index, command.Type, err)
		}
	}
}

func snapshotRoundTrip(t *testing.T, plugin *Plugin, state game.State) game.State {
	t.Helper()
	raw, err := plugin.EncodeState(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := plugin.DecodeState(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func encodedAcquireState(t *testing.T, plugin *Plugin, state game.State) []byte {
	t.Helper()
	raw, err := plugin.EncodeState(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func nextAvailableTiles(t *testing.T, plugin *Plugin, state game.State, count int) []string {
	t.Helper()
	cloned := snapshotRoundTrip(t, plugin, state)
	room, err := asRoom(cloned)
	if err != nil {
		t.Fatal(err)
	}
	tiles, err := data.GenerateAvailableTiles(room, count)
	if err != nil {
		t.Fatal(err)
	}
	return tiles
}

func stateHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
