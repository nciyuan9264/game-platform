package plugins

import (
	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/plugins/acquire"
	"github.com/nciyuan9264/game-platform/internal/plugins/davinci"
	"github.com/nciyuan9264/game-platform/internal/plugins/fugitive"
	"github.com/nciyuan9264/game-platform/internal/plugins/othello"
	"github.com/nciyuan9264/game-platform/internal/plugins/quoridor"
	"github.com/nciyuan9264/game-platform/internal/plugins/splendor"
	"github.com/nciyuan9264/game-platform/internal/plugins/warchest"
	"github.com/nciyuan9264/game-platform/internal/plugins/yinsh"
)

func Registry() *game.Registry {
	return game.NewRegistry(
		davinci.Definition(),
		acquire.Definition(),
		splendor.Definition(),
		yinsh.Definition(),
		othello.Definition(),
		fugitive.Definition(),
		warchest.Definition(),
		quoridor.Definition(),
	)
}
