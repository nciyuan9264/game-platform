package cantstop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

const (
	GameType            = "cantstop"
	EngineVersion       = "cantstop-v3"
	ReplaySchemaVersion = 1
	defaultMaxPlayers   = 4
	maxNoHumanChecks    = 3
)

const (
	CommandRollDice   = "game_roll_dice"
	CommandAdvance    = "game_advance"
	CommandBank       = "game_bank"
	CommandNextTurn   = "game_next_turn"
	CommandTimeout    = "turn_timeout"
	CommandTransition = "turn_transition_timeout"
)

const (
	turnTimeout     = 60 * time.Second
	transitionDelay = 4 * time.Second
	aiMinDelay      = 3 * time.Second
	aiMaxDelay      = 5 * time.Second
	aiTimeout       = 750 * time.Millisecond
)

var aiNames = []string{"小满", "阿橙", "松子", "米拉"}

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
	maximum := cfg.MaxPlayers
	if maximum <= 0 {
		maximum = defaultMaxPlayers
	}
	if maximum != defaultMaxPlayers {
		return nil, errors.New("can't stop requires four room seats")
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
		MaxPlayers:         maximum,
		Visibility:         visibility,
		TurnTimeoutSeconds: int(turnTimeout / time.Second),
		RandomSeed:         cfg.RandomSeed,
		MatchRoundNo:       roundNo,
		Roster:             map[string]*Player{},
		PlayerOrder:        []string{},
		Dice:               []int{},
		PairingOptions:     []PairingOption{},
		Runners:            map[int]int{},
		ClaimedBy:          map[int]string{},
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
	maxClaimed := 0
	for _, playerID := range typed.PlayerOrder {
		if count := claimedColumnCount(typed, playerID); count > maxClaimed {
			maxClaimed = count
		}
	}
	return game.RoomProgress{Current: maxClaimed, Total: targetColumns}, nil
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
	staged := cloneState(typed)
	payload, err := json.Marshal(command.Payload)
	if err != nil {
		return game.ApplyResult{}, err
	}
	removedPlayerID := ""
	refreshAutomation := false
	source := commandSource(staged, command)
	switch command.Type {
	case "connect":
		err = connect(staged, command.UserID, command.PlayerID)
	case "disconnect":
		if player := staged.Roster[command.PlayerID]; player != nil {
			player.Online = false
		}
	case "match_ready":
		err = setReady(staged, command.PlayerID, payload)
	case "match_add_ai":
		err = addAI(staged, command.PlayerID, payload)
	case "match_remove_player":
		removedPlayerID, err = removePlayer(staged, command.PlayerID, payload)
	case "room_update_settings":
		err = updateVisibility(staged, command.PlayerID, payload)
	case "match_begin":
		err = beginMatch(staged, command.PlayerID)
	case "game_ready":
		err = gameReady(staged, command.PlayerID, command.SentAt)
		refreshAutomation = err == nil
	case CommandRollDice:
		err = rollDice(staged, command.PlayerID, source)
		refreshAutomation = err == nil
	case CommandAdvance:
		var value struct {
			ActionID string `json:"actionID"`
		}
		err = decodePayload(payload, &value)
		if err == nil && value.ActionID == "" {
			err = errors.New("actionID is required")
		}
		if err == nil {
			err = advance(staged, command.PlayerID, value.ActionID, source)
			refreshAutomation = err == nil
		}
	case CommandBank:
		err = bank(staged, command.PlayerID, source)
		refreshAutomation = err == nil
	case CommandNextTurn:
		err = nextTurn(staged, command.PlayerID, source)
		refreshAutomation = err == nil
	case CommandTimeout:
		if command.ConnectionRef.Valid() {
			err = errors.New("client cannot submit timeout commands")
		} else {
			err = p.applyTimeout(ctx, staged, command.PlayerID)
			refreshAutomation = err == nil
		}
	case CommandTransition:
		if command.ConnectionRef.Valid() {
			err = errors.New("client cannot submit transition commands")
		} else {
			err = nextTurn(staged, command.PlayerID, SourceTimeout)
			refreshAutomation = err == nil
		}
	case "game_restart_game":
		err = errors.New("finished rooms must start a new match")
	default:
		err = fmt.Errorf("unknown can't stop command %q", command.Type)
	}
	if err != nil {
		return game.ApplyResult{}, err
	}
	if refreshAutomation {
		armAutomation(staged, command.SentAt)
	}
	if err := validateState(staged); err != nil {
		return game.ApplyResult{}, err
	}
	*typed = *staged
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
	switch typed.Status {
	case StatusRoll, StatusChoose, StatusDecide:
		if typed.TurnDeadline.IsZero() || typed.CurrentPlayer == "" {
			return nil, nil
		}
		player := typed.Roster[typed.CurrentPlayer]
		if player == nil || player.AI {
			return nil, nil
		}
		return []game.ScheduledCommand{{
			Key: fmt.Sprintf(
				"%s:%d:%d:%s:%d",
				typed.Status,
				typed.TurnNumber,
				typed.RollNumber,
				typed.CurrentPlayer,
				typed.TurnDeadline.UnixNano(),
			),
			ExecuteAt: typed.TurnDeadline,
			PlayerID:  typed.CurrentPlayer,
			Type:      CommandTimeout,
		}}, nil
	case StatusBust, StatusBanked:
		if typed.TransitionDeadline.IsZero() {
			return nil, nil
		}
		return []game.ScheduledCommand{{
			Key: fmt.Sprintf(
				"transition:%d:%s:%d",
				typed.TurnNumber,
				typed.Status,
				typed.TransitionDeadline.UnixNano(),
			),
			ExecuteAt: typed.TransitionDeadline,
			PlayerID:  typed.OwnerID,
			Type:      CommandTransition,
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
			"playerOrder": append([]string(nil), typed.PlayerOrder...),
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

func (p *Plugin) BuildSpectatorView(
	_ context.Context,
	state game.State,
	seq int64,
) (any, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	return roomView(typed, "", "", seq, playerViews(typed, nil)), nil
}

func (p *Plugin) applyTimeout(
	ctx context.Context,
	state *State,
	playerID string,
) error {
	command, err := p.Decide(ctx, game.AIRequest{
		RoomID: state.RoomID, GameType: GameType,
		PlayerID: playerID, State: cloneState(state),
	})
	if err != nil {
		command, err = timeoutFallback(state, playerID)
		if err != nil {
			return err
		}
	}
	switch command.Type {
	case CommandRollDice:
		return rollDice(state, playerID, SourceTimeout)
	case CommandAdvance:
		actionID := stringPayload(command.Payload, "actionID")
		return advance(state, playerID, actionID, SourceTimeout)
	case CommandBank:
		return bank(state, playerID, SourceTimeout)
	default:
		return errors.New("timeout planner returned an unsupported action")
	}
}

func commandSource(state *State, command contracts.RoomCommand) string {
	if command.Type == CommandTimeout || command.Type == CommandTransition {
		return SourceTimeout
	}
	if player := state.Roster[command.PlayerID]; player != nil && player.AI {
		return SourceAI
	}
	return SourceManual
}

func armAutomation(state *State, sentAt time.Time) {
	if sentAt.IsZero() {
		sentAt = time.Now().UTC()
	}
	sentAt = sentAt.UTC()
	switch state.Status {
	case StatusRoll, StatusChoose, StatusDecide:
		state.TransitionDeadline = time.Time{}
		state.TransitionTimeout = 0
		state.TurnDeadline = time.Time{}
		state.TurnTimeout = 0
		player := state.Roster[state.CurrentPlayer]
		if player != nil && !player.AI {
			state.TurnTimeout = turnTimeout
			state.TurnDeadline = sentAt.Add(turnTimeout)
		}
	case StatusBust, StatusBanked:
		state.TurnDeadline = time.Time{}
		state.TurnTimeout = 0
		if state.TransitionDeadline.IsZero() {
			state.TransitionTimeout = transitionDelay
			state.TransitionDeadline = sentAt.Add(transitionDelay)
		}
	default:
		state.TurnDeadline = time.Time{}
		state.TurnTimeout = 0
		state.TransitionDeadline = time.Time{}
		state.TransitionTimeout = 0
	}
}

func nextAIRequest(state *State) *game.AIRequest {
	player := state.Roster[state.CurrentPlayer]
	if player == nil || !player.AI {
		return nil
	}
	switch state.Status {
	case StatusRoll, StatusChoose, StatusDecide:
	default:
		return nil
	}
	delayRange := int((aiMaxDelay - aiMinDelay) / time.Millisecond)
	offset := int((state.RandomSeed +
		uint64(state.TurnNumber*7919) +
		uint64(state.RollNumber*104729) +
		uint64(player.SeatIndex*15485863) +
		uint64(len(state.Log))*32452843) % uint64(delayRange+1))
	fallback := contracts.RoomCommand{
		Type:     CommandTimeout,
		PlayerID: state.CurrentPlayer,
	}
	return &game.AIRequest{
		RoomID: state.RoomID, GameType: GameType,
		PlayerID: state.CurrentPlayer, State: cloneState(state),
		Delay:   aiMinDelay + time.Duration(offset)*time.Millisecond,
		Timeout: aiTimeout, FallbackCommand: &fallback,
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
		"type":       contracts.MessageTypeRoomSync,
		"roomID":     state.RoomID,
		"matchID":    matchID,
		"seq":        seq,
		"playerId":   playerID,
		"ownerID":    state.OwnerID,
		"visibility": state.Visibility,
		"playerData": publicPlayerData(state),
		"result":     result,
		"roomData":   roomDataView(state, players),
	}
}

func roomDataView(state *State, players map[string]any) map[string]any {
	logs := state.Log
	if len(logs) > 80 {
		logs = logs[len(logs)-80:]
	}
	var lastAction any
	if state.LastAction != nil {
		lastAction = cloneAction(*state.LastAction)
	}
	return map[string]any{
		"gameStatus":              state.Status,
		"currentPlayer":           state.CurrentPlayer,
		"nextPlayer":              state.NextPlayer,
		"turnNumber":              state.TurnNumber,
		"rollNumber":              state.RollNumber,
		"dice":                    append([]int(nil), state.Dice...),
		"pairingOptions":          clonePairingOptions(state.PairingOptions),
		"runners":                 cloneProgress(state.Runners),
		"claimedBy":               cloneClaimedBy(state.ClaimedBy),
		"activityLog":             append([]Action(nil), logs...),
		"lastAction":              lastAction,
		"bustRisk":                bustRisk(state),
		"turnGain":                turnGain(state),
		"lostGain":                state.LostGain,
		"turnDeadline":            deadlineValue(state.TurnDeadline),
		"turnTimeoutMs":           timeoutMilliseconds(state.TurnTimeout),
		"turnTransitionDeadline":  deadlineValue(state.TransitionDeadline),
		"turnTransitionTimeoutMs": timeoutMilliseconds(state.TransitionTimeout),
		"columnHeights":           cloneProgress(columnHeights),
		"targetColumns":           targetColumns,
		"players":                 players,
		"playerOrder":             append([]string(nil), state.PlayerOrder...),
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
			"savedProgress":  cloneProgress(player.Saved),
			"claimedColumns": claimedColumnsFor(state, playerID),
			"savedStepCount": savedStepCount(player),
			"busts":          player.Busts,
		}
	}
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
			"gameReady":   player.GameReady,
			"seatIndex":   player.SeatIndex,
		}
	}
	return result
}

func connect(state *State, userID, playerID string) error {
	if playerID == "" {
		return errors.New("player ID is required")
	}
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
		Ready:     playerID == state.OwnerID,
		SeatIndex: firstFreeSeat(state),
		Saved:     map[int]int{},
	}
	state.PlayerOrder = append(state.PlayerOrder, playerID)
	sortPlayerOrder(state)
	return nil
}

