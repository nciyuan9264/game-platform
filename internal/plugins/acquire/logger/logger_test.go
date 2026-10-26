package logger

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"testing"
)

func TestScopedLoggersRemainIsolated(t *testing.T) {
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	var output bytes.Buffer
	log.SetOutput(&output)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	}()

	base := NewLogger("app")
	live := base.With(F("execution_mode", "live"), F("room_id", "live-room"))
	replay := base.With(F("execution_mode", "replay"), F("room_id", "replay-room"))

	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		for index := 0; index < 20; index++ {
			live.Info("live operation", F("index", index))
		}
	}()
	go func() {
		defer workers.Done()
		for index := 0; index < 20; index++ {
			replay.Info("replay operation", F("index", index))
		}
	}()
	workers.Wait()

	counts := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		message, _ := entry["msg"].(string)
		mode, _ := entry["execution_mode"].(string)
		roomID, _ := entry["room_id"].(string)
		switch message {
		case "live operation":
			if mode != "live" || roomID != "live-room" {
				t.Fatalf("live scope leaked: %+v", entry)
			}
		case "replay operation":
			if mode != "replay" || roomID != "replay-room" {
				t.Fatalf("replay scope leaked: %+v", entry)
			}
		default:
			t.Fatalf("unexpected log entry: %+v", entry)
		}
		counts[message]++
	}
	if counts["live operation"] != 20 || counts["replay operation"] != 20 {
		t.Fatalf("log counts = %+v", counts)
	}
}
