package plugins

import (
	"github.com/nciyuan9264/game-platform-server/internal/acquire"
	"github.com/nciyuan9264/game-platform-server/internal/davinci"
	"github.com/nciyuan9264/game-platform-server/internal/game"
	"github.com/nciyuan9264/game-platform-server/internal/splendor"
)

func Registry() *game.Registry {
	return game.NewRegistry(
		davinci.Definition(),
		acquire.Definition(),
		splendor.Definition(),
	)
}
