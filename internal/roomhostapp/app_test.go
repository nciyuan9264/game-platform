package roomhostapp

import (
	"context"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/plugins/acquire"
	"github.com/nciyuan9264/game-platform/internal/plugins/davinci"
	"github.com/nciyuan9264/game-platform/internal/plugins/fugitive"
	"github.com/nciyuan9264/game-platform/internal/plugins/loveletter"
	"github.com/nciyuan9264/game-platform/internal/plugins/onitama"
	"github.com/nciyuan9264/game-platform/internal/plugins/othello"
	"github.com/nciyuan9264/game-platform/internal/plugins/quoridor"
	"github.com/nciyuan9264/game-platform/internal/plugins/sixnimmt"
	"github.com/nciyuan9264/game-platform/internal/plugins/splendor"
	"github.com/nciyuan9264/game-platform/internal/plugins/warchest"
	"github.com/nciyuan9264/game-platform/internal/plugins/yinsh"
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
		fugitive.Definition(),
		warchest.Definition(),
		quoridor.Definition(),
		loveletter.Definition(),
		sixnimmt.Definition(),
		onitama.Definition(),
	}
	for _, definition := range definitions {
		t.Run(definition.GameType, func(t *testing.T) {
			if err := SelfTest(context.Background(), definition); err != nil {
				t.Fatal(err)
			}
		})
	}
}
