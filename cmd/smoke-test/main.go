package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func main() {
	authURL := env("AUTH_URL", "http://localhost:8083")
	controlURL := env("CONTROL_URL", "http://localhost:8083")
	gw1 := env("GW1_WS", "ws://localhost:8081/ws")
	gw2 := env("GW2_WS", "ws://localhost:8082/ws")
	if lb := env("GW_LB_WS", ""); lb != "" {
		gw1 = lb
		gw2 = lb
	}
	if env("SCENARIO", "") == "acquire" {
		runAcquireScenario(authURL, controlURL, gw1)
		return
	}
	if env("SCENARIO", "") == "ai" {
		runAIScenario(authURL, controlURL, gw1)
		return
	}

	aliceSession := login(authURL, "alice")
	bobSession := login(authURL, "bob")

	room := createRoom(controlURL, aliceSession.Cookie, contracts.GameDaVinci, 2)
	fmt.Println("room", room.RoomID, "owner", room.OwnerNode)

	alice := connect(gw1, room.RoomID, room.CurrentMatchID, aliceSession.Cookie)
	defer alice.Close()
	bob := connect(gw2, room.RoomID, room.CurrentMatchID, bobSession.Cookie)
	defer bob.Close()

	waitType(alice, contracts.MessageTypeMatchSync)
	waitType(bob, contracts.MessageTypeMatchSync)

	send(bob, room, "match_ready", map[string]any{"ready": true})
	waitReady(alice, bobSession.PlayerID, true)
	waitReady(bob, bobSession.PlayerID, true)

	send(alice, room, "match_begin", nil)
	waitStatus(alice, "waiting")
	waitStatus(bob, "waiting")

	send(alice, room, "game_ready", nil)
	aliceSync := waitType(alice, contracts.MessageTypeRoomSync)
	roomSync := waitType(bob, contracts.MessageTypeRoomSync)
	fmt.Println("bob room sync", mustJSON(roomSync)[:80])
	if statusOf(aliceSync) == "getCard" {
		cardID := firstBoardCardID(aliceSync)
		send(alice, room, "game_get_card", map[string]any{"id": cardID})
		aliceSync = waitStatus(alice, "guessCard")
		roomSync = waitStatus(bob, "guessCard")
	}

	lastBobSeq := seqOf(roomSync)
	_ = bob.Close()
	bob = connectWithLastSeq(gw2, room.RoomID, room.CurrentMatchID, bobSession.Cookie, lastBobSeq)
	defer bob.Close()
	action := nextAction(aliceSync)
	send(alice, room, action.Type, action.Payload)
	aliceSync = waitType(alice, contracts.MessageTypeRoomSync)
	roomSync = waitType(bob, contracts.MessageTypeRoomSync)

	if env("RECOVERY_WAIT", "") != "" {
		fmt.Println("waiting recovery", env("RECOVERY_WAIT", ""))
		d, _ := time.ParseDuration(env("RECOVERY_WAIT", "35s"))
		time.Sleep(d)
		action = nextAction(aliceSync)
		send(alice, room, action.Type, action.Payload)
		waitType(alice, contracts.MessageTypeRoomSync)
		waitType(bob, contracts.MessageTypeRoomSync)
	}

	fmt.Println("SMOKE_OK")
}

func runAcquireScenario(authURL, controlURL, gatewayURL string) {
	aliceSession := login(authURL, "acquire-alice")
	room := createRoom(controlURL, aliceSession.Cookie, contracts.GameAcquire, 2)
	fmt.Println("acquire room", room.RoomID, "owner", room.OwnerNode)

	alice := connect(gatewayURL, room.RoomID, room.CurrentMatchID, aliceSession.Cookie)
	defer alice.Close()
	waitType(alice, contracts.MessageTypeMatchSync)

	send(alice, room, "match_add_ai", nil)
	waitReady(alice, "ai_001", true)
	send(alice, room, "match_begin", nil)
	waitStatus(alice, "waiting")
	send(alice, room, "game_ready", nil)
	sync := waitType(alice, contracts.MessageTypeRoomSync)
	if statusOf(sync) != "setTile" {
		panic("acquire did not enter setTile")
	}
	roomData, _ := sync["roomData"].(map[string]any)
	current, _ := roomData["currentPlayer"].(string)
	if current == aliceSession.PlayerID {
		playerData, _ := sync["playerData"].(map[string]any)
		tiles, _ := playerData["tiles"].([]any)
		if len(tiles) == 0 {
			panic("acquire player has no tiles")
		}
		send(alice, room, "game_place_tile", map[string]any{"tileKey": tiles[0]})
		waitType(alice, contracts.MessageTypeRoomSync)
	}
	if wait := env("RECOVERY_WAIT", ""); wait != "" {
		fmt.Println("waiting recovery", wait)
		duration, _ := time.ParseDuration(wait)
		time.Sleep(duration)
	}
	fmt.Println("ACQUIRE_SMOKE_OK")
	fmt.Println("SMOKE_OK")
}

