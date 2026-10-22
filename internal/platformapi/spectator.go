package platformapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	storepkg "github.com/nciyuan9264/game-platform/internal/store"
)

const spectatorSessionTTL = 6 * time.Hour

type spectatorStore interface {
	CreateSpectatorSession(context.Context, contracts.SpectatorSession) error
	GetSpectatorSession(context.Context, string, string) (contracts.SpectatorSession, error)
	GetRoom(context.Context, string) (contracts.RoomRecord, error)
	GetMatch(context.Context, string) (contracts.MatchRecord, error)
	LatestAvailableSpectatorSnapshot(context.Context, string) (contracts.SpectatorSnapshotRecord, error)
	NextSpectatorSnapshotAvailability(context.Context, string) (time.Time, error)
}

func (s *Server) spectatorRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	identity, ok := s.requireIdentity(w, r)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/platform/spectator/rooms/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[1] != "sessions" {
		writeJSON(w, http.StatusNotFound, apiResponse{StatusCode: http.StatusNotFound, Error: "not_found", Message: "spectator endpoint not found"})
		return
	}
	roomID, err := url.PathUnescape(parts[0])
	if err != nil || roomID == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse{StatusCode: http.StatusBadRequest, Error: "bad_request", Message: "roomID is required"})
		return
	}
	room, err := s.spectators.GetRoom(r.Context(), roomID)
	if err != nil || room.CurrentMatchID == "" {
		writeError(w, http.StatusConflict, "spectator_unavailable", storepkg.ErrSpectatorUnavailable)
		return
	}
	now := time.Now().UTC()
	session := contracts.SpectatorSession{
		SessionID: uuid.NewString(),
		RoomID:    roomID,
		MatchID:   room.CurrentMatchID,
		UserID:    identity.UserID,
		CreatedAt: now,
		ExpiresAt: now.Add(spectatorSessionTTL),
	}
	if err := s.spectators.CreateSpectatorSession(r.Context(), session); err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeError(w, http.StatusNotFound, "not_found", err)
		case errors.Is(err, storepkg.ErrSpectatorUnavailable):
			writeError(w, http.StatusConflict, "spectator_unavailable", err)
		case errors.Is(err, storepkg.ErrSpectatorParticipant):
			writeError(w, http.StatusConflict, "spectator_participant", err)
		default:
			writeError(w, http.StatusInternalServerError, "store_error", err)
		}
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{
		StatusCode: http.StatusOK,
		Message:    "ok",
		Data: map[string]any{
			"sessionID":    session.SessionID,
			"roomID":       session.RoomID,
			"matchID":      session.MatchID,
			"delaySeconds": int(contracts.SpectatorDelay.Seconds()),
			"createdAt":    session.CreatedAt,
			"expiresAt":    session.ExpiresAt,
		},
	})
}

func (s *Server) spectatorSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	identity, ok := s.requireIdentity(w, r)
	if !ok {
		return
	}
	sessionID, err := url.PathUnescape(strings.Trim(strings.TrimPrefix(r.URL.Path, "/platform/spectator/sessions/"), "/"))
	if err != nil || sessionID == "" || strings.Contains(sessionID, "/") {
		writeJSON(w, http.StatusBadRequest, apiResponse{StatusCode: http.StatusBadRequest, Error: "bad_request", Message: "sessionID is required"})
		return
	}
	session, err := s.spectators.GetSpectatorSession(r.Context(), sessionID, identity.UserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", err)
		} else {
			writeError(w, http.StatusInternalServerError, "store_error", err)
		}
		return
	}
	room, err := s.spectators.GetRoom(r.Context(), session.RoomID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err)
		return
	}
	match, err := s.spectators.GetMatch(r.Context(), session.MatchID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err)
		return
	}
	afterSeq, _ := strconv.ParseInt(r.URL.Query().Get("afterSeq"), 10, 64)
	var (
		snapshot        any
		visibleSeq      int64
		capturedAt      time.Time
		availableAt     time.Time
		nextAvailableAt time.Time
	)
	latest, err := s.spectators.LatestAvailableSpectatorSnapshot(r.Context(), session.MatchID)
	switch {
	case err == nil:
		visibleSeq = latest.Seq
		capturedAt = latest.CapturedAt
		availableAt = latest.AvailableAt
		if latest.Seq > afterSeq {
			snapshot = latest.Payload
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		writeError(w, http.StatusInternalServerError, "store_error", err)
		return
	}
	if next, err := s.spectators.NextSpectatorSnapshotAvailability(r.Context(), session.MatchID); err == nil {
		nextAvailableAt = next
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, apiResponse{
		StatusCode: http.StatusOK,
		Message:    "ok",
		Data: map[string]any{
			"sessionID":       session.SessionID,
			"roomID":          session.RoomID,
			"matchID":         session.MatchID,
			"gameType":        room.GameType,
			"roomStatus":      room.Status,
			"delaySeconds":    int(contracts.SpectatorDelay.Seconds()),
			"liveSeq":         match.LastAppliedSeq,
			"visibleSeq":      visibleSeq,
			"capturedAt":      optionalSpectatorTime(capturedAt),
			"availableAt":     optionalSpectatorTime(availableAt),
			"nextAvailableAt": optionalSpectatorTime(nextAvailableAt),
			"snapshot":        snapshot,
		},
	})
}

func optionalSpectatorTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
