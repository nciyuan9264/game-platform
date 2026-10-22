package transport

import (
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestCommandReplicas(t *testing.T) {
	tests := []struct {
		value string
		want  int
	}{
		{value: "", want: 1},
		{value: "3", want: 3},
		{value: "0", want: 1},
		{value: "6", want: 1},
		{value: "invalid", want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Setenv("NATS_COMMAND_REPLICAS", tt.value)
			if got := commandReplicas(); got != tt.want {
				t.Fatalf("commandReplicas() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRoomCommandSubjectSeparatesControlTraffic(t *testing.T) {
	if got := roomCommandSubject("roomhost-1", "game_ready"); got != "room.cmd.roomhost-1" {
		t.Fatalf("game command subject = %q", got)
	}
	if got := roomCommandSubject("roomhost-1", contracts.RuntimeCommandRoomClose); got != "room.control.roomhost-1" {
		t.Fatalf("runtime command subject = %q", got)
	}
	if commandAllowedOnChannel(contracts.RuntimeCommandRoomClose, false) {
		t.Fatal("runtime command was allowed on gateway command channel")
	}
	if !commandAllowedOnChannel(contracts.RuntimeCommandRoomClose, true) {
		t.Fatal("runtime command was rejected on control channel")
	}
	if commandAllowedOnChannel("runtime_unknown", false) || commandAllowedOnChannel("runtime_unknown", true) {
		t.Fatal("unknown reserved runtime command was accepted")
	}
}

func TestCommandMessageIDIsScopedToMatch(t *testing.T) {
	first := commandMessageID(contracts.CommandLogRecord{RoomID: "room-1", MatchID: "room-1-r1", CommandID: "cmd-1"})
	second := commandMessageID(contracts.CommandLogRecord{RoomID: "room-1", MatchID: "room-1-r2", CommandID: "cmd-1"})
	if first == second {
		t.Fatalf("command message IDs collide across matches: %q", first)
	}
}

func TestSameCommandIntentRejectsCommandIDReuse(t *testing.T) {
	original := contracts.CommandLogRecord{
		RoomID:    "room-1",
		MatchID:   "room-1-r1",
		CommandID: "cmd-1",
		PlayerID:  "alice",
		Type:      "add",
		Payload:   map[string]any{"delta": float64(1)},
	}
	retry := original
	retry.Payload = map[string]any{"delta": float64(2)}
	if sameCommandIntent(original, retry) {
		t.Fatal("command ID reuse with a different payload was accepted")
	}
}
