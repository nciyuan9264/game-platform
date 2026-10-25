package plugins

import (
	"github.com/nciyuan9264/game-platform/internal/acquire"
	"github.com/nciyuan9264/game-platform/internal/davinci"
	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/splendor"
	"github.com/nciyuan9264/game-platform/internal/yinsh"
)

func Registry() *game.Registry {
	return game.NewRegistry(
		davinci.Definition(),
		acquire.Definition(),
		splendor.Definition(),
		yinsh.Definition(),
	)
}
