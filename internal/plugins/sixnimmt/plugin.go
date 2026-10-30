package sixnimmt

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
	GameType            = "sixnimmt"
	EngineVersion       = "sixnimmt-v3"
	ReplaySchemaVersion = 1
	defaultMaxPlayers   = 10
	maxNoHumanChecks    = 3
)

const (
	CommandLockCard        = "game_lock_card"
	CommandUnlockCard      = "game_unlock_card"
	CommandChooseRow       = "game_choose_row"
	CommandNextRound       = "game_next_round"
	CommandTimeout         = "turn_timeout"
	CommandRoundTransition = "round_transition_timeout"
)

const (
	turnTimeout          = 60 * time.Second
	roundTransitionDelay = 15 * time.Second
	aiMinDelay           = 800 * time.Millisecond
	aiMaxDelay           = 1600 * time.Millisecond
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
		LegacyReplay:      plugin,
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
		return nil, errors.New("sixnimmt requires ten room seats")
	}
	visibility := cfg.Visibility
	if visibility == "" {
		visibility = contracts.VisibilityPublic
	}
	matchRoundNo := cfg.RoundNo
	if matchRoundNo <= 0 {
		matchRoundNo = 1
	}
	return &State{
		RoomID:             cfg.RoomID,
		OwnerID:            cfg.OwnerPlayerID,
		Status:             StatusMatch,
		MaxPlayers:         maxPlayers,
		Visibility:         visibility,
		TurnTimeoutSeconds: int(turnTimeout / time.Second),
		RandomSeed:         cfg.RandomSeed,
		MatchRoundNo:       matchRoundNo,
		Roster:             map[string]*Player{},
		PlayerOrder:        []string{},
		Rows:               [][]int{},
		UnusedCards:        []int{},
		Queue:              []Selection{},
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
	if err := validateState(typed); err != nil {
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
	if err := validateState(&state); err != nil {
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
		TurnTimeoutSeconds: int(turnTimeout / time.Second),
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
	maxPenalty := 0
	for _, player := range typed.Roster {
		if player != nil && player.TotalPenalty > maxPenalty {
			maxPenalty = player.TotalPenalty
		}
	}
	return game.RoomProgress{Current: maxPenalty, Total: targetPenalty}, nil
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
	switch command.Type {
	case "connect":
		err = connectUser(typed, command.UserID, command.PlayerID)
	case "disconnect":
		if player := typed.Roster[command.PlayerID]; player != nil {
			player.Online = false
		}
	case "match_ready":
		err = setReady(typed, command.PlayerID, command.UserID, payload)
	case "match_add_ai":
		err = addAI(typed, command.PlayerID, command.UserID, payload)
	case "match_remove_player":
		removedPlayerID, err = removePlayer(typed, command.PlayerID, command.UserID, payload)
	case "room_update_settings":
		err = updateVisibility(typed, command.PlayerID, command.UserID, payload)
	case "match_begin":
		err = beginMatch(typed, command.PlayerID, command.UserID)
	case "game_ready":
		err = gameReady(typed, command.PlayerID, command.SentAt)
	case CommandLockCard:
		var value struct {
			Card *int `json:"card"`
		}
		err = decodePayload(payload, &value)
		if err == nil && value.Card == nil {
			err = errors.New("card is required")
		}
		if err == nil {
			automation := ""
			if player := typed.Roster[command.PlayerID]; player != nil && player.AI {
				automation = AutomationAI
			}
			err = lockCard(typed, command.PlayerID, *value.Card, automation)
		}
	case CommandUnlockCard:
		err = unlockCard(typed, command.PlayerID)
	case CommandChooseRow:
		var value struct {
			RowIndex *int `json:"rowIndex"`
		}
		err = decodePayload(payload, &value)
		if err == nil && value.RowIndex == nil {
			err = errors.New("rowIndex is required")
		}
		if err == nil {
			err = chooseRow(
				typed,
				command.PlayerID,
				*value.RowIndex,
				false,
			)
		}
	case CommandNextRound:
		err = nextRound(typed, command.PlayerID, false)
	case CommandTimeout:
		if command.ConnectionRef.Valid() {
			err = errors.New("client cannot submit timeout commands")
		} else {
			err = p.applyTimeout(ctx, typed, command.PlayerID)
		}
	case CommandRoundTransition:
		if command.ConnectionRef.Valid() {
			err = errors.New("client cannot submit round transition commands")
		} else {
			err = nextRound(typed, command.PlayerID, true)
		}
	case "game_restart_game":
		err = errors.New("finished rooms must start a new match")
	default:
		err = fmt.Errorf("unknown sixnimmt command %q", command.Type)
	}
	if err != nil {
		return game.ApplyResult{}, err
	}
	updateDeadlines(typed, command.SentAt)
	if err := validateState(typed); err != nil {
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
	if typed.Status == StatusRoundEnd &&
		!typed.RoundTransitionDeadline.IsZero() {
		return []game.ScheduledCommand{{
			Key: fmt.Sprintf(
				"round:%d:%d",
				typed.RoundNo,
				typed.RoundTransitionDeadline.UnixNano(),
			),
			ExecuteAt: typed.RoundTransitionDeadline,
			PlayerID:  typed.OwnerID,
			Type:      CommandRoundTransition,
		}}, nil
	}
	if typed.TurnDeadline.IsZero() {
		return nil, nil
	}
	switch typed.Status {
	case StatusSelecting:
		result := []game.ScheduledCommand{}
		for _, playerID := range typed.PlayerOrder {
			player := typed.Roster[playerID]
			if player == nil || player.AI || player.LockedCard != nil {
				continue
			}
			result = append(result, game.ScheduledCommand{
				Key: fmt.Sprintf(
					"select:%d:%d:%s:%d",
					typed.RoundNo,
					typed.TurnNo,
					playerID,
					typed.TurnDeadline.UnixNano(),
				),
				ExecuteAt: typed.TurnDeadline,
				PlayerID:  playerID,
				Type:      CommandTimeout,
			})
		}
		return result, nil
	case StatusChooseRow:
		if typed.PendingChoice == nil {
			return nil, nil
		}
		player := typed.Roster[typed.PendingChoice.PlayerID]
		if player == nil || player.AI {
			return nil, nil
		}
		return []game.ScheduledCommand{{
			Key: fmt.Sprintf(
				"row:%d:%d:%s:%d",
				typed.RoundNo,
				typed.TurnNo,
				typed.PendingChoice.PlayerID,
				typed.TurnDeadline.UnixNano(),
			),
			ExecuteAt: typed.TurnDeadline,
			PlayerID:  typed.PendingChoice.PlayerID,
			Type:      CommandTimeout,
		}}, nil
	default:
		return nil, nil
	}
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
	_ context.Context,
	state *State,
	playerID string,
) error {
	switch state.Status {
	case StatusSelecting:
		player := state.Roster[playerID]
		if player == nil || player.LockedCard != nil {
			return nil
		}
		card, err := bestCard(aiObservationFor(state, playerID))
		if err != nil {
			return err
		}
		return lockCard(state, playerID, card, AutomationTimeout)
	case StatusChooseRow:
		if state.PendingChoice == nil ||
			state.PendingChoice.PlayerID != playerID {
			return nil
		}
		return chooseRow(state, playerID, cheapestRow(state.Rows), true)
	default:
		return nil
	}
}

func nextAIRequest(state *State) *game.AIRequest {
	if state.Status != StatusSelecting {
		return nil
	}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil || !player.AI || player.LockedCard != nil {
			continue
		}
		delayRange := int((aiMaxDelay - aiMinDelay) / time.Millisecond)
		delayOffset := int(
			(state.RandomSeed +
				uint64(len(state.Log))*7919 +
				uint64(state.RoundNo)*104729 +
				uint64(state.TurnNo)*15485863 +
				uint64(player.SeatIndex)*32452843) %
				uint64(delayRange+1),
		)
		fallback := contracts.RoomCommand{
			Type:     CommandTimeout,
			PlayerID: playerID,
		}
		return &game.AIRequest{
			RoomID:   state.RoomID,
			GameType: GameType,
			PlayerID: playerID,
			State:    cloneState(state),
			Delay: aiMinDelay +
				time.Duration(delayOffset)*time.Millisecond,
			FallbackCommand: &fallback,
		}
	}
	return nil
}

func updateDeadlines(state *State, commandTime time.Time) {
	if commandTime.IsZero() {
		commandTime = time.Now().UTC()
	}
	if state.Status == StatusRoundEnd {
		state.TurnDeadline = time.Time{}
		state.TurnTimeout = 0
		if state.RoundTransitionDeadline.IsZero() {
			state.RoundTransitionDeadline = commandTime.UTC().
				Add(roundTransitionDelay)
			if state.RoundResult != nil {
				state.RoundResult.NextRoundDeadline =
					state.RoundTransitionDeadline
			}
		}
		return
	}
	state.RoundTransitionDeadline = time.Time{}
	if state.Status != StatusSelecting && state.Status != StatusChooseRow {
		state.TurnDeadline = time.Time{}
		state.TurnTimeout = 0
		return
	}
	if state.TurnDeadline.IsZero() {
		state.TurnTimeout = turnTimeout
		state.TurnDeadline = commandTime.UTC().Add(turnTimeout)
	}
}

func roomView(
	state *State,
	playerID,
	matchID string,
	seq int64,
	players map[string]any,
) map[string]any {
	var result any
	if state.Result != nil {
		result = cloneResult(state.Result)
	}
	return map[string]any{
		"type":        contracts.MessageTypeRoomSync,
		"roomID":      state.RoomID,
		"matchID":     matchID,
		"seq":         seq,
		"playerId":    playerID,
		"ownerID":     state.OwnerID,
		"visibility":  state.Visibility,
		"playerData":  publicPlayerData(state),
		"privateData": privateDataView(state, playerID),
		"result":      result,
		"roomData":    roomDataView(state, players),
	}
}

func roomDataView(state *State, players map[string]any) map[string]any {
	logs := state.Log
	if len(logs) > 80 {
		logs = logs[len(logs)-80:]
	}
	locked := []string{}
	for _, playerID := range state.PlayerOrder {
		if player := state.Roster[playerID]; player != nil &&
			player.LockedCard != nil {
			locked = append(locked, playerID)
		}
	}
	var roundResult any
	if state.RoundResult != nil {
		roundResult = roundResultView(state.RoundResult)
	}
	return map[string]any{
		"gameStatus":      state.Status,
		"roundNo":         state.RoundNo,
		"turnNo":          state.TurnNo,
		"rows":            rowViews(state.Rows),
		"lockedPlayerIDs": locked,
		"activityLog":     append([]Action(nil), logs...),
		"lastResolution":  cloneResolution(state.LastResolution),
		"pendingChoice":   clonePendingChoice(state.PendingChoice),
		"roundResult":     roundResult,
		"turnDeadline":    deadlineValue(state.TurnDeadline),
		"turnTimeoutMs":   state.TurnTimeout.Milliseconds(),
		"roundTransitionDeadline": deadlineValue(
			state.RoundTransitionDeadline,
		),
		"players":     players,
		"playerOrder": append([]string(nil), state.PlayerOrder...),
	}
}

func publicPlayerData(state *State) map[string]any {
	result := make(map[string]any, len(state.PlayerOrder))
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			continue
		}
		result[playerID] = map[string]any{
			"roundPenalty": player.RoundPenalty,
			"totalPenalty": player.TotalPenalty,
			"handCount":    len(player.Hand),
			"takenCount":   len(player.Taken),
			"locked":       player.LockedCard != nil,
		}
	}
	return result
}

