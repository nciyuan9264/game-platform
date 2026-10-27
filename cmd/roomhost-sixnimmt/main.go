package main

import (
	"log"

	"github.com/nciyuan9264/game-platform/internal/plugins/sixnimmt"
	"github.com/nciyuan9264/game-platform/internal/roomhostapp"
)

func main() {
	if err := roomhostapp.Run(sixnimmt.Definition()); err != nil {
		log.Fatal(err)
	}
}
