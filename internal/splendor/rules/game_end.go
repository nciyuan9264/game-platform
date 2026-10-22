package rules

import (
	"log"

	"github.com/nciyuan9264/game-platform-server/internal/splendor/domain"
)

func HandleGameEndMessage(r *domain.Room, cmd domain.Command) {
	r.State.RoomStatus = domain.RoomStatusEnd
	log.Printf("✅ 游戏结束 room=%s", r.ID)
}