func privateDataView(state *State, playerID string) map[string]any {
	player := state.Roster[playerID]
	hand := []int{}
	var locked any
	if player != nil {
		hand = append(hand, player.Hand...)
		if player.LockedCard != nil {
			locked = *player.LockedCard
		}
	}
	legal := []int{}
	if state.Status == StatusSelecting && player != nil &&
		player.LockedCard == nil {
		legal = append(legal, hand...)
	}
	return map[string]any{
		"hand":       hand,
		"legalCards": legal,
		"lockedCard": locked,
	}
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
				displayName = aiName(player.SeatIndex)
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
		}
	}
	return result
}

func connect(state *State, playerID string) error {
	return connectUser(state, playerID, playerID)
}

func connectUser(state *State, userID, playerID string) error {
	if player := state.Roster[playerID]; player != nil {
		player.Online = true
		if userID != "" {
			player.UserID = userID
		}
		return nil
	}
	if state.Status != StatusMatch {
		return errors.New("game already started")
	}
	if len(state.Roster) >= state.MaxPlayers {
		return errors.New("room is full")
	}
	if userID == "" {
		userID = playerID
	}
	state.Roster[playerID] = &Player{
		UserID:    userID,
		PlayerID:  playerID,
		Online:    true,
		Ready:     ownerActor(state, playerID, userID),
		SeatIndex: firstFreeSeat(state),
		Hand:      []int{},
		Taken:     []int{},
	}
	state.PlayerOrder = append(state.PlayerOrder, playerID)
	sortPlayerOrder(state)
	return nil
}

