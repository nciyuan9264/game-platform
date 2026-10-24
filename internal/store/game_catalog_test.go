package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestValidateGameCatalogEntry(t *testing.T) {
	valid := contracts.GameCatalogEntry{
		GameType:            "test-game",
		ActiveEngineVersion: "test-v1",
		DefaultMaxPlayers:   4,
		ReplaySchemaVersion: 1,
		EnabledForNewRooms:  true,
	}
	if err := validateGameCatalogEntry(valid); err != nil {
		t.Fatalf("validateGameCatalogEntry() error = %v", err)
	}

	tests := []struct {
		name  string
		entry contracts.GameCatalogEntry
	}{
		{name: "missing game", entry: contracts.GameCatalogEntry{ActiveEngineVersion: "v1", DefaultMaxPlayers: 2, ReplaySchemaVersion: 1}},
		{name: "missing version", entry: contracts.GameCatalogEntry{GameType: "game", DefaultMaxPlayers: 2, ReplaySchemaVersion: 1}},
		{name: "invalid players", entry: contracts.GameCatalogEntry{GameType: "game", ActiveEngineVersion: "v1", ReplaySchemaVersion: 1}},
		{name: "invalid replay schema", entry: contracts.GameCatalogEntry{GameType: "game", ActiveEngineVersion: "v1", DefaultMaxPlayers: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateGameCatalogEntry(tt.entry); !errors.Is(err, ErrInvalidGameCatalog) {
				t.Fatalf("validateGameCatalogEntry() error = %v, want ErrInvalidGameCatalog", err)
			}
		})
	}
}

func TestGameCatalogLifecycleIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	st, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		_ = st.Close()
		t.Fatal(err)
	}

	gameType := fmt.Sprintf("catalog-test-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = st.db.ExecContext(context.Background(), `DELETE FROM game_catalog_events WHERE game_type = $1`, gameType)
		_, _ = st.db.ExecContext(context.Background(), `DELETE FROM game_catalog WHERE game_type = $1`, gameType)
		_ = st.Close()
	})

	input := contracts.GameCatalogEntry{
		GameType:            gameType,
		ActiveEngineVersion: "test-v1",
		DefaultMaxPlayers:   4,
		ReplaySchemaVersion: 1,
		EnabledForNewRooms:  true,
		IsDefault:           false,
	}
	registered, created, err := st.RegisterGameCatalogEntry(ctx, input, "sha-register", "integration-test")
	if err != nil || !created {
		t.Fatalf("RegisterGameCatalogEntry() created = %v, err = %v", created, err)
	}
	if registered.Revision != 1 || registered.ActiveEngineVersion != "test-v1" {
		t.Fatalf("registered entry = %+v", registered)
	}
	idempotent, created, err := st.RegisterGameCatalogEntry(ctx, input, "ignored", "integration-test")
	if err != nil || created || idempotent.Revision != registered.Revision {
		t.Fatalf("idempotent register = %+v, created = %v, err = %v", idempotent, created, err)
	}

	promoted, err := st.SetActiveGameVersion(
		ctx,
		gameType,
		"test-v2",
		"test-v1",
		contracts.GameCatalogActionPromote,
		"sha-promote",
		"integration-test",
	)
	if err != nil {
		t.Fatal(err)
	}
	if promoted.ActiveEngineVersion != "test-v2" || promoted.Revision != 2 {
		t.Fatalf("promoted entry = %+v", promoted)
	}
	_, err = st.SetActiveGameVersion(
		ctx,
		gameType,
		"test-v3",
		"test-v1",
		contracts.GameCatalogActionPromote,
		"sha-stale",
		"integration-test",
	)
	if !errors.Is(err, ErrGameCatalogConflict) {
		t.Fatalf("stale promotion error = %v, want ErrGameCatalogConflict", err)
	}

	disabled, changed, err := st.DisableGameCatalogEntry(ctx, gameType, "sha-disable", "integration-test")
	if err != nil || !changed || disabled.EnabledForNewRooms || disabled.IsDefault {
		t.Fatalf("disabled entry = %+v, changed = %v, err = %v", disabled, changed, err)
	}
	_, changed, err = st.DisableGameCatalogEntry(ctx, gameType, "ignored", "integration-test")
	if err != nil || changed {
		t.Fatalf("idempotent disable changed = %v, err = %v", changed, err)
	}

	events, err := st.ListGameCatalogEvents(ctx, gameType, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("catalog events = %d, want 3: %+v", len(events), events)
	}
	if events[0].Action != contracts.GameCatalogActionDisable ||
		events[1].Action != contracts.GameCatalogActionPromote ||
		events[2].Action != contracts.GameCatalogActionRegister {
		t.Fatalf("catalog event actions = %+v", events)
	}
	if events[1].Artifact != "sha-promote" || events[1].Actor != "integration-test" {
		t.Fatalf("promotion audit = %+v", events[1])
	}
}

func TestGameCatalogAllowsOnlyOneEnabledDefaultIntegration(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	st, err := NewPostgresStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	defer st.Close()

	gameType := fmt.Sprintf("catalog-default-test-%d", time.Now().UnixNano())
	_, _, err = st.RegisterGameCatalogEntry(ctx, contracts.GameCatalogEntry{
		GameType:            gameType,
		ActiveEngineVersion: "test-v1",
		DefaultMaxPlayers:   2,
		ReplaySchemaVersion: 1,
		EnabledForNewRooms:  true,
		IsDefault:           true,
	}, "", "integration-test")
	if err == nil {
		_, _ = st.db.ExecContext(ctx, `DELETE FROM game_catalog_events WHERE game_type = $1`, gameType)
		_, _ = st.db.ExecContext(ctx, `DELETE FROM game_catalog WHERE game_type = $1`, gameType)
		t.Fatal("registering a second enabled default game succeeded")
	}
}
