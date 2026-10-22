package control

import (
	"context"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/observability"
	platformcontrol "github.com/nciyuan9264/game-platform/kitex_gen/pam/platform/control/v1"
)

var beijing = time.FixedZone("CST", 8*60*60)

func (s *Server) StartDailyCleanup(ctx context.Context) {
	go func() {
		for {
			timer := time.NewTimer(durationUntilNext4AM(time.Now()))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			rooms, err := s.store.ListRooms(ctx, "")
			if err != nil {
				observability.Warn("daily room cleanup list failed", "error", err)
				continue
			}
			handler := NewRPCHandler(s)
			systemIdentity := contracts.UserIdentity{UserID: "system", Roles: []string{"system"}}
			for _, room := range rooms {
				if contracts.IsTerminalRoomStatus(room.Status) || room.Status == contracts.RoomStatusCreating {
					continue
				}
				_, err := handler.finishRoomAs(ctx, &platformcontrol.RoomActionReq{
					RoomId:      room.RoomID,
					ActorUserId: "system",
					Reason:      stringPointer("daily_4am_cleanup"),
				}, contracts.RoomStatusClosed, systemIdentity)
				if err != nil {
					observability.Warn("daily room cleanup failed", "roomID", room.RoomID, "error", err)
				}
			}
			if err := s.store.CleanupOperationalData(ctx, time.Now().UTC()); err != nil {
				observability.Warn("operational data cleanup failed", "error", err)
			}
		}
	}()
}

func durationUntilNext4AM(now time.Time) time.Duration {
	local := now.In(beijing)
	next := time.Date(local.Year(), local.Month(), local.Day(), 4, 0, 0, 0, beijing)
	if !local.Before(next) {
		next = next.Add(24 * time.Hour)
	}
	return next.Sub(local)
}

func stringPointer(value string) *string {
	return &value
}