func setReady(state *State, playerID, userID string, payload []byte) error {
	if state.Status != StatusMatch {
		return errors.New("preparation phase has ended")
	}
	player := state.Roster[playerID]
	if player == nil {
		return errors.New("player not found")
	}
	var value struct {
		Ready *bool `json:"ready"`
	}
	if err := decodePayload(payload, &value); err != nil ||
		value.Ready == nil {
		return errors.New("invalid ready payload")
	}
	if ownerActor(state, playerID, userID) && !*value.Ready {
		return errors.New("owner is always ready")
	}
	player.Ready = *value.Ready
	return nil
}

func ownerActor(state *State, actorID, actorUserID string) bool {
	if actorID != "" && actorID == state.OwnerID {
		return true
	}
	if actorUserID != "" && actorUserID == state.OwnerID {
		return true
	}
	if actorUserID == "" {
		return false
	}
	if owner := state.Roster[state.OwnerID]; owner != nil && owner.UserID == actorUserID {
		return true
	}
	if actor := state.Roster[actorID]; actor != nil && actor.UserID != "" && actor.UserID == state.OwnerID {
		return true
	}
	return false
}

func addAI(state *State, actorID, actorUserID string, payload []byte) error {
	if !ownerActor(state, actorID, actorUserID) || state.Status != StatusMatch {
		return errors.New("only the owner can add AI during preparation")
	}
	if len(state.Roster) >= state.MaxPlayers {
		return errors.New("room is full")
	}
	seatIndex := firstFreeSeat(state)
	var value struct {
		SeatIndex *int `json:"seatIndex"`
	}
	if len(payload) > 0 && string(payload) != "null" {
		if err := json.Unmarshal(payload, &value); err != nil {
			return errors.New("invalid seat")
		}
	}
	if value.SeatIndex != nil {
		seatIndex = *value.SeatIndex
		if seatIndex < 0 ||
			seatIndex >= state.MaxPlayers ||
			seatOccupied(state, seatIndex) {
			return errors.New("seat is unavailable")
		}
	}
	playerID := fmt.Sprintf("ai_%d", seatIndex+1)
	for state.Roster[playerID] != nil {
		playerID += "_"
	}
	state.Roster[playerID] = &Player{
		PlayerID:    playerID,
		DisplayName: aiName(seatIndex),
		Online:      true,
		Ready:       true,
		GameReady:   true,
		AI:          true,
		SeatIndex:   seatIndex,
		Hand:        []int{},
		Taken:       []int{},
	}
	state.PlayerOrder = append(state.PlayerOrder, playerID)
	sortPlayerOrder(state)
	return nil
}

