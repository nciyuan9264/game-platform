package roompkg

import (
	"github.com/nciyuan9264/game-platform-server/internal/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform-server/internal/acquire/rules"
)

func simulateAction(r *domain.Room, playerID string, action aiAction) (*domain.Room, bool) {
	sim := cloneRoomForAISimulation(r)
	if sim == nil || sim.State == nil {
		return nil, false
	}
	cmdType, payload, ok := payloadForAction(action)
	if !ok {
		return nil, false
	}
	cmd := domain.Command{Type: cmdType, PlayerID: playerID, Payload: payload}
	switch action.Kind {
	case aiActionPlaceTile:
		game.HandlePlaceTileMessage(sim, cmd)
	case aiActionCreateCompany:
		game.HandleCreateCompanyMessage(sim, cmd)
	case aiActionBuyStock:
		game.HandleBuyStockMessage(sim, cmd)
	case aiActionMergeSelection:
		game.HandleMergingSelectionMessage(sim, cmd)
	case aiActionMergeSettle:
		game.HandleMergingSettleMessage(sim, cmd)
	default:
		return nil, false
	}
	game.RecomputeDerivedState(sim)
	return sim, true
}
