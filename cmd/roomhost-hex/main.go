package main

import (
	"log"

	"github.com/nciyuan9264/game-platform/internal/plugins/hex"
	"github.com/nciyuan9264/game-platform/internal/roomhostapp"
)

func main() {
	if err := roomhostapp.Run(hex.Definition()); err != nil {
		log.Fatal(err)
	}
}
