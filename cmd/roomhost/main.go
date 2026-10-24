package main

import (
	"log"

	"github.com/nciyuan9264/game-platform/internal/plugins"
	"github.com/nciyuan9264/game-platform/internal/roomhostapp"
)

// This all-games entrypoint is for local development and integration only.
func main() {
	if err := roomhostapp.RunRegistry(plugins.Registry()); err != nil {
		log.Fatal(err)
	}
}
