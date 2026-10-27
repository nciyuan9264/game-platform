package main

import (
	"log"

	"github.com/nciyuan9264/game-platform/internal/plugins/loveletter"
	"github.com/nciyuan9264/game-platform/internal/roomhostapp"
)

func main() {
	if err := roomhostapp.Run(loveletter.Definition()); err != nil {
		log.Fatal(err)
	}
}
