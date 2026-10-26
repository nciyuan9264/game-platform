package main

import (
	"log"

	"github.com/nciyuan9264/game-platform/internal/plugins/splendor"
	"github.com/nciyuan9264/game-platform/internal/roomhostapp"
)

func main() {
	if err := roomhostapp.Run(splendor.Definition()); err != nil {
		log.Fatal(err)
	}
}
