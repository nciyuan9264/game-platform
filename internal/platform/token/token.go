package token

import (
	"context"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

type Verifier interface {
	Verify(ctx context.Context, token string) (contracts.UserIdentity, error)
}

func LegacyPlayerID(identity contracts.UserIdentity) string {
	return identity.UserID
}
