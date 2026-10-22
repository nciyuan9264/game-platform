package game

import (
	"context"
	"errors"
	"testing"

	"github.com/nciyuan9264/game-platform-server/internal/contracts"
)

type testEngine struct {
	gameType string
	version  string
}

func (e testEngine) GameType() string { return e.gameType }

func (e testEngine) Version() string { return e.version }

func (e testEngine) InitRoom(ctx context.Context, cfg RoomConfig) (State, error) {
	return map[string]any{"roomID": cfg.RoomID}, nil
}

func (e testEngine) EncodeState(ctx context.Context, state State) ([]byte, error) {
	return []byte(`{}`), nil
}

func (e testEngine) DecodeState(ctx context.Context, data []byte) (State, error) {
	return map[string]any{}, nil
}

func (e testEngine) ApplyCommand(ctx context.Context, state State, cmd contracts.RoomCommand) (ApplyResult, error) {
	return ApplyResult{}, nil
}

func (e testEngine) BuildPlayerView(ctx context.Context, state State, req ViewRequest) (any, error) {
	return map[string]any{"playerID": req.PlayerID}, nil
}

func TestRegistryUsesDefinitionDefaultsFromEngine(t *testing.T) {
	registry := NewRegistry(Definition{Engine: testEngine{gameType: "test-game", version: "test-v1"}})

	def, ok := registry.Get("test-game")
	if !ok {
		t.Fatalf("definition not registered")
	}
	if def.GameType != "test-game" || def.Version != "test-v1" {
		t.Fatalf("unexpected definition: %+v", def)
	}
	if def.DefaultMaxPlayers != 4 {
		t.Fatalf("default max players = %d, want 4", def.DefaultMaxPlayers)
	}

	defaultDef, ok := registry.DefaultDefinition()
	if !ok || defaultDef.GameType != "test-game" {
		t.Fatalf("unexpected default definition: %+v ok=%v", defaultDef, ok)
	}
}

func TestRegistryRejectsInvalidDefinition(t *testing.T) {
	err := NewRegistry().Register(Definition{})
	if !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("Register() error = %v, want ErrInvalidDefinition", err)
	}
}
