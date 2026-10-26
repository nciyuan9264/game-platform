package main

import (
	"log"

	"github.com/nciyuan9264/game-platform/internal/plugins/warchest"
	"github.com/nciyuan9264/game-platform/internal/roomhostapp"
)

func main() {
	if err := roomhostapp.Run(warchest.Definition()); err != nil {
		log.Fatal(err)
	}
}
