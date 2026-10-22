package platformapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
	rules "github.com/nciyuan9264/game-platform/internal/acquire/rules"
	"github.com/nciyuan9264/game-platform/internal/history"
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
	if len(parts) == 1 {
		metas := make([]history.EventMeta, 0, len(events))
		for _, event := range events {
			metas = append(metas, history.EventMeta{Seq: event.Seq, PlayerID: event.PlayerID, CommandType: event.CommandType})
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
			writeError(w, http.StatusInternalServerError, "replay_error", err)
			return
		}
		writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Message: "ok", Data: snapshot})
	case "snapshots":
		snapshots := make([]any, 0, len(events)+1)
		initial, err := acquireSnapshot(gameRecord, players, events, -1)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "replay_error", err)
			return
		}
		snapshots = append(snapshots, initial)
		for _, event := range events {
			snapshot, err := acquireSnapshot(gameRecord, players, events, event.Seq)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "replay_error", err)
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
	writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Message: "ok", Data: map[string]any{
		"gameType": gameType, "entries": entries,
	}})
}

func acquireSnapshot(gameRecord history.Game, players []history.Player, events []history.Event, targetSeq int) (any, error) {
	var state *domain.GameState
	result := map[string]any{}
	var current *history.Event
	if targetSeq < 0 {
		state = &domain.GameState{}
		if err := json.Unmarshal(gameRecord.InitialState, state); err != nil {
			return nil, err
		}
	} else {
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
		decoded, derived, ok := rules.DecodeStateSnapshot(current.StateBlob)
		if !ok {
			decoded, derived, ok = rules.DecodeStateSnapshot(current.StateSnapshot)
		}
		if !ok {
			return nil, strconv.ErrSyntax
		}
		state = decoded
		result = derived
	}
	playerMeta := map[string]any{}
	playersData := map[string]any{}
	for _, player := range players {
		playerMeta[player.PlayerID] = map[string]any{
			"playerID": player.PlayerID,
			"online":   false,
			"ai":       player.IsAI,
		}
	}
	for playerID, player := range state.Players {
		playersData[playerID] = player
	}
	snapshot := map[string]any{
		"seq":         targetSeq,
		"totalEvents": len(events),
		"roomData": map[string]any{
			"companyInfo":   state.Companies,
			"currentPlayer": state.CurrentPlayer,
			"gameStatus":    state.RoomStatus,
			"tiles":         state.BoardTiles,
			"players":       playerMeta,
		},
		"playersData": playersData,
		"result":      result,
	}
	if current != nil {
		var payload any
		_ = json.Unmarshal(current.Payload, &payload)
		snapshot["currentEvent"] = map[string]any{
			"seq": current.Seq, "playerID": current.PlayerID, "cmdType": current.CommandType, "payload": payload,
		}
	}
	return snapshot, nil
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
