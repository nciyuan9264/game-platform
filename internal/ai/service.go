package ai

import (
	"context"

	"github.com/nciyuan9264/game-platform-server/internal/contracts"
	"github.com/nciyuan9264/game-platform-server/internal/davinci"
	"github.com/nciyuan9264/game-platform-server/internal/game"
)

type Service struct {
	planner *davinci.AIPlanner
}

func NewService() *Service {
	return &Service{planner: davinci.NewAIPlanner()}
}

func (s *Service) Decide(ctx context.Context, req game.AIRequest) (contracts.RoomCommand, error) {
	return s.planner.Decide(ctx, req)
}
