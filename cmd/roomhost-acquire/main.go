package main

import (
	"log"

	"github.com/nciyuan9264/game-platform/internal/plugins/acquire"
	"github.com/nciyuan9264/game-platform/internal/roomhostapp"
)

func main() {
	if err := roomhostapp.Run(acquire.Definition()); err != nil {
		log.Fatal(err)
	}
}