func setReady(state *State, playerID string, payload []byte) error {
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
	if err := decodePayload(payload, &value); err != nil || value.Ready == nil {
		return errors.New("invalid ready payload")
	}
	if playerID == state.OwnerID && !*value.Ready {
		return errors.New("owner is always ready")
	}
	player.Ready = *value.Ready
	return nil
}

func addAI(state *State, actorID string, payload []byte) error {
	if actorID != state.OwnerID || state.Status != StatusMatch {
		return errors.New("only the owner can add AI during preparation")
	}
	if len(state.Roster) >= state.MaxPlayers {
		return errors.New("room is full")
	}
	seat := firstFreeSeat(state)
	var value struct {
		SeatIndex *int `json:"seatIndex"`
	}
	if len(payload) > 0 && string(payload) != "null" {
		if err := json.Unmarshal(payload, &value); err != nil {
			return errors.New("invalid seat")
		}
	}
	if value.SeatIndex != nil {
		seat = *value.SeatIndex
		if seat < 0 || seat >= state.MaxPlayers || seatOccupied(state, seat) {
			return errors.New("seat is unavailable")
		}
	}
	playerID := fmt.Sprintf("ai_%d", seat+1)
	for state.Roster[playerID] != nil {
		playerID += "_"
	}
	state.Roster[playerID] = &Player{
		PlayerID:    playerID,
		DisplayName: aiName(seat),
		Online:      true,
		Ready:       true,
		GameReady:   true,
		AI:          true,
		SeatIndex:   seat,
		Saved:       map[int]int{},
	}
	state.PlayerOrder = append(state.PlayerOrder, playerID)
	sortPlayerOrder(state)
	return nil
}

