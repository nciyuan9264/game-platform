package main

import (
	"log"

	"github.com/nciyuan9264/game-platform/internal/fugitive"
	"github.com/nciyuan9264/game-platform/internal/roomhostapp"
)

func main() {
	if err := roomhostapp.Run(fugitive.Definition()); err != nil {
		log.Fatal(err)
	}
}