func removePlayer(
	state *State,
	actorID string,
	actorUserID string,
	payload []byte,
) (string, error) {
	if !ownerActor(state, actorID, actorUserID) || state.Status != StatusMatch {
		return "", errors.New(
			"only the owner can remove players during preparation",
		)
	}
	var value struct {
		PlayerID string `json:"playerID"`
	}
	if err := decodePayload(payload, &value); err != nil ||
		value.PlayerID == "" ||
		value.PlayerID == state.OwnerID {
		return "", errors.New("invalid player")
	}
	if state.Roster[value.PlayerID] == nil {
		return "", errors.New("player not found")
	}
	delete(state.Roster, value.PlayerID)
	state.PlayerOrder = removeString(state.PlayerOrder, value.PlayerID)
	return value.PlayerID, nil
}

func updateVisibility(state *State, actorID, actorUserID string, payload []byte) error {
	if !ownerActor(state, actorID, actorUserID) || state.Status != StatusMatch {
		return errors.New("only the owner can update room settings")
	}
	var value struct {
		Visibility string `json:"visibility"`
	}
	if err := decodePayload(payload, &value); err != nil ||
		(value.Visibility != contracts.VisibilityPublic &&
			value.Visibility != contracts.VisibilityPrivate) {
		return errors.New("invalid visibility")
	}
	state.Visibility = value.Visibility
	return nil
}

