package rpc

import (
	"context"

	"github.com/nciyuan9264/game-platform-server/internal/contracts"
	"github.com/nciyuan9264/game-platform-server/internal/game"
	"github.com/nciyuan9264/game-platform-server/internal/store"
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

type Handler struct {
	store *store.Store
	rooms roomController
	games *game.Registry
}

func NewHandler(st *store.Store, rooms roomController, games *game.Registry) *Handler {
	if games == nil {
		games = game.NewRegistry()
	}
	return &Handler{store: st, rooms: rooms, games: games}
}
