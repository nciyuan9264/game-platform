package main

import (
	"log"

	"github.com/nciyuan9264/game-platform/internal/plugins/davinci"
	"github.com/nciyuan9264/game-platform/internal/roomhostapp"
)

func main() {
	if err := roomhostapp.Run(davinci.Definition()); err != nil {
		log.Fatal(err)
	}
}