func removePlayer(
	state *State,
	actorID string,
	payload []byte,
) (string, error) {
	if actorID != state.OwnerID || state.Status != StatusMatch {
		return "", errors.New("only the owner can remove players during preparation")
	}
	var value struct {
		PlayerID string `json:"playerID"`
	}
	if err := decodePayload(payload, &value); err != nil ||
		value.PlayerID == "" || value.PlayerID == state.OwnerID {
		return "", errors.New("invalid player")
	}
	if state.Roster[value.PlayerID] == nil {
		return "", errors.New("player not found")
	}
	delete(state.Roster, value.PlayerID)
	state.PlayerOrder = removeString(state.PlayerOrder, value.PlayerID)
	return value.PlayerID, nil
}

func updateVisibility(state *State, actorID string, payload []byte) error {
	if actorID != state.OwnerID || state.Status != StatusMatch {
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

func beginMatch(state *State, actorID string) error {
	if actorID != state.OwnerID || state.Status != StatusMatch {
		return errors.New("only the owner can start the match")
	}
	if len(state.PlayerOrder) < minPlayers || len(state.PlayerOrder) > maxPlayers {
		return errors.New("can't stop requires two to four players")
	}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil || !player.Ready || !player.Online {
			return errors.New("all players must be ready and online")
		}
		player.Ready = player.AI
		player.GameReady = player.AI
		player.Saved = map[int]int{}
		player.Busts = 0
	}
	state.Status = StatusWaiting
	state.CurrentPlayer = ""
	state.NextPlayer = ""
	state.FirstPlayer = ""
	state.TurnNumber = 0
	state.RollNumber = 0
	state.TotalRolls = 0
	state.Dice = []int{}
	state.PairingOptions = []PairingOption{}
	state.Runners = map[int]int{}
	state.ClaimedBy = map[int]string{}
	state.LostGain = 0
	state.Log = []Action{}
	state.LastAction = nil
	state.Result = nil
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
	return setupGame(state, sentAt)
}

func asState(state game.State) (*State, error) {
	var typed *State
	switch value := state.(type) {
	case *State:
		typed = value
	case State:
		copyValue := value
		typed = &copyValue
	default:
		return nil, fmt.Errorf("unexpected can't stop state %T", state)
	}
	if typed == nil {
		return nil, errors.New("can't stop state is nil")
	}
	ensureState(typed)
	return typed, nil
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
	if state.Dice == nil {
		state.Dice = []int{}
	}
	if state.PairingOptions == nil {
		state.PairingOptions = []PairingOption{}
	}
	if state.Runners == nil {
		state.Runners = map[int]int{}
	}
	if state.ClaimedBy == nil {
		state.ClaimedBy = map[int]string{}
	}
	if state.Log == nil {
		state.Log = []Action{}
	}
	for _, player := range state.Roster {
		if player != nil && player.Saved == nil {
			player.Saved = map[int]int{}
		}
	}
}

func validateState(state *State) error {
	if state == nil {
		return errors.New("can't stop state is nil")
	}
	if state.RandomSeed == 0 {
		return errors.New("can't stop state is missing random seed")
	}
	if state.MaxPlayers != defaultMaxPlayers {
		return errors.New("can't stop state must expose four seats")
	}
	if state.Visibility != contracts.VisibilityPublic &&
		state.Visibility != contracts.VisibilityPrivate {
		return errors.New("can't stop state has invalid visibility")
	}
	switch state.Status {
	case StatusMatch, StatusWaiting, StatusRoll, StatusChoose,
		StatusDecide, StatusBust, StatusBanked, StatusEnd:
	default:
		return errors.New("can't stop state has invalid status")
	}
	if state.Roster == nil || state.PlayerOrder == nil ||
		state.Dice == nil || state.PairingOptions == nil ||
		state.Runners == nil || state.ClaimedBy == nil ||
		state.Log == nil {
		return errors.New("can't stop state has nil collections")
	}
	if err := validateRoster(state); err != nil {
		return err
	}
	if state.Status == StatusMatch {
		return validateLobbyState(state)
	}
	if state.Status != StatusWaiting &&
		(len(state.PlayerOrder) < minPlayers ||
			len(state.PlayerOrder) > maxPlayers) {
		return errors.New("can't stop game has invalid player count")
	}
	if err := validateProgress(state); err != nil {
		return err
	}
	if err := validateActions(state); err != nil {
		return err
	}
	return validatePhase(state)
}

func validateRoster(state *State) error {
	if len(state.Roster) != len(state.PlayerOrder) ||
		len(state.Roster) > state.MaxPlayers {
		return errors.New("can't stop roster is inconsistent")
	}
	seenSeats := map[int]bool{}
	seenPlayers := map[string]bool{}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil ||
			player.PlayerID != playerID ||
			player.SeatIndex < 0 ||
			player.SeatIndex >= state.MaxPlayers ||
			seenSeats[player.SeatIndex] ||
			seenPlayers[playerID] {
			return errors.New("can't stop roster contains invalid player")
		}
		seenSeats[player.SeatIndex] = true
		seenPlayers[playerID] = true
		if player.Saved == nil {
			return errors.New("can't stop player has nil progress")
		}
	}
	return nil
}

