package rpc

import (
	"context"
	"errors"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/store"
)

type rpcTestGameCatalog struct {
	entry contracts.GameCatalogEntry
	err   error
}

func (c rpcTestGameCatalog) GetGameCatalogEntry(context.Context, string) (contracts.GameCatalogEntry, error) {
	return c.entry, c.err
}

func TestValidGameTypeAllowsDisabledExistingGame(t *testing.T) {
	handler := &Handler{gameCatalog: rpcTestGameCatalog{entry: contracts.GameCatalogEntry{
		GameType:            contracts.GameAcquire,
		EnabledForNewRooms:  false,
		ActiveEngineVersion: "acquire-v2",
		DefaultMaxPlayers:   6,
		ReplaySchemaVersion: 1,
	}}}
	got, err := handler.validGameType(context.Background(), contracts.GameAcquire)
	if err != nil || got != contracts.GameAcquire {
		t.Fatalf("validGameType() = %q, %v", got, err)
	}
}

func TestValidGameTypeForCreateRejectsDisabledGame(t *testing.T) {
	handler := &Handler{gameCatalog: rpcTestGameCatalog{entry: contracts.GameCatalogEntry{
		GameType:            contracts.GameAcquire,
		EnabledForNewRooms:  false,
		ActiveEngineVersion: "acquire-v2",
		DefaultMaxPlayers:   6,
		ReplaySchemaVersion: 1,
	}}}
	_, err := handler.validGameTypeForCreate(context.Background(), contracts.GameAcquire)
	var platformErr interface{ GetCode() string }
	if !errors.As(err, &platformErr) || platformErr.GetCode() != "game_disabled" {
		t.Fatalf("validGameTypeForCreate() error = %v, want game_disabled", err)
	}
}

func TestValidGameTypeRejectsMissingCatalogEntry(t *testing.T) {
	handler := &Handler{gameCatalog: rpcTestGameCatalog{err: store.ErrGameCatalogNotFound}}
	_, err := handler.validGameType(context.Background(), "missing")
	var platformErr interface{ GetCode() string }
	if !errors.As(err, &platformErr) || platformErr.GetCode() != "invalid_game_type" {
		t.Fatalf("validGameType() error = %v, want invalid_game_type", err)
	}
}
