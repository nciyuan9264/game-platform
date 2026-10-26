package yinsh

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
	GameType            = "yinsh"
	EngineVersion       = "yinsh-v2"
	ReplaySchemaVersion = 1
	defaultMaxPlayers   = 2
	maxNoHumanChecks    = 3
)

const (
	CommandPlaceRing  = "game_place_ring"
	CommandMoveRing   = "game_move_ring"
	CommandRemoveRow  = "game_remove_row"
	CommandRemoveRing = "game_remove_ring"
	CommandResign     = "game_resign"
	CommandTimeout    = "turn_timeout"
)

const (
	placementTimeout  = 30 * time.Second
	moveTimeout       = 60 * time.Second
	resolutionTimeout = 30 * time.Second
	aiMinDelay        = 3 * time.Second
	aiMaxDelay        = 5 * time.Second
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

func (p *Plugin) InitRoom(_ context.Context, cfg game.RoomConfig) (game.State, error) {
	if cfg.RandomSeed == 0 {
		return nil, errors.New("random seed is required")
	}
	maxPlayers := cfg.MaxPlayers
	if maxPlayers <= 0 {
		maxPlayers = defaultMaxPlayers
	}
	if maxPlayers != defaultMaxPlayers {
		return nil, errors.New("yinsh requires exactly two seats")
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
		RoomID:       cfg.RoomID,
		OwnerID:      cfg.OwnerPlayerID,
		Status:       StatusMatch,
		MaxPlayers:   maxPlayers,
		Visibility:   visibility,
		RandomSeed:   cfg.RandomSeed,
		RoundNo:      roundNo,
		Roster:       map[string]*Player{},
		PlayerOrder:  []string{},
		Rings:        map[string]Side{},
		Markers:      map[string]Side{},
		PlacedRings:  map[Side]int{SideWhite: 0, SideBlack: 0},
		RingsRemoved: map[Side]int{SideWhite: 0, SideBlack: 0},
		Log:          []Action{},
	}, nil
}

func (p *Plugin) EncodeState(_ context.Context, state game.State) ([]byte, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	return json.Marshal(typed)
}

func (p *Plugin) DecodeState(_ context.Context, data []byte) (game.State, error) {
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	ensureState(&state)
	return &state, nil
}

func (p *Plugin) RandomSeed(_ context.Context, state game.State) (uint64, error) {
	typed, err := asState(state)
	if err != nil {
		return 0, err
	}
	return typed.RandomSeed, nil
}

func (p *Plugin) RoomMetadata(_ context.Context, state game.State) (game.RoomMetadata, error) {
	typed, err := asState(state)
	if err != nil {
		return game.RoomMetadata{}, err
	}
	return game.RoomMetadata{Visibility: typed.Visibility}, nil
}

func (p *Plugin) Roster(_ context.Context, state game.State) ([]game.RosterPlayer, error) {
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

func (p *Plugin) Progress(_ context.Context, state game.State) (game.RoomProgress, error) {
	typed, err := asState(state)
	if err != nil {
		return game.RoomProgress{}, err
	}
	current := typed.RingsRemoved[SideWhite]
	if typed.RingsRemoved[SideBlack] > current {
		current = typed.RingsRemoved[SideBlack]
	}
	return game.RoomProgress{Current: current, Total: 3}, nil
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
	_ context.Context,
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
		removedPlayerID, err = removePlayer(typed, command.PlayerID, payload)
	case "room_update_settings":
		err = updateVisibility(typed, command.PlayerID, payload)
	case "match_begin":
		err = beginMatch(typed, command.PlayerID)
	case "game_ready":
		err = gameReady(typed, command.PlayerID, command.SentAt)
	case CommandPlaceRing:
		var value struct {
			PointID string `json:"pointID"`
		}
		err = decodePayload(payload, &value)
		if err == nil {
			err = placeRing(typed, command.PlayerID, value.PointID)
		}
	case CommandMoveRing:
		var value struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		err = decodePayload(payload, &value)
		if err == nil {
			err = moveRing(typed, command.PlayerID, value.From, value.To)
		}
	case CommandRemoveRow:
		var value struct {
			RowID string `json:"rowID"`
		}
		err = decodePayload(payload, &value)
		if err == nil {
			err = removeRow(typed, command.PlayerID, value.RowID)
		}
	case CommandRemoveRing:
		var value struct {
			PointID string `json:"pointID"`
		}
		err = decodePayload(payload, &value)
		if err == nil {
			err = removeRing(typed, command.PlayerID, value.PointID)
		}
	case CommandResign:
		err = resign(typed, command.PlayerID)
	case CommandTimeout:
		if command.ConnectionRef.Valid() {
			err = errors.New("client cannot submit timeout commands")
		} else {
			err = applyTimeout(typed, command.PlayerID)
		}
	case "game_restart_game":
		err = errors.New("finished rooms must start a new match")
	default:
		err = fmt.Errorf("unknown yinsh command %q", command.Type)
	}
	if err != nil {
		return game.ApplyResult{}, err
	}
	if command.Type != "connect" && command.Type != "disconnect" {
		updateTurnDeadline(typed, command.SentAt)
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
		Key:       fmt.Sprintf("%s:%s:%d", typed.Status, typed.CurrentPlayer, typed.TurnDeadline.UnixNano()),
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
	return roomView(typed, request.PlayerID, request.MatchID, request.Seq, players), nil
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

func roomView(
	state *State,
	playerID,
	matchID string,
	seq int64,
	players map[string]any,
) map[string]any {
	playerData := map[string]any{}
	for _, id := range state.PlayerOrder {
		player := state.Roster[id]
		if player == nil {
			continue
		}
		playerData[id] = map[string]any{
			"side":         player.Side,
			"ringsRemoved": state.RingsRemoved[player.Side],
		}
	}
	candidateRows := []Row{}
	if state.Status == StatusRemoveRow {
		candidateRows = rowsFor(state, sideForPlayer(state, state.ResolverPlayer))
	}
	legal := map[string][]MoveOption{}
	if state.Status == StatusMove {
		legal = allLegalMoves(state, state.CurrentPlayer)
	}
	var lastAction any
	if state.LastAction != nil {
		lastAction = actionView(*state.LastAction)
	}
	activity := make([]any, 0, len(state.Log))
	for _, action := range state.Log {
		activity = append(activity, actionView(action))
	}
	var result any
	if state.Status == StatusEnd {
		result = resultView(state)
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
		"result":     result,
		"roomData": map[string]any{
			"rings":          state.Rings,
			"markers":        state.Markers,
			"currentPlayer":  state.CurrentPlayer,
			"moverPlayer":    optionalString(state.MoverPlayer),
			"resolverPlayer": optionalString(state.ResolverPlayer),
			"gameStatus":     state.Status,
			"placedRings":    state.PlacedRings,
			"markerPool":     markerPool(state),
			"legalMoves":     legal,
			"candidateRows":  candidateRows,
			"lastAction":     lastAction,
			"turnDeadline":   deadlineValue(state.TurnDeadline),
			"turnTimeoutMs":  state.TurnTimeout.Milliseconds(),
			"players":        players,
			"playerOrder":    state.PlayerOrder,
			"roundNo":        state.RoundNo,
			"activity":       activity,
		},
	}
}

func actionView(action Action) map[string]any {
	payload, _ := json.Marshal(action.Payload)
	return map[string]any{
		"action":      action.Action,
		"playerID":    action.PlayerID,
		"payloadJSON": string(payload),
	}
}

func resultView(state *State) map[string]any {
	result := map[string]any{
		"reason":       state.EndReason,
		"ringsRemoved": state.RingsRemoved,
	}
	if state.WinnerID != "" {
		result["winnerID"] = state.WinnerID
	}
	return result
}

func playerViews(state *State, profiles map[string]game.PlayerProfile) map[string]any {
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
				displayName = "墨白"
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
	if player := state.Roster[playerID]; player != nil {
		player.Online = true
		return nil
	}
	if state.Status != StatusMatch {
		return errors.New("game already started")
	}
	if len(state.Roster) >= state.MaxPlayers {
		return errors.New("room is full")
	}
	state.Roster[playerID] = &Player{
		UserID:    playerID,
		PlayerID:  playerID,
		Online:    true,
		Ready:     playerID == state.OwnerID,
		SeatIndex: firstFreeSeat(state),
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
		if seatIndex < 0 || seatIndex >= state.MaxPlayers || seatOccupied(state, seatIndex) {
			return errors.New("seat is unavailable")
		}
	}
	playerID := fmt.Sprintf("ai_%d", seatIndex+1)
	for state.Roster[playerID] != nil {
		playerID += "_"
	}
	state.Roster[playerID] = &Player{
		PlayerID:    playerID,
		DisplayName: "墨白",
		Online:      true,
		Ready:       true,
		GameReady:   true,
		AI:          true,
		SeatIndex:   seatIndex,
	}
	state.PlayerOrder = append(state.PlayerOrder, playerID)
	sortPlayerOrder(state)
	return nil
}

func removePlayer(state *State, actorID string, payload []byte) (string, error) {
	if actorID != state.OwnerID || state.Status != StatusMatch {
		return "", errors.New("only the owner can remove players during preparation")
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
	if len(state.Roster) != defaultMaxPlayers {
		return errors.New("yinsh requires two players")
	}
	for _, player := range state.Roster {
		if player == nil || !player.Ready || !player.Online {
			return errors.New("all players must be ready and online")
		}
		player.Ready = player.AI
		player.GameReady = player.AI
	}
	if err := initializeBoard(state); err != nil {
		return err
	}
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
	for _, current := range state.Roster {
		if current == nil || !current.GameReady {
			return nil
		}
	}
	if sentAt.IsZero() {
		sentAt = time.Now().UTC()
	}
	state.GameStartTime = sentAt.UTC()
	return startGame(state)
}

func applyTimeout(state *State, playerID string) error {
	if state.CurrentPlayer != playerID {
		return errors.New("timeout player is no longer active")
	}
	var err error
	switch state.Status {
	case StatusPlacement:
		points := emptyPointIDs(state)
		if len(points) == 0 {
			return errors.New("no placement is available")
		}
		err = placeRing(state, playerID, points[0])
	case StatusMove:
		moves := allLegalMoves(state, playerID)
		fromIDs := make([]string, 0, len(moves))
		for from := range moves {
			fromIDs = append(fromIDs, from)
		}
		sort.Strings(fromIDs)
		if len(fromIDs) == 0 {
			advanceBlockedPlayers(state)
			return nil
		}
		err = moveRing(state, playerID, fromIDs[0], moves[fromIDs[0]][0].To)
	case StatusRemoveRow:
		rows := rowsFor(state, sideForPlayer(state, playerID))
		if len(rows) == 0 {
			return errors.New("no row is available")
		}
		err = removeRow(state, playerID, rows[0].ID)
	case StatusRemoveRing:
		rings := ringIDs(state, sideForPlayer(state, playerID))
		if len(rings) == 0 {
			return errors.New("no ring is available")
		}
		err = removeRing(state, playerID, rings[0])
	default:
		return errors.New("current phase cannot time out")
	}
	if err == nil && state.LastAction != nil {
		if state.LastAction.Payload == nil {
			state.LastAction.Payload = map[string]any{}
		}
		state.LastAction.Payload["timeout"] = true
		if len(state.Log) > 0 {
			state.Log[len(state.Log)-1] = cloneAction(*state.LastAction)
		}
	}
	return err
}

func nextAIRequest(state *State) *game.AIRequest {
	player := state.Roster[state.CurrentPlayer]
	if player == nil || !player.AI || !isActionStatus(state.Status) {
		return nil
	}
	delayRange := int((aiMaxDelay - aiMinDelay) / time.Millisecond)
	delayOffset := int((state.RandomSeed + uint64(len(state.Log))*7919) % uint64(delayRange+1))
	fallback := contracts.RoomCommand{Type: CommandTimeout, PlayerID: player.PlayerID}
	return &game.AIRequest{
		RoomID:          state.RoomID,
		GameType:        GameType,
		PlayerID:        player.PlayerID,
		State:           cloneState(state),
		Delay:           aiMinDelay + time.Duration(delayOffset)*time.Millisecond,
		FallbackCommand: &fallback,
	}
}

func updateTurnDeadline(state *State, commandTime time.Time) {
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	if !isActionStatus(state.Status) {
		return
	}
	player := state.Roster[state.CurrentPlayer]
	if player == nil || player.AI {
		return
	}
	if commandTime.IsZero() {
		commandTime = time.Now().UTC()
	}
	switch state.Status {
	case StatusPlacement:
		state.TurnTimeout = placementTimeout
	case StatusMove:
		state.TurnTimeout = moveTimeout
	case StatusRemoveRow, StatusRemoveRing:
		state.TurnTimeout = resolutionTimeout
	}
	state.TurnDeadline = commandTime.UTC().Add(state.TurnTimeout)
}

func isActionStatus(status string) bool {
	switch status {
	case StatusPlacement, StatusMove, StatusRemoveRow, StatusRemoveRing:
		return true
	default:
		return false
	}
}

func asState(state game.State) (*State, error) {
	switch typed := state.(type) {
	case *State:
		if typed == nil {
			return nil, errors.New("yinsh state is nil")
		}
		ensureState(typed)
		return typed, nil
	case State:
		ensureState(&typed)
		return &typed, nil
	default:
		return nil, fmt.Errorf("unexpected yinsh state %T", state)
	}
}

func ensureState(state *State) {
	if state.MaxPlayers <= 0 {
		state.MaxPlayers = defaultMaxPlayers
	}
	if state.RoundNo <= 0 {
		state.RoundNo = 1
	}
	if state.Visibility == "" {
		state.Visibility = contracts.VisibilityPublic
	}
	if state.Roster == nil {
		state.Roster = map[string]*Player{}
	}
	if state.PlayerOrder == nil {
		state.PlayerOrder = []string{}
	}
	if state.Rings == nil {
		state.Rings = map[string]Side{}
	}
	if state.Markers == nil {
		state.Markers = map[string]Side{}
	}
	if state.PlacedRings == nil {
		state.PlacedRings = map[Side]int{SideWhite: 0, SideBlack: 0}
	}
	if state.RingsRemoved == nil {
		state.RingsRemoved = map[Side]int{SideWhite: 0, SideBlack: 0}
	}
	if state.Log == nil {
		state.Log = []Action{}
	}
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

func firstFreeSeat(state *State) int {
	for index := 0; index < state.MaxPlayers; index++ {
		if !seatOccupied(state, index) {
			return index
		}
	}
	return len(state.Roster)
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
		return state.Roster[state.PlayerOrder[i]].SeatIndex <
			state.Roster[state.PlayerOrder[j]].SeatIndex
	})
}

func removeString(values []string, target string) []string {
	result := values[:0]
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func optionalString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func deadlineValue(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}
