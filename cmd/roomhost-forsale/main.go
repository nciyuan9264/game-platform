package main

import (
	"log"

	"github.com/nciyuan9264/game-platform/internal/plugins/forsale"
	"github.com/nciyuan9264/game-platform/internal/roomhostapp"
)

func main() {
	if err := roomhostapp.Run(forsale.Definition()); err != nil {
		log.Fatal(err)
	}
}
