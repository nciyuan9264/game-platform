package plugins

import (
	"testing"

	"github.com/nciyuan9264/game-platform-server/internal/contracts"
)

func TestRegistryContainsAllGamePlugins(t *testing.T) {
	registry := Registry()
	for _, gameType := range []string{
		contracts.GameAcquire,
		contracts.GameDaVinci,
		contracts.GameSplendor,
	} {
		definition, ok := registry.Get(gameType)
		if !ok || definition.Engine == nil || definition.Version == "" ||
			definition.DefaultMaxPlayers <= 0 {
			t.Fatalf("%s definition = %+v, %v", gameType, definition, ok)
		}
	}
}