func validateLobbyState(state *State) error {
	if state.CurrentPlayer != "" || state.NextPlayer != "" ||
		state.FirstPlayer != "" || state.TurnNumber != 0 ||
		state.RollNumber != 0 || state.TotalRolls != 0 ||
		len(state.Dice) != 0 || len(state.PairingOptions) != 0 ||
		len(state.Runners) != 0 || len(state.ClaimedBy) != 0 ||
		len(state.Log) != 0 || state.LastAction != nil ||
		state.Result != nil {
		return errors.New("can't stop lobby contains game state")
	}
	return nil
}

func validateProgress(state *State) error {
	for playerID, player := range state.Roster {
		if player == nil {
			continue
		}
		for column, position := range player.Saved {
			height := columnHeight(column)
			if height == 0 || position <= 0 || position > height {
				return errors.New("can't stop saved progress is invalid")
			}
			owner := state.ClaimedBy[column]
			if owner != "" && owner != playerID {
				return errors.New("can't stop claimed progress leaked")
			}
			if owner == playerID && position != height {
				return errors.New("can't stop claimed column is not complete")
			}
		}
	}
	for column, owner := range state.ClaimedBy {
		height := columnHeight(column)
		player := state.Roster[owner]
		if height == 0 || player == nil || player.Saved[column] != height {
			return errors.New("can't stop claimed column is invalid")
		}
	}
	if len(state.Runners) > targetColumns {
		return errors.New("can't stop has too many runners")
	}
	current := state.Roster[state.CurrentPlayer]
	for column, position := range state.Runners {
		height := columnHeight(column)
		if height == 0 || state.ClaimedBy[column] != "" ||
			position <= 0 || position > height {
			return errors.New("can't stop runner is invalid")
		}
		saved := 0
		if current != nil {
			saved = current.Saved[column]
		}
		if position <= saved {
			return errors.New("can't stop runner does not advance progress")
		}
	}
	return nil
}

