package main

import (
	"log"

	"github.com/nciyuan9264/game-platform/internal/roomhostapp"
	"github.com/nciyuan9264/game-platform/internal/warchest"
)

func main() {
	if err := roomhostapp.Run(warchest.Definition()); err != nil {
		log.Fatal(err)
	}
}
