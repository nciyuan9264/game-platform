package main

import (
	"log"

	"github.com/nciyuan9264/game-platform/internal/plugins/yinsh"
	"github.com/nciyuan9264/game-platform/internal/roomhostapp"
)

func main() {
	if err := roomhostapp.Run(yinsh.Definition()); err != nil {
		log.Fatal(err)
	}
}
