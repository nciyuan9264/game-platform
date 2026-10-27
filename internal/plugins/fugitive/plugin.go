package fugitive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

const (
	GameType            = "fugitive"
	EngineVersion       = "fugitive-v2"
	ReplaySchemaVersion = 1
	defaultMaxPlayers   = 2
)

const (
	CommandSelectRole     = "match_select_role"
	CommandDrawCard       = "game_draw_card"
	CommandPlayHideout    = "game_play_hideout"
	CommandFinishOpening  = "game_finish_opening"
	CommandLayLow         = "game_lay_low"
	CommandSearchHideouts = "game_search_hideouts"
	CommandTimeout        = "turn_timeout"
)

const (
	aiMinDelay = 3 * time.Second
	aiMaxDelay = 5 * time.Second
)

type Plugin struct{}

func Definition() game.Definition {
	plugin := &Plugin{}
	return game.Definition{
		GameType:          GameType,
		Version:           EngineVersion,
		DefaultMaxPlayers: defaultMaxPlayers,
		Engine:            plugin,
		AI:                plugin,
		Scheduler:         plugin,
		History:           plugin,
	}
}

func (p *Plugin) GameType() string { return GameType }

func (p *Plugin) Version() string { return EngineVersion }

func (p *Plugin) InitRoom(
	_ context.Context,
	cfg game.RoomConfig,
) (game.State, error) {
	if cfg.RandomSeed == 0 {
		return nil, errors.New("random seed is required")
	}
	maxPlayers := cfg.MaxPlayers
	if maxPlayers <= 0 {
		maxPlayers = defaultMaxPlayers
	}
	if maxPlayers != defaultMaxPlayers {
		return nil, errors.New("fugitive requires exactly two seats")
	}
	visibility := cfg.Visibility
	if visibility == "" {
		visibility = contracts.VisibilityPublic
	}
	roundNo := cfg.RoundNo
	if roundNo <= 0 {
		roundNo = 1
	}
	return &State{
		RoomID:             cfg.RoomID,
		OwnerID:            cfg.OwnerPlayerID,
		Status:             StatusMatch,
		MaxPlayers:         maxPlayers,
		Visibility:         visibility,
		DisplayName:        defaultRoomName,
		TurnTimeoutSeconds: defaultTurnTimeout,
		RandomSeed:         cfg.RandomSeed,
		MatchRoundNo:       roundNo,
		Roster:             map[string]*Player{},
		PlayerOrder:        []string{},
		OwnerRole:          RoleFugitive,
		Decks:              [][]Card{},
		Route:              []Hideout{},
		Log:                []Action{},
	}, nil
}

func (p *Plugin) EncodeState(
	_ context.Context,
	state game.State,
) ([]byte, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	if err := validateCardConservation(typed); err != nil {
		return nil, err
	}
	return json.Marshal(typed)
}

