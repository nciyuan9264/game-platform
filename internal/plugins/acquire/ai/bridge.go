package roompkg

import "github.com/nciyuan9264/game-platform/internal/plugins/acquire/domain/domain"

func DecideCommand(room *domain.Room, playerID string) (string, []byte, bool) {
	if room == nil || room.State == nil {
		return "", nil, false
	}
	return buildAIActionMsg(room, playerID, room.State.RoomStatus, nil)
}

func CloneRoom(room *domain.Room) *domain.Room {
	return cloneRoomForAISimulation(room)
}
