package roompkg

import "github.com/nciyuan9264/game-platform-server/internal/acquire/domain/domain"

func DecideCommand(room *domain.Room, playerID string) (string, []byte, bool) {
	if room == nil || room.State == nil {
		return "", nil, false
	}
	return buildAIActionMsg(room, playerID, room.State.RoomStatus, nil)
}

func CloneRoom(room *domain.Room) *domain.Room {
	return cloneRoomForAISimulation(room)
}

func NextAIPlayerID(room *domain.Room) string {
	return genAIPlayerID(room)
}
