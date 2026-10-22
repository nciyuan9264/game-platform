package roompkg

import (
	"fmt"
	"github.com/nciyuan9264/game-platform-server/internal/acquire/domain/domain"
)

func genAIPlayerID(r *domain.Room) string {
	for i := 1; ; i++ {
		id := fmt.Sprintf("ai_%03d", i)
		if _, ok := r.Connections[id]; !ok {
			return id
		}
	}
}

func min(a, b, c int) int {
	if a <= b && a <= c {
		return a
	}
	if b <= c {
		return b
	}
	return c
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}
