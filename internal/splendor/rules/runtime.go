package rules

import (
	"encoding/json"

	"github.com/nciyuan9264/game-platform-server/internal/splendor/domain"
)

const winningScore = 15

func HandleTurnTimeoutMessage(room *domain.Room, command domain.Command) {
	if room == nil || room.State == nil || room.State.RoomStatus == domain.RoomStatusEnd ||
		room.State.CurrentPlayer != command.PlayerID {
		return
	}
	room.State.LastData = &domain.LastAction{
		Action: "turn_timeout", PlayerID: command.PlayerID, Payload: json.RawMessage("null"),
	}
	AdvanceTurn(room)
}

func AdvanceTurn(room *domain.Room) {
	if anyPlayerReachedTarget(room) && room.State.RoomStatus == domain.RoomStatusPlaying {
		room.State.RoomStatus = domain.RoomStatusLastTurn
	}
	switchToNextPlayer(room)
	if room.State.RoomStatus == domain.RoomStatusLastTurn &&
		room.State.CurrentPlayer == room.State.FirstPlayer {
		room.State.RoomStatus = domain.RoomStatusEnd
	}
}

func switchToNextPlayer(room *domain.Room) {
	if len(room.PlayerSeq) == 0 {
		return
	}
	for index, playerID := range room.PlayerSeq {
		if playerID == room.State.CurrentPlayer {
			room.State.CurrentPlayer = room.PlayerSeq[(index+1)%len(room.PlayerSeq)]
			return
		}
	}
	room.State.CurrentPlayer = room.PlayerSeq[0]
}

func anyPlayerReachedTarget(room *domain.Room) bool {
	for _, player := range room.State.Players {
		if player != nil && player.Score >= winningScore {
			return true
		}
	}
	return false
}

func isTurnPlayable(room *domain.Room) bool {
	return room.State.RoomStatus == domain.RoomStatusPlaying ||
		room.State.RoomStatus == domain.RoomStatusLastTurn
}
