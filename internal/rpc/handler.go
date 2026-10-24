package rpc

import (
	"context"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/playerprofile"
	"github.com/nciyuan9264/game-platform/internal/store"
)

type roomController interface {
	CreateRoomWithVisibility(
		context.Context,
		string,
		string,
		string,
		string,
		int,
		string,
	) (contracts.RoomRecord, error)
	StartMatch(context.Context, string, string, []string, string) (contracts.RoomRecord, string, int, error)
	CloseRoom(context.Context, string, string, []string, string) (contracts.RoomRecord, error)
}

type gameCatalog interface {
	GetGameCatalogEntry(context.Context, string) (contracts.GameCatalogEntry, error)
}

type Handler struct {
	store       *store.Store
	rooms       roomController
	gameCatalog gameCatalog
	profiles    *playerprofile.Resolver
}

func NewHandler(st *store.Store, rooms roomController, profiles *playerprofile.Resolver) *Handler {
	return &Handler{
		store:       st,
		rooms:       rooms,
		gameCatalog: st,
		profiles:    profiles,
	}
}