func runAIScenario(authURL, controlURL, gatewayURL string) {
	aliceSession := login(authURL, "alice")
	room := createRoom(controlURL, aliceSession.Cookie, contracts.GameDaVinci, 2)
	fmt.Println("room", room.RoomID, "owner", room.OwnerNode)

	alice := connect(gatewayURL, room.RoomID, room.CurrentMatchID, aliceSession.Cookie)
	defer alice.Close()
	waitType(alice, contracts.MessageTypeMatchSync)

	send(alice, room, "match_add_ai", nil)
	waitReady(alice, "AI_2", true)

	send(alice, room, "match_ready", map[string]any{"ready": true})
	waitReady(alice, aliceSession.PlayerID, true)

	send(alice, room, "match_begin", nil)
	waitStatus(alice, "waiting")

	send(alice, room, "game_ready", nil)
	sync := waitAnyStatus(alice, "getCard", "guessCard")
	status := statusOf(sync)
	if status == "waiting" {
		panic("game_ready left room in waiting")
	}
	fmt.Println("ai room status", status)
	fmt.Println("SMOKE_OK")
}

type loginSession struct {
	Cookie   string
	User     contracts.UserIdentity
	PlayerID string
}

func login(base, name string) loginSession {
	return loginAuthCenter(base, name)
}

func loginAuthCenter(base, name string) loginSession {
	email := name + "@example.com"
	sendBody := mustJSON(map[string]string{"email": email})
	resp, err := http.Post(base+"/platform/auth/send-code", "application/json", bytes.NewReader([]byte(sendBody)))
	if err != nil {
		panic(err)
	}
	var sendOut contracts.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&sendOut); err != nil {
		_ = resp.Body.Close()
		panic(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		panic(fmt.Sprintf("send code failed %+v", sendOut))
	}
	data, _ := sendOut.Data.(map[string]any)
	code, _ := data["dev_code"].(string)
	if code == "" {
		panic("auth center did not return devCode; set AUTH_DEV_CODE_LOG=true and AUTH_DEV_CODE_RESPONSE=true")
	}
	verifyBody := mustJSON(map[string]string{"email": email, "code": code})
	resp, err = http.Post(base+"/platform/auth/verify-code", "application/json", bytes.NewReader([]byte(verifyBody)))
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var raw struct {
		StatusCode int             `json:"status_code"`
		Message    string          `json:"message"`
		Data       json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		panic(err)
	}
	if resp.StatusCode != http.StatusOK {
		panic(fmt.Sprintf("verify code failed %+v", raw))
	}
	var payload struct {
		User struct {
			UserID string `json:"id"`
			Email  string `json:"email"`
			Avatar string `json:"avatar"`
			Name   string `json:"name"`
		} `json:"user"`
	}
	if err := json.Unmarshal(raw.Data, &payload); err != nil {
		panic(err)
	}
	cookieHeader := responseCookieHeader(resp.Cookies())
	if cookieHeader == "" {
		panic("auth center login response missing session cookies")
	}
	userID := payload.User.UserID
	displayName := payload.User.Name
	if displayName == "" {
		displayName = payload.User.Email
	}
	user := contracts.UserIdentity{
		UserID:      userID,
		DisplayName: displayName,
		Name:        payload.User.Name,
		Email:       payload.User.Email,
		Avatar:      payload.User.Avatar,
	}
	return loginSession{Cookie: cookieHeader, User: user, PlayerID: legacyPlayerID(user)}
}

func legacyPlayerID(identity contracts.UserIdentity) string {
	return identity.UserID
}

func createRoom(base, cookieHeader, gameType string, maxPlayers int) contracts.RoomRecord {
	payload := map[string]any{"gameType": gameType, "maxPlayers": maxPlayers}
	req, err := http.NewRequest(http.MethodPost, base+"/platform/rooms", bytes.NewReader([]byte(mustJSON(payload))))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookieHeader)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		panic(fmt.Sprintf("create room failed %s", string(b)))
	}
	var raw struct {
		StatusCode int             `json:"status_code"`
		Message    string          `json:"message"`
		Data       json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		panic(err)
	}
	var room contracts.RoomRecord
	if err := json.Unmarshal(raw.Data, &room); err != nil {
		panic(err)
	}
	return room
}

func connect(base, roomID, matchID, cookieHeader string) *websocket.Conn {
	return connectWithLastSeq(base, roomID, matchID, cookieHeader, 0)
}

func connectWithLastSeq(base, roomID, matchID, cookieHeader string, lastSeq int64) *websocket.Conn {
	url := fmt.Sprintf(
		"%s?roomID=%s&matchID=%s&lastSeq=%d",
		base,
		roomID,
		matchID,
		lastSeq,
	)
	header := http.Header{}
	header.Set("Cookie", cookieHeader)
	conn, _, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		panic(err)
	}
	return conn
}

