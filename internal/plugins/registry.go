package plugins

import (
	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/plugins/acquire"
	"github.com/nciyuan9264/game-platform/internal/plugins/cantstop"
	"github.com/nciyuan9264/game-platform/internal/plugins/davinci"
	"github.com/nciyuan9264/game-platform/internal/plugins/forsale"
	"github.com/nciyuan9264/game-platform/internal/plugins/fugitive"
	"github.com/nciyuan9264/game-platform/internal/plugins/hex"
	"github.com/nciyuan9264/game-platform/internal/plugins/hive"
	"github.com/nciyuan9264/game-platform/internal/plugins/loveletter"
	"github.com/nciyuan9264/game-platform/internal/plugins/onitama"
	"github.com/nciyuan9264/game-platform/internal/plugins/othello"
	"github.com/nciyuan9264/game-platform/internal/plugins/quoridor"
	"github.com/nciyuan9264/game-platform/internal/plugins/sixnimmt"
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
		forsale.Definition(),
		hive.Definition(),
		loveletter.Definition(),
		sixnimmt.Definition(),
		warchest.Definition(),
		quoridor.Definition(),
		hex.Definition(),
		onitama.Definition(),
		cantstop.Definition(),
	)
}