func validateActions(state *State) error {
	for index, action := range state.Log {
		if action.ID != index+1 || state.Roster[action.PlayerID] == nil {
			return errors.New("can't stop action log is invalid")
		}
		switch action.Kind {
		case "roll":
			if len(action.Dice) != dicePerRoll {
				return errors.New("can't stop roll action has invalid dice")
			}
		case "advance":
			if action.ActionID == "" || action.Gain <= 0 {
				return errors.New("can't stop advance action is invalid")
			}
		case "bank", "bust", "nextTurn":
		default:
			return fmt.Errorf("can't stop action kind %q is invalid", action.Kind)
		}
	}
	if len(state.Log) == 0 {
		if state.LastAction != nil {
			return errors.New("can't stop empty log has last action")
		}
		return nil
	}
	if state.LastAction == nil ||
		state.LastAction.ID != state.Log[len(state.Log)-1].ID {
		return errors.New("can't stop last action is inconsistent")
	}
	return nil
}

func validatePhase(state *State) error {
	switch state.Status {
	case StatusWaiting:
		return nil
	case StatusRoll:
		if state.CurrentPlayer == "" || state.Roster[state.CurrentPlayer] == nil ||
			len(state.Dice) != 0 || len(state.PairingOptions) != 0 {
			return errors.New("can't stop roll phase is invalid")
		}
	case StatusChoose:
		if state.CurrentPlayer == "" || len(state.Dice) != dicePerRoll ||
			len(state.PairingOptions) == 0 {
			return errors.New("can't stop choose phase is invalid")
		}
	case StatusDecide:
		if state.CurrentPlayer == "" || turnGain(state) <= 0 ||
			len(state.PairingOptions) != 0 {
			return errors.New("can't stop decide phase is invalid")
		}
	case StatusBust, StatusBanked:
		if state.CurrentPlayer == "" || state.NextPlayer == "" ||
			state.Roster[state.NextPlayer] == nil ||
			len(state.Runners) != 0 || len(state.PairingOptions) != 0 {
			return errors.New("can't stop transition phase is invalid")
		}
	case StatusEnd:
		if state.Result == nil || state.Result.WinnerID == "" ||
			claimedColumnCount(state, state.Result.WinnerID) < targetColumns {
			return errors.New("can't stop result is invalid")
		}
	}
	return nil
}

