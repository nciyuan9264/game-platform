package control

import (
	"testing"
	"time"
)

func TestDurationUntilNext4AM(t *testing.T) {
	before := time.Date(2026, 8, 1, 3, 0, 0, 0, beijing)
	if got := durationUntilNext4AM(before); got < 59*time.Minute || got > 61*time.Minute {
		t.Fatalf("duration before 4AM = %s", got)
	}
	after := time.Date(2026, 8, 1, 5, 0, 0, 0, beijing)
	if got := durationUntilNext4AM(after); got < 22*time.Hour || got > 24*time.Hour {
		t.Fatalf("duration after 4AM = %s", got)
	}
}
