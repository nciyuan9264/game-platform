package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"
)

type commandObservation struct {
	CommandID string
	Type      string
	Seq       int64
}

type canaryObservation struct {
	GameType string
	Room     roomInfo
	PlayerID string
	LastSeq  int64
	Commands []commandObservation
}

type canaryVerifier interface {
	VerifyActive(context.Context, canaryObservation) []checkResult
	VerifyClosed(context.Context, canaryObservation) []checkResult
	Close()
}

type legalAction struct {
	Command         clientCommand
	ConflictCommand clientCommand
}

func runCanary(
	ctx context.Context,
	cfg config,
	api *apiClient,
	verifier canaryVerifier,
	gameType string,
) (result gameResult) {
	result = gameResult{
		GameType: gameType,
		Request:  "regression:" + cfg.RunID + ":" + gameType,
		Checks:   []checkResult{},
	}
	var room roomInfo
	var realtime *realtimeClient
	observation := canaryObservation{GameType: gameType}
	defer func() {
		if realtime != nil {
			_ = realtime.Close()
		}
		if room.RoomID == "" {
			return
		}
		closeRequest := result.Request + ":close"
		result.Checks = append(result.Checks, runCheck("close private room", func() error {
			closed, err := api.closeRoom(ctx, gameType, room.RoomID, closeRequest)
			if err != nil {
				return err
			}
			if closed.Status != "closed" {
				return fmt.Errorf("closed room status = %q", closed.Status)
			}
			return nil
		}))
		observation.Room = room
		result.Checks = append(result.Checks, verifier.VerifyClosed(ctx, observation)...)
	}()

	if !appendGameCheck(&result, "create private room", func() error {
		var err error
		room, err = api.createRoom(ctx, gameType, result.Request, 2)
		if err != nil {
			return err
		}
		if room.GameType != gameType || room.Visibility != "private" {
			return fmt.Errorf("created room = %+v", room)
		}
		result.RoomID = room.RoomID
		result.MatchID = room.CurrentMatchID
		observation.Room = room
		return nil
	}) {
		return result
	}

	if !appendGameCheck(&result, "create request is idempotent", func() error {
		repeated, err := api.createRoom(ctx, gameType, result.Request, 2)
		if err != nil {
			return err
		}
		if repeated.RoomID != room.RoomID || repeated.CurrentMatchID != room.CurrentMatchID {
			return fmt.Errorf("idempotent create returned %s/%s, want %s/%s",
				repeated.RoomID, repeated.CurrentMatchID, room.RoomID, room.CurrentMatchID)
		}
		return nil
	}) {
		return result
	}

	if !appendGameCheck(&result, "create request detects intent conflict", func() error {
		_, err := api.createRoom(ctx, gameType, result.Request, 3)
		if !isAPIError(err, http.StatusConflict, "idempotency_conflict") {
			return fmt.Errorf("conflicting create error = %v", err)
		}
		return nil
	}) {
		return result
	}

	if !appendGameCheck(&result, "websocket initial sync", func() error {
		dialCtx, cancel := commandContext(ctx)
		defer cancel()
		var err error
		realtime, err = dialRealtime(dialCtx, cfg, room.RoomID, room.CurrentMatchID, 0)
		if err != nil {
			return err
		}
		sync, err := realtime.waitForSync(dialCtx, func(message realtimeEnvelope) bool {
			return message.Type == "MATCH_SYNC" && message.PlayerID != ""
		})
		if err != nil {
			return err
		}
		observation.PlayerID = sync.PlayerID
		observation.LastSeq = sync.Seq
		return nil
	}) {
		return result
	}

	var lastAckSeq int64
	sendAccepted := func(name string, command clientCommand) bool {
		return appendGameCheck(&result, name, func() error {
			commandCtx, cancel := commandContext(ctx)
			defer cancel()
			ack, err := realtime.sendAndWait(commandCtx, command)
			if err != nil {
				return err
			}
			if !ack.Accepted {
				return fmt.Errorf("%s rejected: %s %s", command.Type, ack.ErrorCode, ack.Message)
			}
			if ack.Seq <= lastAckSeq {
				return fmt.Errorf("%s seq = %d, previous = %d", command.Type, ack.Seq, lastAckSeq)
			}
			lastAckSeq = ack.Seq
			observation.Commands = append(observation.Commands, commandObservation{
				CommandID: command.CommandID,
				Type:      command.Type,
				Seq:       ack.Seq,
			})
			return nil
		})
	}

	addAI := commonCommand(room.CurrentMatchID, canaryCommandID(cfg.RunID, gameType, "add-ai"), "match_add_ai", map[string]any{"seatIndex": 1})
	if !sendAccepted("add AI", addAI) {
		return result
	}
	begin := commonCommand(room.CurrentMatchID, canaryCommandID(cfg.RunID, gameType, "begin"), "match_begin", nil)
	if !sendAccepted("begin match", begin) {
		return result
	}
	ready := commonCommand(room.CurrentMatchID, canaryCommandID(cfg.RunID, gameType, "ready"), "game_ready", nil)
	if !sendAccepted("game ready", ready) {
		return result
	}

	var action legalAction
	if !appendGameCheck(&result, "select first legal action", func() error {
		commandCtx, cancel := commandContext(ctx)
		defer cancel()
		_, err := realtime.waitForSync(commandCtx, func(message realtimeEnvelope) bool {
			selected, ok := selectFirstAction(
				cfg.RunID,
				gameType,
				room.CurrentMatchID,
				observation.PlayerID,
				message,
			)
			if ok {
				action = selected
			}
			return ok
		})
		return err
	}) {
		return result
	}

	var actionAck realtimeEnvelope
	if !appendGameCheck(&result, "submit first legal action", func() error {
		commandCtx, cancel := commandContext(ctx)
		defer cancel()
		var err error
		actionAck, err = realtime.sendAndWait(commandCtx, action.Command)
		if err != nil {
			return err
		}
		if !actionAck.Accepted || actionAck.Seq <= lastAckSeq {
			return fmt.Errorf("action result = %+v, previous seq = %d", actionAck, lastAckSeq)
		}
		lastAckSeq = actionAck.Seq
		observation.Commands = append(observation.Commands, commandObservation{
			CommandID: action.Command.CommandID,
			Type:      action.Command.Type,
			Seq:       actionAck.Seq,
		})
		return nil
	}) {
		return result
	}

	if !appendGameCheck(&result, "command retry is idempotent", func() error {
		commandCtx, cancel := commandContext(ctx)
		defer cancel()
		repeated, err := realtime.sendAndWait(commandCtx, action.Command)
		if err != nil {
			return err
		}
		if !repeated.Accepted || repeated.Seq != actionAck.Seq {
			return fmt.Errorf("duplicate result = %+v, want accepted seq %d", repeated, actionAck.Seq)
		}
		return nil
	}) {
		return result
	}

	if !appendGameCheck(&result, "command retry detects intent conflict", func() error {
		commandCtx, cancel := commandContext(ctx)
		defer cancel()
		conflict, err := realtime.sendAndWait(commandCtx, action.ConflictCommand)
		if err != nil {
			return err
		}
		if conflict.Accepted || conflict.ErrorCode != "command_id_reused" || conflict.Seq > actionAck.Seq {
			return fmt.Errorf("intent conflict result = %+v", conflict)
		}
		return nil
	}) {
		return result
	}

	var latest realtimeEnvelope
	if !appendGameCheck(&result, "observe committed action sync", func() error {
		commandCtx, cancel := commandContext(ctx)
		defer cancel()
		var err error
		latest, err = realtime.waitForSync(commandCtx, func(message realtimeEnvelope) bool {
			return message.Type == "ROOM_SYNC" && message.Seq >= actionAck.Seq
		})
		if err == nil {
			observation.LastSeq = latest.Seq
		}
		return err
	}) {
		return result
	}

	result.Checks = append(result.Checks, verifier.VerifyActive(ctx, observation)...)

	if !appendGameCheck(&result, "lastSeq reconnect", func() error {
		_ = realtime.Close()
		realtime = nil
		commandCtx, cancel := commandContext(ctx)
		defer cancel()
		var err error
		realtime, err = dialRealtime(
			commandCtx,
			cfg,
			room.RoomID,
			room.CurrentMatchID,
			observation.LastSeq,
		)
		if err != nil {
			return err
		}
		reconnected, err := realtime.waitForSync(commandCtx, func(message realtimeEnvelope) bool {
			return message.Seq > observation.LastSeq
		})
		if err != nil {
			return err
		}
		observation.LastSeq = reconnected.Seq
		return nil
	}) {
		return result
	}

	appendGameCheck(&result, "gateway rejects runtime command", func() error {
		commandCtx, cancel := commandContext(ctx)
		defer cancel()
		command := commonCommand(
			room.CurrentMatchID,
			canaryCommandID(cfg.RunID, gameType, "runtime"),
			"runtime_room_close",
			nil,
		)
		rejected, err := realtime.sendAndWait(commandCtx, command)
		if err != nil {
			return err
		}
		if rejected.Accepted || rejected.ErrorCode != "unauthorized_command" {
			return fmt.Errorf("runtime command result = %+v", rejected)
		}
		return nil
	})
	return result
}

