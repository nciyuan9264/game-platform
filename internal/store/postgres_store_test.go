package store

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

type roomScannerFunc func(dest ...any) error

func (scan roomScannerFunc) Scan(dest ...any) error {
	return scan(dest...)
}

func TestScanRoomIncludesVisibility(t *testing.T) {
	record, err := scanRoom(roomScannerFunc(func(dest ...any) error {
		if len(dest) != 21 {
			t.Fatalf("scan destinations = %d, want 21", len(dest))
		}
		*dest[0].(*string) = "room-1"
		*dest[1].(*string) = "acquire"
		*dest[2].(*string) = "private"
		*dest[3].(*string) = "Midnight"
		*dest[4].(*int) = 60
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if record.Visibility != "private" {
		t.Fatalf("visibility = %q, want private", record.Visibility)
	}
}

func TestNormalizeRoomVisibility(t *testing.T) {
	tests := []struct {
		name       string
		visibility string
		want       string
		wantError  bool
	}{
		{name: "default", want: contracts.VisibilityPublic},
		{name: "public", visibility: contracts.VisibilityPublic, want: contracts.VisibilityPublic},
		{name: "private", visibility: contracts.VisibilityPrivate, want: contracts.VisibilityPrivate},
		{name: "invalid", visibility: "hidden", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeRoomVisibility(tt.visibility)
			if (err != nil) != tt.wantError {
				t.Fatalf("normalizeRoomVisibility() error = %v, wantError %v", err, tt.wantError)
			}
			if got != tt.want {
				t.Fatalf("normalizeRoomVisibility() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRequireRoomVisibilityRejectsEmpty(t *testing.T) {
	if _, err := requireRoomVisibility(""); err == nil {
		t.Fatal("requireRoomVisibility() accepted an empty value")
	}
}

func TestWithBinaryParametersURL(t *testing.T) {
	dsn := "postgres://game:p%40ss@db.example.com:5432/game?sslmode=require"
	got := withBinaryParameters(dsn)
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if parsed.Query().Get("binary_parameters") != "yes" {
		t.Fatalf("binary_parameters = %q, want yes", parsed.Query().Get("binary_parameters"))
	}
	if parsed.Query().Get("sslmode") != "require" {
		t.Fatalf("sslmode = %q, want require", parsed.Query().Get("sslmode"))
	}
}

func TestWithBinaryParametersPreservesExplicitSetting(t *testing.T) {
	dsn := "postgres://game:game@db.example.com/game?binary_parameters=no"
	if got := withBinaryParameters(dsn); got != dsn {
		t.Fatalf("withBinaryParameters() = %q, want original DSN", got)
	}
}

func TestWithBinaryParametersKeywordDSN(t *testing.T) {
	got := withBinaryParameters("host=db.example.com dbname=game sslmode=require")
	if !strings.HasSuffix(got, " binary_parameters=yes") {
		t.Fatalf("withBinaryParameters() = %q, want binary parameter option", got)
	}
}

func TestNullableJSONUsesTextParameter(t *testing.T) {
	value := nullableJSON(json.RawMessage(`{"ok":true}`))
	if got, ok := value.(string); !ok || got != `{"ok":true}` {
		t.Fatalf("nullableJSON() = %#v, want JSON string", value)
	}
}

func TestValidateMatchPlayerIdentity(t *testing.T) {
	tests := []struct {
		name    string
		player  contracts.MatchPlayerRecord
		wantErr bool
	}{
		{
			name:   "human canonical ID",
			player: contracts.MatchPlayerRecord{UserID: "7", PlayerID: "7"},
		},
		{
			name:   "AI without user ID",
			player: contracts.MatchPlayerRecord{PlayerID: "ai_001", IsAI: true},
		},
		{
			name:    "human composite ID",
			player:  contracts.MatchPlayerRecord{UserID: "7", PlayerID: "Alice-7"},
			wantErr: true,
		},
		{
			name:    "human nonnumeric ID",
			player:  contracts.MatchPlayerRecord{UserID: "alice", PlayerID: "alice"},
			wantErr: true,
		},
		{
			name:    "AI with user ID",
			player:  contracts.MatchPlayerRecord{UserID: "ai_001", PlayerID: "ai_001", IsAI: true},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMatchPlayerIdentity(tt.player)
			if tt.wantErr && !errors.Is(err, ErrInvalidPlayerIdentity) {
				t.Fatalf("validateMatchPlayerIdentity() error = %v", err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validateMatchPlayerIdentity() error = %v", err)
			}
		})
	}
}
