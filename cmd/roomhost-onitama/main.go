package main

import (
	"log"

	"github.com/nciyuan9264/game-platform/internal/plugins/onitama"
	"github.com/nciyuan9264/game-platform/internal/roomhostapp"
)

func main() {
	if err := roomhostapp.Run(onitama.Definition()); err != nil {
		log.Fatal(err)
	}
}