func (p *Plugin) DecodeState(
	_ context.Context,
	data []byte,
) (game.State, error) {
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	ensureState(&state)
	if err := validateCardConservation(&state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (p *Plugin) RandomSeed(
	_ context.Context,
	state game.State,
) (uint64, error) {
	typed, err := asState(state)
	if err != nil {
		return 0, err
	}
	return typed.RandomSeed, nil
}

func (p *Plugin) RoomMetadata(
	_ context.Context,
	state game.State,
) (game.RoomMetadata, error) {
	typed, err := asState(state)
	if err != nil {
		return game.RoomMetadata{}, err
	}
	return game.RoomMetadata{
		Visibility:         typed.Visibility,
		DisplayName:        typed.DisplayName,
		TurnTimeoutSeconds: typed.TurnTimeoutSeconds,
	}, nil
}

func (p *Plugin) Roster(
	_ context.Context,
	state game.State,
) ([]game.RosterPlayer, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	result := make([]game.RosterPlayer, 0, len(typed.PlayerOrder))
	for _, playerID := range typed.PlayerOrder {
		player := typed.Roster[playerID]
		if player == nil {
			continue
		}
		displayName := ""
		if player.AI {
			displayName = player.DisplayName
		}
		result = append(result, game.RosterPlayer{
			UserID:      player.UserID,
			PlayerID:    player.PlayerID,
			DisplayName: displayName,
			Online:      player.Online,
			IsAI:        player.AI,
			SeatIndex:   player.SeatIndex,
			Ready:       player.Ready,
			GameRole:    string(player.Role),
		})
	}
	return result, nil
}

func (p *Plugin) Progress(
	_ context.Context,
	state game.State,
) (game.RoomProgress, error) {
	typed, err := asState(state)
	if err != nil {
		return game.RoomProgress{}, err
	}
	return game.RoomProgress{
		Current: highestRevealed(typed),
		Total:   42,
	}, nil
}

func (p *Plugin) ObserveHumanPresence(
	_ context.Context,
	state game.State,
	hasHuman bool,
) (game.HumanPresenceCheck, error) {
	typed, err := asState(state)
	if err != nil {
		return game.HumanPresenceCheck{}, err
	}
	if hasHuman {
		typed.NoHumanChecks = 0
	} else {
		typed.NoHumanChecks++
	}
	return game.HumanPresenceCheck{
		ConsecutiveEmpty: typed.NoHumanChecks,
		Limit:            maxNoHumanChecks,
		ShouldClose:      typed.NoHumanChecks >= maxNoHumanChecks,
	}, nil
}

func (p *Plugin) ApplyCommand(
	ctx context.Context,
	state game.State,
	command contracts.RoomCommand,
) (game.ApplyResult, error) {
	typed, err := asState(state)
	if err != nil {
		return game.ApplyResult{}, err
	}
	payload, err := json.Marshal(command.Payload)
	if err != nil {
		return game.ApplyResult{}, err
	}
	removedPlayerID := ""
	automatic := false
	if player := typed.Roster[command.PlayerID]; player != nil {
		automatic = player.AI
	}
	switch command.Type {
	case "connect":
		err = connect(typed, command.PlayerID)
	case "disconnect":
		if player := typed.Roster[command.PlayerID]; player != nil {
			player.Online = false
		}
	case "match_ready":
		err = setReady(typed, command.PlayerID, payload)
	case "match_add_ai":
		err = addAI(typed, command.PlayerID, payload)
	case "match_remove_player":
		removedPlayerID, err = removePlayer(
			typed,
			command.PlayerID,
			payload,
		)
	case "room_update_settings":
		err = updateRoomSettings(typed, command.PlayerID, payload)
	case CommandSelectRole:
		err = selectRole(typed, command.PlayerID, payload)
	case "match_begin":
		err = beginMatch(typed, command.PlayerID)
	case "game_ready":
		err = gameReady(typed, command.PlayerID, command.SentAt)
	case CommandDrawCard:
		var value struct {
			DeckIndex *int `json:"deckIndex"`
		}
		err = decodePayload(payload, &value)
		if err == nil && value.DeckIndex == nil {
			err = errors.New("deckIndex is required")
		}
		if err == nil {
			err = drawCard(
				typed,
				command.PlayerID,
				*value.DeckIndex,
				automatic,
			)
		}
	case CommandPlayHideout:
		var value struct {
			Hideout *int  `json:"hideout"`
			Sprints []int `json:"sprints"`
		}
		err = decodePayload(payload, &value)
		if err == nil && value.Hideout == nil {
			err = errors.New("hideout is required")
		}
		if err == nil {
			err = playHideout(
				typed,
				command.PlayerID,
				*value.Hideout,
				value.Sprints,
				automatic,
			)
		}
	case CommandFinishOpening:
		err = finishOpening(typed, command.PlayerID, automatic)
	case CommandLayLow:
		err = layLow(typed, command.PlayerID, automatic)
	case CommandSearchHideouts:
		var value struct {
			Numbers []int `json:"numbers"`
		}
		err = decodePayload(payload, &value)
		if err == nil {
			err = searchHideouts(
				typed,
				command.PlayerID,
				value.Numbers,
				automatic,
			)
		}
	case CommandTimeout:
		if command.ConnectionRef.Valid() {
			err = errors.New("client cannot submit timeout commands")
		} else {
			err = p.applyTimeout(ctx, typed, command.PlayerID)
		}
	case "game_restart_game":
		err = errors.New("finished rooms must start a new match")
	default:
		err = fmt.Errorf("unknown fugitive command %q", command.Type)
	}
	if err != nil {
		return game.ApplyResult{}, err
	}
	if err := validateCardConservation(typed); err != nil {
		return game.ApplyResult{}, err
	}
	result := game.ApplyResult{
		Status:   typed.Status,
		Finished: typed.Status == StatusEnd,
	}
	if removedPlayerID != "" {
		result.Effects = append(result.Effects, game.DeliveryEffect{
			Audience:       game.AudiencePlayer,
			TargetPlayerID: removedPlayerID,
			Reliable:       true,
			CloseAfter:     true,
			Payload: contracts.ErrorMessage{
				Type:    contracts.MessageTypeError,
				Code:    "REMOVED",
				Message: "你已被移出房间",
			},
		})
	}
	if request := nextAIRequest(typed); request != nil {
		result.AIRequests = append(result.AIRequests, *request)
	}
	return result, nil
}

func (p *Plugin) PendingCommands(
	_ context.Context,
	state game.State,
) ([]game.ScheduledCommand, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	if typed.TurnDeadline.IsZero() || typed.CurrentPlayer == "" {
		return nil, nil
	}
	player := typed.Roster[typed.CurrentPlayer]
	if player == nil || player.AI {
		return nil, nil
	}
	return []game.ScheduledCommand{{
		Key: fmt.Sprintf(
			"%s:%s:%d",
			typed.Status,
			typed.CurrentPlayer,
			typed.TurnDeadline.UnixNano(),
		),
		ExecuteAt: typed.TurnDeadline,
		PlayerID:  typed.CurrentPlayer,
		Type:      CommandTimeout,
	}}, nil
}

func (p *Plugin) BuildPlayerView(
	_ context.Context,
	state game.State,
	request game.ViewRequest,
) (any, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	if typed.Roster[request.PlayerID] == nil {
		return nil, errors.New("player is not in room")
	}
	players := playerViews(typed, request.PlayerProfiles)
	if typed.Status == StatusMatch {
		return map[string]any{
			"type":        contracts.MessageTypeMatchSync,
			"roomID":      typed.RoomID,
			"matchID":     request.MatchID,
			"ownerID":     typed.OwnerID,
			"status":      typed.Status,
			"playerID":    request.PlayerID,
			"visibility":  typed.Visibility,
			"maxPlayers":  typed.MaxPlayers,
			"players":     players,
			"playerOrder": typed.PlayerOrder,
		}, nil
	}
	return roomView(
		typed,
		request.PlayerID,
		request.MatchID,
		request.Seq,
		players,
	), nil
}

func (p *Plugin) applyTimeout(
	ctx context.Context,
	state *State,
	playerID string,
) error {
	if state.CurrentPlayer != playerID {
		return errors.New("timeout player is no longer active")
	}
	command, err := p.Decide(ctx, game.AIRequest{
		RoomID:   state.RoomID,
		GameType: GameType,
		PlayerID: playerID,
		State:    cloneState(state),
	})
	if err != nil {
		return err
	}
	payload, err := json.Marshal(command.Payload)
	if err != nil {
		return err
	}
	switch command.Type {
	case CommandDrawCard:
		var value struct {
			DeckIndex int `json:"deckIndex"`
		}
		if err := decodePayload(payload, &value); err != nil {
			return err
		}
		return drawCard(state, playerID, value.DeckIndex, true)
	case CommandPlayHideout:
		var value struct {
			Hideout int   `json:"hideout"`
			Sprints []int `json:"sprints"`
		}
		if err := decodePayload(payload, &value); err != nil {
			return err
		}
		return playHideout(
			state,
			playerID,
			value.Hideout,
			value.Sprints,
			true,
		)
	case CommandFinishOpening:
		return finishOpening(state, playerID, true)
	case CommandLayLow:
		return layLow(state, playerID, true)
	case CommandSearchHideouts:
		var value struct {
			Numbers []int `json:"numbers"`
		}
		if err := decodePayload(payload, &value); err != nil {
			return err
		}
		return searchHideouts(state, playerID, value.Numbers, true)
	default:
		return errors.New("AI returned an unsupported timeout command")
	}
}

func nextAIRequest(state *State) *game.AIRequest {
	player := state.Roster[state.CurrentPlayer]
	if player == nil ||
		!player.AI ||
		state.Status == StatusMatch ||
		state.Status == StatusWaiting ||
		state.Status == StatusEnd {
		return nil
	}
	delayRange := int((aiMaxDelay - aiMinDelay) / time.Millisecond)
	delayOffset := int(
		(state.RandomSeed +
			uint64(len(state.Log))*7919 +
			uint64(state.RoundNo)*104729) %
			uint64(delayRange+1),
	)
	fallback := contracts.RoomCommand{
		Type:     CommandTimeout,
		PlayerID: player.PlayerID,
	}
	return &game.AIRequest{
		RoomID:   state.RoomID,
		GameType: GameType,
		PlayerID: player.PlayerID,
		State:    cloneState(state),
		Delay: aiMinDelay +
			time.Duration(delayOffset)*time.Millisecond,
		FallbackCommand: &fallback,
	}
}

func roomView(
	state *State,
	playerID,
	matchID string,
	seq int64,
	players map[string]any,
) map[string]any {
	viewer := state.Roster[playerID]
	viewerRole := Role("")
	hand := []Card{}
	if viewer != nil {
		viewerRole = viewer.Role
		hand = append(hand, viewer.Hand...)
	}
	playerData := map[string]any{}
	for _, currentID := range state.PlayerOrder {
		player := state.Roster[currentID]
		if player == nil {
			continue
		}
		playerData[currentID] = map[string]any{
			"role":      player.Role,
			"handCount": len(player.Hand),
		}
	}
	route := make([]map[string]any, 0, len(state.Route))
	revealAll := state.Status == StatusEnd
	for index, hideout := range state.Route {
		readable := revealAll ||
			hideout.Revealed ||
			viewerRole == RoleFugitive
		entry := map[string]any{
			"index":       index,
			"revealed":    hideout.Revealed,
			"sprintCount": len(hideout.Sprints),
		}
		if readable {
			entry["card"] = hideout.Card
			entry["sprints"] = hideout.Sprints
		}
		route = append(route, entry)
	}
	logs := make([]Action, 0, len(state.Log))
	for _, action := range state.Log {
		logs = append(
			logs,
			projectAction(action, playerID),
		)
	}
	var lastAction any
	if len(logs) > 0 {
		lastAction = logs[len(logs)-1]
	}
	decks := make([]map[string]any, len(state.Decks))
	for index, cards := range state.Decks {
		decks[index] = map[string]any{
			"deckIndex": index,
			"min":       deckRanges[index][0],
			"max":       deckRanges[index][1],
			"remaining": len(cards),
		}
	}
	var result any
	if state.Status == StatusEnd {
		result = map[string]any{
			"winnerID":   state.WinnerID,
			"winnerRole": state.WinnerRole,
			"reason":     state.EndReason,
			"searches":   state.Searches,
		}
	}
	return map[string]any{
		"type":       contracts.MessageTypeRoomSync,
		"roomID":     state.RoomID,
		"matchID":    matchID,
		"seq":        seq,
		"playerId":   playerID,
		"ownerID":    state.OwnerID,
		"visibility": state.Visibility,
		"playerData": playerData,
		"hand":       hand,
		"result":     result,
		"roomData": map[string]any{
			"currentPlayer":         state.CurrentPlayer,
			"gameStatus":            state.Status,
			"roundNo":               state.RoundNo,
			"drawsRemaining":        state.DrawsRemaining,
			"highestRevealed":       highestRevealed(state),
			"hiddenHideoutCount":    hiddenHideoutCount(state),
			"route":                 route,
			"decks":                 decks,
			"activityLog":           logs,
			"lastData":              lastAction,
			"turnDeadline":          deadlineValue(state.TurnDeadline),
			"turnTimeoutMs":         state.TurnTimeout.Milliseconds(),
			"players":               players,
			"playerOrder":           state.PlayerOrder,
			"openingHideoutsPlaced": state.OpeningHideouts,
		},
	}
}

func projectAction(
	action Action,
	playerID string,
) Action {
	result := cloneAction(action)
	private := result.PrivatePlayerID != ""
	if private && result.PrivatePlayerID != playerID {
		result.Numbers = nil
		result.Destination = nil
	}
	result.Private = private && result.PrivatePlayerID == playerID
	result.PrivatePlayerID = ""
	return result
}

func playerViews(
	state *State,
	profiles map[string]game.PlayerProfile,
) map[string]any {
	result := make(map[string]any, len(state.PlayerOrder))
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			continue
		}
		displayName := player.DisplayName
		userID := player.UserID
		avatar := ""
		if profile, ok := profiles[playerID]; ok {
			userID = profile.UserID
			displayName = profile.DisplayName
			avatar = profile.Avatar
		}
		if displayName == "" {
			if player.AI {
				displayName = "灰狐"
			} else {
				displayName = fmt.Sprintf("玩家 %s", playerID)
			}
		}
		result[playerID] = map[string]any{
			"userID":      userID,
			"playerID":    playerID,
			"displayName": displayName,
			"avatar":      avatar,
			"online":      player.Online,
			"ai":          player.AI,
			"ready":       player.Ready,
			"seatIndex":   player.SeatIndex,
			"gameRole":    player.Role,
		}
	}
	return result
}

func asState(state game.State) (*State, error) {
	switch typed := state.(type) {
	case *State:
		if typed == nil {
			return nil, errors.New("fugitive state is nil")
		}
		return typed, nil
	case State:
		return &typed, nil
	default:
		return nil, fmt.Errorf(
			"unexpected fugitive state %T",
			state,
		)
	}
}

func deadlineValue(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}
