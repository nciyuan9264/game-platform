package main

import (
	"log"

	"github.com/nciyuan9264/game-platform/internal/othello"
	"github.com/nciyuan9264/game-platform/internal/roomhostapp"
)

func main() {
	if err := roomhostapp.Run(othello.Definition()); err != nil {
		log.Fatal(err)
	}
}
