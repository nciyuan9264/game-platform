package main

import (
	"log"

	"github.com/nciyuan9264/game-platform/internal/plugins/hive"
	"github.com/nciyuan9264/game-platform/internal/roomhostapp"
)

func main() {
	if err := roomhostapp.Run(hive.Definition()); err != nil {
		log.Fatal(err)
	}
}