func beginMatch(state *State, actorID, actorUserID string) error {
	if !ownerActor(state, actorID, actorUserID) || state.Status != StatusMatch {
		return errors.New("only the owner can start the match")
	}
	if len(state.PlayerOrder) < 2 ||
		len(state.PlayerOrder) > defaultMaxPlayers {
		return errors.New("sixnimmt requires two to ten players")
	}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil || !player.Ready || !player.Online {
			return errors.New("all players must be ready and online")
		}
		player.Ready = player.AI
		player.GameReady = player.AI
		player.TotalPenalty = 0
		player.RoundPenalty = 0
		player.Hand = []int{}
		player.Taken = []int{}
		player.LockedCard = nil
		player.AutomationKind = ""
	}
	state.RoundNo = 0
	state.TurnNo = 0
	state.Rows = [][]int{}
	state.UnusedCards = []int{}
	state.Queue = []Selection{}
	state.QueueCursor = 0
	state.Log = []Action{}
	state.LastResolution = nil
	state.PendingChoice = nil
	state.RoundResult = nil
	state.Result = nil
	state.Status = StatusWaiting
	return nil
}

func gameReady(state *State, playerID string, sentAt time.Time) error {
	if state.Status != StatusWaiting {
		return nil
	}
	player := state.Roster[playerID]
	if player == nil {
		return errors.New("player not found")
	}
	player.Ready = true
	player.GameReady = true
	for _, currentID := range state.PlayerOrder {
		current := state.Roster[currentID]
		if current == nil || !current.GameReady {
			return nil
		}
	}
	if sentAt.IsZero() {
		sentAt = time.Now().UTC()
	}
	state.GameStartTime = sentAt.UTC()
	return setupRound(state, 1)
}

func asState(state game.State) (*State, error) {
	switch typed := state.(type) {
	case *State:
		if typed == nil {
			return nil, errors.New("sixnimmt state is nil")
		}
		ensureState(typed)
		return typed, nil
	case State:
		ensureState(&typed)
		return &typed, nil
	default:
		return nil, fmt.Errorf("unexpected sixnimmt state %T", state)
	}
}

func ensureState(state *State) {
	if state.MaxPlayers <= 0 {
		state.MaxPlayers = defaultMaxPlayers
	}
	if state.Visibility == "" {
		state.Visibility = contracts.VisibilityPublic
	}
	if state.TurnTimeoutSeconds <= 0 {
		state.TurnTimeoutSeconds = int(turnTimeout / time.Second)
	}
	if state.Roster == nil {
		state.Roster = map[string]*Player{}
	}
	if state.PlayerOrder == nil {
		state.PlayerOrder = []string{}
	}
	if state.Rows == nil {
		state.Rows = [][]int{}
	}
	if state.UnusedCards == nil {
		state.UnusedCards = []int{}
	}
	if state.Queue == nil {
		state.Queue = []Selection{}
	}
	if state.Log == nil {
		state.Log = []Action{}
	}
	for _, player := range state.Roster {
		if player == nil {
			continue
		}
		if player.Hand == nil {
			player.Hand = []int{}
		}
		if player.Taken == nil {
			player.Taken = []int{}
		}
	}
}