func decodePayload(data []byte, target any) error {
	if len(data) == 0 || string(data) == "null" {
		return errors.New("payload is required")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return errors.New("invalid command payload")
	}
	return nil
}

func stringPayload(payload any, key string) string {
	values, _ := payload.(map[string]any)
	if value, ok := values[key].(string); ok {
		return value
	}
	return ""
}

func firstFreeSeat(state *State) int {
	for seat := 0; seat < state.MaxPlayers; seat++ {
		if !seatOccupied(state, seat) {
			return seat
		}
	}
	return state.MaxPlayers
}

func seatOccupied(state *State, seat int) bool {
	for _, player := range state.Roster {
		if player != nil && player.SeatIndex == seat {
			return true
		}
	}
	return false
}

func sortPlayerOrder(state *State) {
	sort.SliceStable(state.PlayerOrder, func(i, j int) bool {
		return playerSeat(state, state.PlayerOrder[i]) <
			playerSeat(state, state.PlayerOrder[j])
	})
}

func playerSeat(state *State, playerID string) int {
	if player := state.Roster[playerID]; player != nil {
		return player.SeatIndex
	}
	return state.MaxPlayers
}

func removeString(values []string, target string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func deadlineValue(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func timeoutMilliseconds(value time.Duration) any {
	if value <= 0 {
		return nil
	}
	return value.Milliseconds()
}

func cloneClaimedBy(value map[int]string) map[int]string {
	result := make(map[int]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func clonePairingOptions(values []PairingOption) []PairingOption {
	result := make([]PairingOption, len(values))
	for index, value := range values {
		result[index] = clonePairingOption(value)
	}
	return result
}

func cloneResult(value *Result) any {
	if value == nil {
		return nil
	}
	return map[string]any{
		"winnerID":       value.WinnerID,
		"claimedColumns": append([]int(nil), value.ClaimedColumns...),
		"turnCount":      value.TurnCount,
		"rollCount":      value.RollCount,
		"bustCounts":     cloneStringIntMap(value.BustCounts),
		"reason":         value.Reason,
	}
}

func aiName(seat int) string {
	if seat >= 0 && seat < len(aiNames) {
		return aiNames[seat]
	}
	return fmt.Sprintf("AI %d", seat+1)
}
