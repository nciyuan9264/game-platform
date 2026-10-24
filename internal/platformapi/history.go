package platformapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/nciyuan9264/game-platform/internal/history"
	"github.com/nciyuan9264/game-platform/internal/playerprofile"
)

func (s *Server) historyGames(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	identity, ok := s.requireIdentity(w, r)
	if !ok {
		return
	}
	userID, err := strconv.ParseInt(identity.UserID, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_user_id", err)
		return
	}
	gameType := historyGameType(r)
	limit := queryInt(r, "limit", 20)
	offset := queryInt(r, "offset", 0)
	games, err := s.store.ListHistoryGames(r.Context(), userID, gameType, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "history_error", err)
		return
	}
	s.resolveHistoryGames(r.Context(), games)
	writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Message: "ok", Data: map[string]any{"games": games}})
}

func (s *Server) historyGame(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	identity, ok := s.requireIdentity(w, r)
	if !ok {
		return
	}
	userID, err := strconv.ParseInt(identity.UserID, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_user_id", err)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/platform/history/games/"), "/")
	parts := strings.Split(path, "/")
	gameID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_game_id", err)
		return
	}
	includeState := len(parts) > 1 && (parts[1] == "snapshot" || parts[1] == "snapshots")
	gameRecord, players, events, err := s.store.GetCompletedHistoryGameForUser(r.Context(), gameID, userID, historyGameType(r), includeState)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err)
		return
	}
	s.resolveHistoryPlayers(r.Context(), players)
	displayNames := historyDisplayNames(players)
	for index := range events {
		events[index].DisplayName = displayNames[events[index].PlayerID]
	}
	if len(parts) == 1 {
		metas := make([]history.EventMeta, 0, len(events))
		for _, event := range events {
			metas = append(metas, history.EventMeta{
				Seq:         event.Seq,
				PlayerID:    event.PlayerID,
				DisplayName: event.DisplayName,
				CommandType: event.CommandType,
			})
		}
		writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Message: "ok", Data: map[string]any{
			"game": gameRecord, "players": players, "events": metas,
		}})
		return
	}
	switch parts[1] {
	case "snapshot":
		target := queryInt(r, "seq", -1)
		snapshot, err := acquireSnapshot(gameRecord, players, events, target)
		if err != nil {
			writeError(w, http.StatusInternalServerError, materializedReplayErrorCode(err), err)
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Message: "ok", Data: snapshot})
	case "snapshots":
		snapshots := make([]any, 0, len(events)+1)
		initial, err := acquireSnapshot(gameRecord, players, events, -1)
		if err != nil {
			writeError(w, http.StatusInternalServerError, materializedReplayErrorCode(err), err)
			return
		}
		snapshots = append(snapshots, initial)
		for _, event := range events {
			snapshot, err := acquireSnapshot(gameRecord, players, events, event.Seq)
			if err != nil {
				writeError(w, http.StatusInternalServerError, materializedReplayErrorCode(err), err)
				return
			}
			snapshots = append(snapshots, snapshot)
		}
		writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Message: "ok", Data: map[string]any{
			"totalEvents": len(events), "snapshots": snapshots,
		}})
	default:
		writeJSON(w, http.StatusNotFound, apiResponse{StatusCode: http.StatusNotFound, Error: "not_found", Message: "history endpoint not found"})
	}
}

func (s *Server) historyStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	identity, ok := s.requireIdentity(w, r)
	if !ok {
		return
	}
	userID, err := strconv.ParseInt(identity.UserID, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_user_id", err)
		return
	}
	stats, err := s.store.HistoryStats(r.Context(), userID, historyGameType(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "history_error", err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Message: "ok", Data: stats})
}

func (s *Server) rankings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	gameType := historyGameType(r)
	entries, err := s.store.HistoryLeaderboard(r.Context(), gameType, queryInt(r, "limit", 50), queryInt(r, "offset", 0))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "history_error", err)
		return
	}
	s.resolveLeaderboard(r.Context(), entries)
	writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Message: "ok", Data: map[string]any{
		"gameType": gameType, "entries": entries,
	}})
}

func acquireSnapshot(gameRecord history.Game, players []history.Player, events []history.Event, targetSeq int) (any, error) {
	var current *history.Event
	payload := gameRecord.InitialReplayPayload
	if targetSeq >= 0 {
		for index := range events {
			event := &events[index]
			if event.Seq > targetSeq {
				break
			}
			current = event
		}
		if current == nil {
			return acquireSnapshot(gameRecord, players, events, -1)
		}
		payload = current.ReplayPayload
	}
	encoded, err := history.MaterializedReplayJSON(
		gameRecord.ReplaySchemaVersion,
		payload,
		targetSeq,
		len(events),
		current,
		players,
	)
	if err != nil {
		return nil, err
	}
	var snapshot any
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func materializedReplayErrorCode(err error) string {
	switch {
	case errors.Is(err, history.ErrReplayMaterializationMissing):
		return "replay_materialization_missing"
	case errors.Is(err, history.ErrReplaySchemaUnsupported):
		return "replay_schema_unsupported"
	default:
		return "replay_error"
	}
}

func historyGameType(r *http.Request) string {
	value := r.URL.Query().Get("gameType")
	if value == "" {
		value = r.URL.Query().Get("game_type")
	}
	if value == "" {
		value = "acquire"
	}
	return value
}

func queryInt(r *http.Request, key string, fallback int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return fallback
	}
	return value
}

func (s *Server) resolveHistoryGames(ctx context.Context, games []history.Game) {
	subjects := make([]playerprofile.Player, 0)
	for _, gameRecord := range games {
		subjects = append(subjects, historyProfileSubjects(gameRecord.Players)...)
	}
	displayNames := s.profiles.Resolve(ctx, subjects)
	for gameIndex := range games {
		for playerIndex := range games[gameIndex].Players {
			player := &games[gameIndex].Players[playerIndex]
			player.DisplayName = displayNames[player.PlayerID]
		}
	}
}

func (s *Server) resolveHistoryPlayers(ctx context.Context, players []history.Player) {
	displayNames := s.profiles.Resolve(ctx, historyProfileSubjects(players))
	for index := range players {
		players[index].DisplayName = displayNames[players[index].PlayerID]
	}
}

func (s *Server) resolveLeaderboard(ctx context.Context, entries []history.LeaderboardEntry) {
	subjects := make([]playerprofile.Player, 0, len(entries))
	for _, entry := range entries {
		subjects = append(subjects, playerprofile.Player{
			UserID:      strconv.FormatInt(entry.UserID, 10),
			PlayerID:    entry.PlayerID,
			DisplayName: entry.DisplayName,
		})
	}
	displayNames := s.profiles.Resolve(ctx, subjects)
	for index := range entries {
		entries[index].DisplayName = displayNames[entries[index].PlayerID]
	}
}

func historyProfileSubjects(players []history.Player) []playerprofile.Player {
	subjects := make([]playerprofile.Player, 0, len(players))
	for _, player := range players {
		userID := ""
		if player.UserID != nil {
			userID = strconv.FormatInt(*player.UserID, 10)
		}
		subjects = append(subjects, playerprofile.Player{
			UserID:      userID,
			PlayerID:    player.PlayerID,
			DisplayName: player.DisplayName,
			IsAI:        player.IsAI,
		})
	}
	return subjects
}

func historyDisplayNames(players []history.Player) map[string]string {
	displayNames := make(map[string]string, len(players))
	for _, player := range players {
		displayNames[player.PlayerID] = player.DisplayName
	}
	return displayNames
}