func appendGameCheck(result *gameResult, name string, fn func() error) bool {
	check := runCheck(name, fn)
	result.Checks = append(result.Checks, check)
	return check.Status == checkPassed
}

func canaryCommandID(runID, gameType, suffix string) string {
	namespace := uuid.MustParse("8de99310-e202-43d1-b2c8-9d33d0606dc1")
	return uuid.NewSHA1(namespace, []byte(runID+":"+gameType+":"+suffix)).String()
}

func selectFirstAction(
	runID,
	gameType,
	matchID,
	playerID string,
	message realtimeEnvelope,
) (legalAction, bool) {
	if message.Type != "ROOM_SYNC" || len(message.View[gameType]) == 0 {
		return legalAction{}, false
	}
	var view map[string]any
	if err := json.Unmarshal(message.View[gameType], &view); err != nil {
		return legalAction{}, false
	}
	roomData, _ := view["roomData"].(map[string]any)
	if roomData == nil || stringValue(roomData, "currentPlayer") != playerID {
		return legalAction{}, false
	}
	commandID := canaryCommandID(runID, gameType, "action")
	switch gameType {
	case "acquire":
		playerData, _ := view["playerData"].(map[string]any)
		tiles := stringSlice(playerData["tiles"])
		sort.Strings(tiles)
		if len(tiles) == 0 {
			return legalAction{}, false
		}
		return legalAction{
			Command: gameCommand(
				gameType, matchID, commandID, "game_place_tile", "placeTile",
				map[string]any{"tileKey": tiles[0]},
			),
			ConflictCommand: gameCommand(
				gameType, matchID, commandID, "game_place_tile", "placeTile",
				map[string]any{"tileKey": "__intent_conflict__"},
			),
		}, true
	case "davinci":
		boardCards, _ := roomData["boardCards"].(map[string]any)
		cardIDs := make([]string, 0, len(boardCards))
		for cardID := range boardCards {
			cardIDs = append(cardIDs, cardID)
		}
		sort.Strings(cardIDs)
		if len(cardIDs) == 0 {
			return legalAction{}, false
		}
		return legalAction{
			Command: gameCommand(
				gameType, matchID, commandID, "game_get_card", "getCard",
				map[string]any{"id": cardIDs[0]},
			),
			ConflictCommand: gameCommand(
				gameType, matchID, commandID, "game_get_card", "getCard",
				map[string]any{"id": "__intent_conflict__"},
			),
		}, true
	case "splendor":
		gems, _ := roomData["gems"].(map[string]any)
		colors := make([]string, 0, len(gems))
		for color, value := range gems {
			if color != "Gold" && numericValue(value) > 0 {
				colors = append(colors, color)
			}
		}
		sort.Strings(colors)
		if len(colors) < 3 {
			return legalAction{}, false
		}
		take := map[string]any{colors[0]: 1, colors[1]: 1, colors[2]: 1}
		return legalAction{
			Command: gameCommand(
				gameType, matchID, commandID, "game_get_gem", "getGem",
				map[string]any{"take": take},
			),
			ConflictCommand: gameCommand(
				gameType, matchID, commandID, "game_get_gem", "getGem",
				map[string]any{"take": map[string]any{colors[0]: 2}},
			),
		}, true
	case "yinsh":
		rings, _ := roomData["rings"].(map[string]any)
		markers, _ := roomData["markers"].(map[string]any)
		pointID := ""
		for _, candidate := range []string{
			"F6", "E4", "D3", "C5", "F9", "G3", "H9", "I8", "I6", "D7",
		} {
			if rings[candidate] == nil && markers[candidate] == nil {
				pointID = candidate
				break
			}
		}
		if pointID == "" {
			return legalAction{}, false
		}
		return legalAction{
			Command: gameCommand(
				gameType, matchID, commandID, "game_place_ring", "placeRing",
				map[string]any{"pointID": pointID},
			),
			ConflictCommand: gameCommand(
				gameType, matchID, commandID, "game_place_ring", "placeRing",
				map[string]any{"pointID": "__intent_conflict__"},
			),
		}, true
	case "othello":
		moves, _ := roomData["legalMoves"].([]any)
		if len(moves) == 0 {
			return legalAction{}, false
		}
		first, _ := moves[0].(map[string]any)
		index := numericValue(first["index"])
		if index < 0 || index >= 64 {
			return legalAction{}, false
		}
		return legalAction{
			Command: gameCommand(
				gameType,
				matchID,
				commandID,
				"game_place_disc",
				"placeDisc",
				map[string]any{"index": index},
			),
			ConflictCommand: gameCommand(
				gameType,
				matchID,
				commandID,
				"game_place_disc",
				"placeDisc",
				map[string]any{"index": 99},
			),
		}, true
	case "fugitive":
		hand, _ := view["hand"].([]any)
		hideout := -1
		for _, raw := range hand {
			value, _ := raw.(map[string]any)
			number := numericValue(value["number"])
			if number > 0 && number <= 3 &&
				(hideout < 0 || number < hideout) {
				hideout = number
			}
		}
		if hideout < 1 {
			return legalAction{}, false
		}
		return legalAction{
			Command: gameCommand(
				gameType,
				matchID,
				commandID,
				"game_play_hideout",
				"playHideout",
				map[string]any{
					"hideout": hideout,
					"sprints": []any{},
				},
			),
			ConflictCommand: gameCommand(
				gameType,
				matchID,
				commandID,
				"game_play_hideout",
				"playHideout",
				map[string]any{
					"hideout": 41,
					"sprints": []any{},
				},
			),
		}, true
	case "warchest":
		actions, _ := roomData["legalActions"].([]any)
		if len(actions) == 0 {
			return legalAction{}, false
		}
		first, _ := actions[0].(map[string]any)
		actionID := stringValue(first, "actionID")
		if actionID == "" {
			return legalAction{}, false
		}
		return legalAction{
			Command: gameCommand(
				gameType,
				matchID,
				commandID,
				"game_play_action",
				"playAction",
				map[string]any{"actionID": actionID},
			),
			ConflictCommand: gameCommand(
				gameType,
				matchID,
				commandID,
				"game_play_action",
				"playAction",
				map[string]any{"actionID": "__intent_conflict__"},
			),
		}, true
	case "quoridor":
		moves, _ := roomData["legalMoves"].([]any)
		if len(moves) == 0 {
			return legalAction{}, false
		}
		first, _ := moves[0].(map[string]any)
		row := numericValue(first["row"])
		column := numericValue(first["column"])
		if row < 0 || row >= 9 || column < 0 || column >= 9 {
			return legalAction{}, false
		}
		return legalAction{
			Command: gameCommand(
				gameType,
				matchID,
				commandID,
				"game_move_pawn",
				"movePawn",
				map[string]any{
					"to": map[string]any{
						"row": row, "column": column,
					},
				},
			),
			ConflictCommand: gameCommand(
				gameType,
				matchID,
				commandID,
				"game_move_pawn",
				"movePawn",
				map[string]any{
					"to": map[string]any{
						"row": -1, "column": -1,
					},
				},
			),
		}, true
	case "hex":
		moves, _ := roomData["legalMoves"].([]any)
		if len(moves) == 0 {
			return legalAction{}, false
		}
		first, _ := moves[0].(map[string]any)
		row := numericValue(first["row"])
		column := numericValue(first["column"])
		if row < 0 || row >= 11 || column < 0 || column >= 11 {
			return legalAction{}, false
		}
		return legalAction{
			Command: gameCommand(
				gameType,
				matchID,
				commandID,
				"game_place_stone",
				"placeStone",
				map[string]any{
					"cell": map[string]any{
						"row": row, "column": column,
					},
				},
			),
			ConflictCommand: gameCommand(
				gameType,
				matchID,
				commandID,
				"game_place_stone",
				"placeStone",
				map[string]any{
					"cell": map[string]any{
						"row": -1, "column": -1,
					},
				},
			),
		}, true
	default:
		return legalAction{}, false
	}
}

func stringSlice(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			result = append(result, text)
		}
	}
	return result
}

func numericValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		number, _ := typed.Int64()
		return int(number)
	default:
		return 0
	}
}
