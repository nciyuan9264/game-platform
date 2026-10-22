package node

import (
	"context"
	"time"

	"github.com/nciyuan9264/game-platform-server/internal/contracts"
	"github.com/nciyuan9264/game-platform-server/internal/store"
)

func Heartbeat(ctx context.Context, st *store.Store, ttl time.Duration, snapshot func() contracts.NodeInfo) {
	ticker := time.NewTicker(ttl / 2)
	defer ticker.Stop()
	for {
		_ = st.UpsertNode(ctx, snapshot(), ttl)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