func responseCookieHeader(cookies []*http.Cookie) string {
	var values []string
	for _, cookie := range cookies {
		if cookie.Name == "access_token" || cookie.Name == "refresh_token" {
			values = append(values, cookie.Name+"="+cookie.Value)
		}
	}
	return strings.Join(values, "; ")
}

func send(conn *websocket.Conn, room contracts.RoomRecord, commandType string, payload any) {
	cmd := contracts.ClientCommand{
		SchemaVersion: contracts.RealtimeSchemaVersion,
		CommandID:     fmt.Sprintf("smoke-%d", time.Now().UnixNano()),
		MatchID:       room.CurrentMatchID,
		Type:          commandType,
		Payload:       generatedCommandPayload(room.GameType, commandType, payload),
	}
	if err := conn.WriteJSON(cmd); err != nil {
		panic(err)
	}
}

func generatedCommandPayload(gameType, commandType string, payload any) any {
	switch commandType {
	case "match_ready":
		return map[string]any{"common": map[string]any{"matchReady": payload}}
	case "match_add_ai":
		if payload == nil {
			payload = map[string]any{}
		}
		return map[string]any{"common": map[string]any{"matchSeat": payload}}
	case "match_begin", "game_ready", "game_restart_game":
		return map[string]any{"common": map[string]any{"empty": map[string]any{}}}
	case "game_place_tile":
		return map[string]any{"acquire": map[string]any{"placeTile": payload}}
	case "game_get_card":
		return map[string]any{"davinci": map[string]any{"getCard": payload}}
	case "game_guess_card":
		return map[string]any{"davinci": map[string]any{"guessCard": payload}}
	default:
		panic(fmt.Sprintf("unsupported smoke command %s/%s", gameType, commandType))
	}
}

func nextAction(sync map[string]any) contracts.ClientCommand {
	roomData, _ := sync["roomData"].(map[string]any)
	status, _ := roomData["gameStatus"].(string)
	if status == "guessCard" {
		cardData, _ := sync["cardData"].(map[string]any)
		opponents, _ := cardData["opponents"].([]any)
		for _, raw := range opponents {
			card, _ := raw.(map[string]any)
			if revealed, _ := card["isRevealed"].(bool); !revealed {
				return contracts.ClientCommand{Type: "game_guess_card", Payload: map[string]any{"id": card["id"], "num": 99}}
			}
		}
	}
	return contracts.ClientCommand{Type: "game_ready"}
}

func waitReady(conn *websocket.Conn, playerID string, ready bool) map[string]any {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		msg := waitType(conn, contracts.MessageTypeMatchSync)
		players, _ := msg["players"].(map[string]any)
		player, _ := players[playerID].(map[string]any)
		if got, _ := player["ready"].(bool); got == ready {
			return msg
		}
	}
	panic("timeout waiting ready")
}

func waitStatus(conn *websocket.Conn, status string) map[string]any {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		msg := waitType(conn, contracts.MessageTypeRoomSync)
		if statusOf(msg) == status {
			return msg
		}
	}
	panic("timeout waiting status " + status)
}

func waitAnyStatus(conn *websocket.Conn, statuses ...string) map[string]any {
	want := map[string]bool{}
	for _, status := range statuses {
		want[status] = true
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		msg := waitType(conn, contracts.MessageTypeRoomSync)
		if want[statusOf(msg)] {
			return msg
		}
	}
	panic("timeout waiting statuses")
}

func statusOf(msg map[string]any) string {
	roomData, _ := msg["roomData"].(map[string]any)
	status, _ := roomData["gameStatus"].(string)
	return status
}

func seqOf(msg map[string]any) int64 {
	raw, _ := msg["seq"].(float64)
	return int64(raw)
}

func firstBoardCardID(msg map[string]any) string {
	roomData, _ := msg["roomData"].(map[string]any)
	cards, _ := roomData["boardCards"].(map[string]any)
	for id := range cards {
		return id
	}
	panic("no board cards")
}

func waitType(conn *websocket.Conn, typ string) map[string]any {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			panic(err)
		}
		if msg["type"] == typ {
			if typ == contracts.MessageTypeRoomSync {
				return flattenRoomSync(msg)
			}
			return msg
		}
		if msg["type"] == contracts.MessageTypeError {
			panic(fmt.Sprintf("realtime error: %s", mustJSON(msg)))
		}
		if msg["type"] == contracts.MessageTypeCommandResult && msg["accepted"] == false {
			panic(fmt.Sprintf("command rejected: %s", mustJSON(msg)))
		}
	}
	panic("timeout waiting " + typ)
}

func flattenRoomSync(message map[string]any) map[string]any {
	view, _ := message["view"].(map[string]any)
	for _, gameType := range []string{contracts.GameAcquire, contracts.GameDaVinci, contracts.GameSplendor} {
		payload, ok := view[gameType].(map[string]any)
		if !ok {
			continue
		}
		result := make(map[string]any, len(message)+len(payload))
		for key, value := range message {
			result[key] = value
		}
		for key, value := range payload {
			result[key] = value
		}
		return result
	}
	return message
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
