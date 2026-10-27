package main

import (
	"log"

	"github.com/nciyuan9264/game-platform/internal/plugins/cantstop"
	"github.com/nciyuan9264/game-platform/internal/roomhostapp"
)

func main() {
	if err := roomhostapp.Run(cantstop.Definition()); err != nil {
		log.Fatal(err)
	}
}
