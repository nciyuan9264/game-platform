package main

import (
	"log"

	"github.com/nciyuan9264/game-platform/internal/roomhostapp"
	"github.com/nciyuan9264/game-platform/internal/splendor"
)

func main() {
	if err := roomhostapp.Run(splendor.Definition()); err != nil {
		log.Fatal(err)
	}
}
