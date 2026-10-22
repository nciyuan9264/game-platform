package store

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

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
