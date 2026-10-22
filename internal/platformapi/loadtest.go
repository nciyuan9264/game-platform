package platformapi

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

const maxLoadtestRooms = 500
const minLoadtestTokenLength = 32

func (s *Server) loadtestRooms(w http.ResponseWriter, r *http.Request) {
	if !loadtestAuthorized(r) {
		writeJSON(w, http.StatusUnauthorized, apiResponse{StatusCode: http.StatusUnauthorized, Error: "unauthorized"})
		return
	}
	if _, ok := s.requireRole(w, r, "admin"); !ok {
		return
	}
	switch r.Method {
	case http.MethodPost:
		var req struct {
			Prefix      string `json:"prefix"`
			Count       int    `json:"count"`
			OwnerPrefix string `json:"ownerPrefix"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !strings.HasPrefix(req.Prefix, "lt-") || req.Count <= 0 || req.Count > maxLoadtestRooms {
			writeJSON(w, http.StatusBadRequest, apiResponse{StatusCode: http.StatusBadRequest, Error: "invalid_request"})
			return
		}
		if req.OwnerPrefix == "" {
			req.OwnerPrefix = req.Prefix + "-owner"
		}
		roomIDs := make([]string, 0, req.Count)
		for index := 1; index <= req.Count; index++ {
			owner := req.OwnerPrefix + "-" + leftPad(index, 6)
			room, err := s.rooms.CreateRoom(r.Context(), contracts.GameAcquire, owner, owner, req.Prefix+"-"+leftPad(index, 6), 6)
			if err != nil {
				writeRPCError(w, err, "control_unavailable")
				return
			}
			roomIDs = append(roomIDs, room.RoomID)
		}
		writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Data: map[string]any{"rooms": roomIDs}})
	case http.MethodDelete:
		var req struct {
			Prefix string `json:"prefix"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !strings.HasPrefix(req.Prefix, "lt-") {
			writeJSON(w, http.StatusBadRequest, apiResponse{StatusCode: http.StatusBadRequest, Error: "invalid_request"})
			return
		}
		rooms, err := s.store.ListRooms(r.Context(), contracts.GameAcquire)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "store_error", err)
			return
		}
		deleted := 0
		for _, room := range rooms {
			if !strings.HasPrefix(room.OwnerUserID, req.Prefix) && !strings.HasPrefix(room.OwnerPlayerID, req.Prefix) {
				continue
			}
			if _, err := s.rooms.InterruptRoom(r.Context(), room.RoomID, "loadtest", []string{"system"}, "loadtest_cleanup"); err == nil {
				deleted++
			}
		}
		writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Data: map[string]any{"deleted": deleted}})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) loadtestStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if !loadtestAuthorized(r) {
		writeJSON(w, http.StatusUnauthorized, apiResponse{StatusCode: http.StatusUnauthorized, Error: "unauthorized"})
		return
	}
	if _, ok := s.requireRole(w, r, "admin"); !ok {
		return
	}
	rooms, err := s.store.ListRooms(r.Context(), contracts.GameAcquire)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err)
		return
	}
	presences, err := s.store.ListAllPresence(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store_error", err)
		return
	}
	statuses := map[string]int{}
	for _, room := range rooms {
		statuses[room.Status]++
	}
	onlineHumans := 0
	for _, presence := range presences {
		if presence.Online {
			onlineHumans++
		}
	}
	writeJSON(w, http.StatusOK, apiResponse{StatusCode: http.StatusOK, Data: map[string]any{
		"rooms": len(rooms), "connections": len(presences), "onlineHumans": onlineHumans, "roomStatus": statuses,
	}})
}

func loadtestAuthorized(r *http.Request) bool {
	if os.Getenv("ACQUIRE_LOADTEST_ENABLED") != "true" {
		return false
	}
	token := os.Getenv("ACQUIRE_LOADTEST_TOKEN")
	provided := r.Header.Get("X-Loadtest-Token")
	if len(token) < minLoadtestTokenLength || len(provided) != len(token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
}

func leftPad(value, width int) string {
	result := ""
	for value > 0 {
		result = string(rune('0'+value%10)) + result
		value /= 10
	}
	for len(result) < width {
		result = "0" + result
	}
	return result
}
