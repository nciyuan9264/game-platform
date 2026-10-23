package store

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

type roomScannerFunc func(dest ...any) error

func (scan roomScannerFunc) Scan(dest ...any) error {
	return scan(dest...)
}

func TestScanRoomIncludesVisibility(t *testing.T) {
	record, err := scanRoom(roomScannerFunc(func(dest ...any) error {
		if len(dest) != 19 {
			t.Fatalf("scan destinations = %d, want 19", len(dest))
		}
		*dest[0].(*string) = "room-1"
		*dest[1].(*string) = "acquire"
		*dest[2].(*string) = "private"
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if record.Visibility != "private" {
		t.Fatalf("visibility = %q, want private", record.Visibility)
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
