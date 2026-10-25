package roomhostapp

import (
	"context"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/acquire"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/davinci"
	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/othello"
	"github.com/nciyuan9264/game-platform/internal/splendor"
	"github.com/nciyuan9264/game-platform/internal/yinsh"
)

func TestSingleGameRegistryContainsOnlyRequestedDefinition(t *testing.T) {
	registry, err := SingleGameRegistry(acquire.Definition())
	if err != nil {
		t.Fatal(err)
	}
	definitions := registry.Definitions()
	if len(definitions) != 1 {
		t.Fatalf("definitions = %d, want 1", len(definitions))
	}
	if definitions[0].GameType != contracts.GameAcquire ||
		definitions[0].Version != acquire.EngineVersion {
		t.Fatalf("definition = %+v", definitions[0])
	}
	if versions := registry.Versions(); len(versions) != 1 ||
		versions[contracts.GameAcquire] != acquire.EngineVersion {
		t.Fatalf("versions = %+v", versions)
	}
}

func TestSelfTestCoversProductionDefinitions(t *testing.T) {
	definitions := []game.Definition{
		acquire.Definition(),
		davinci.Definition(),
		splendor.Definition(),
		yinsh.Definition(),
		othello.Definition(),
	}
	for _, definition := range definitions {
		t.Run(definition.GameType, func(t *testing.T) {
			if err := SelfTest(context.Background(), definition); err != nil {
				t.Fatal(err)
			}
		})
	}
}
