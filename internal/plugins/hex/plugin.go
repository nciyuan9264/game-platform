package hex

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
	GameType            = "hex"
	EngineVersion       = "hex-v2"
	ReplaySchemaVersion = 1
	defaultMaxPlayers   = 2
	maxNoHumanChecks    = 3
)

var aiNames = []string{"青岸"}

const (
	CommandPlaceStone = "game_place_stone"
	CommandSwap       = "game_swap"
	CommandResign     = "game_resign"
	CommandTimeout    = "turn_timeout"
)

const (
	turnTimeout = 60 * time.Second
	aiMinDelay  = 1200 * time.Millisecond
	aiMaxDelay  = 2 * time.Second
	aiTimeout   = 900 * time.Millisecond
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
		return nil, errors.New("hex requires exactly two seats")
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
		RoomID:      cfg.RoomID,
		OwnerID:     cfg.OwnerPlayerID,
		Status:      StatusMatch,
		MaxPlayers:  maxPlayers,
		Visibility:  visibility,
		RandomSeed:  cfg.RandomSeed,
		RoundNo:     roundNo,
		Roster:      map[string]*Player{},
		PlayerOrder: []string{},
		Board:       []Stone{},
		Log:         []Action{},
		WinningPath: []Cell{},
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
			GameRole:    string(player.Side),
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
	return game.RoomProgress{Current: len(typed.Log), Total: 0}, nil
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
	advanceTurn := false
	switch command.Type {
	case "connect":
		err = connect(staged, command.PlayerID)
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
		wasPlaying := staged.Status == StatusPlaying
		err = gameReady(staged, command.PlayerID, command.SentAt)
		advanceTurn = err == nil && !wasPlaying &&
			staged.Status == StatusPlaying
	case CommandPlaceStone:
		var value struct {
			Cell *Cell `json:"cell"`
		}
		err = decodePayload(payload, &value)
		if err == nil && value.Cell == nil {
			err = errors.New("cell is required")
		}
		if err == nil {
			player := staged.Roster[command.PlayerID]
			err = placeStone(
				staged,
				command.PlayerID,
				*value.Cell,
				player != nil && player.AI,
			)
			advanceTurn = err == nil
		}
	case CommandSwap:
		player := staged.Roster[command.PlayerID]
		err = swapSides(
			staged,
			command.PlayerID,
			player != nil && player.AI,
		)
		advanceTurn = err == nil
	case CommandResign:
		err = resign(staged, command.PlayerID)
		advanceTurn = err == nil
	case CommandTimeout:
		if command.ConnectionRef.Valid() {
			err = errors.New("client cannot submit timeout commands")
		} else {
			err = applyTimeout(ctx, staged, command.PlayerID)
			advanceTurn = err == nil
		}
	case "game_restart_game":
		err = errors.New("finished rooms must start a new match")
	default:
		err = fmt.Errorf("unknown hex command %q", command.Type)
	}
	if err != nil {
		return game.ApplyResult{}, err
	}
	if advanceTurn {
		updateTurnDeadline(staged, command.SentAt)
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
	if typed.Status != StatusPlaying ||
		typed.TurnDeadline.IsZero() ||
		typed.CurrentPlayer == "" {
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
		"playerData": replayPlayerData(state),
		"result":     result,
		"roomData": roomDataView(
			state,
			players,
			playerID != "" && playerID == state.CurrentPlayer,
		),
	}
}

func roomDataView(
	state *State,
	players map[string]any,
	interactive bool,
) map[string]any {
	legal := []Cell{}
	if interactive && state.Status == StatusPlaying {
		legal = legalCells(state.Board)
	}
	var lastAction any
	if state.LastAction != nil {
		lastAction = cloneAction(*state.LastAction)
	}
	return map[string]any{
		"boardSize":     boardSize,
		"board":         append([]Stone(nil), state.Board...),
		"currentPlayer": state.CurrentPlayer,
		"currentSide":   sideForPlayer(state, state.CurrentPlayer),
		"redPlayerID":   state.RedPlayerID,
		"tealPlayerID":  state.TealPlayerID,
		"firstPlayerID": state.FirstPlayerID,
		"roundNo":       state.RoundNo,
		"gameStatus":    state.Status,
		"pieRule":       true,
		"swapAvailable": canSwap(state, state.CurrentPlayer),
		"legalMoves":    legal,
		"winningPath":   append([]Cell(nil), state.WinningPath...),
		"moveLog":       append([]Action(nil), state.Log...),
		"lastAction":    lastAction,
		"turnDeadline":  deadlineValue(state.TurnDeadline),
		"turnTimeoutMs": state.TurnTimeout.Milliseconds(),
		"players":       players,
		"playerOrder":   append([]string(nil), state.PlayerOrder...),
	}
}

func resultView(state *State) map[string]any {
	red, teal := stoneCounts(state.Board)
	result := map[string]any{
		"reason":         state.EndReason,
		"turnCount":      len(state.Log),
		"pathLength":     len(state.WinningPath),
		"redStoneCount":  red,
		"tealStoneCount": teal,
	}
	if state.WinnerID != "" {
		result["winnerID"] = state.WinnerID
		result["winnerSide"] = state.WinnerSide
	}
	return result
}

func replayPlayerData(state *State) map[string]any {
	red, teal := stoneCounts(state.Board)
	result := map[string]any{}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil ||
			(player.Side != SideRed && player.Side != SideTeal) {
			continue
		}
		count := red
		if player.Side == SideTeal {
			count = teal
		}
		result[playerID] = map[string]any{
			"side":       player.Side,
			"stoneCount": count,
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
				displayName = "青岸"
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
	request, err := game.DecodeAddAIRequest(payload)
	if err != nil {
		return errors.New("invalid seat")
	}
	if request.SeatIndex != nil {
		seatIndex = *request.SeatIndex
		if seatIndex < 0 ||
			seatIndex >= state.MaxPlayers ||
			seatOccupied(state, seatIndex) {
			return errors.New("seat is unavailable")
		}
	}
	identity, err := game.ResolveAIIdentity(
		request.RestorePlayerID,
		func(playerID string) bool { return state.Roster[playerID] != nil },
		aiNames,
	)
	if err != nil {
		return err
	}
	state.Roster[identity.PlayerID] = &Player{
		PlayerID:    identity.PlayerID,
		DisplayName: identity.DisplayName,
		Online:      true,
		Ready:       true,
		GameReady:   true,
		AI:          true,
		SeatIndex:   seatIndex,
	}
	state.PlayerOrder = append(state.PlayerOrder, identity.PlayerID)
	sortPlayerOrder(state)
	return nil
}

func removePlayer(
	state *State,
	actorID string,
	payload []byte,
) (string, error) {
	if actorID != state.OwnerID || state.Status != StatusMatch {
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
		return errors.New("hex requires two players")
	}
	for _, player := range state.Roster {
		if player == nil || !player.Ready || !player.Online {
			return errors.New("all players must be ready and online")
		}
		player.Ready = player.AI
		player.GameReady = player.AI
	}
	assignSides(state)
	state.Board = freshBoard()
	state.Log = []Action{}
	state.LastAction = nil
	state.WinningPath = []Cell{}
	state.WinnerID = ""
	state.WinnerSide = ""
	state.EndReason = ""
	state.GameStartTime = time.Time{}
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	state.CurrentPlayer = state.RedPlayerID
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
	state.Status = StatusPlaying
	state.CurrentPlayer = state.RedPlayerID
	return nil
}

func assignSides(state *State) {
	if len(state.PlayerOrder) != 2 {
		return
	}
	redIndex := (int(state.RandomSeed&1) + state.RoundNo - 1) % 2
	state.RedPlayerID = state.PlayerOrder[redIndex]
	state.TealPlayerID = state.PlayerOrder[1-redIndex]
	state.FirstPlayerID = state.RedPlayerID
	state.Roster[state.RedPlayerID].Side = SideRed
	state.Roster[state.TealPlayerID].Side = SideTeal
}

func applyTimeout(
	ctx context.Context,
	state *State,
	playerID string,
) error {
	if state.Status != StatusPlaying {
		return errors.New("current phase cannot time out")
	}
	if state.CurrentPlayer != playerID {
		return errors.New("timeout player is no longer active")
	}
	if canSwap(state, playerID) && shouldSwap(state) {
		return swapSides(state, playerID, true)
	}
	cell, ok := bestCell(ctx, state, sideForPlayer(state, playerID))
	if !ok {
		return errors.New("no legal timeout move")
	}
	return placeStone(state, playerID, cell, true)
}

func nextAIRequest(state *State) *game.AIRequest {
	player := state.Roster[state.CurrentPlayer]
	if player == nil ||
		!player.AI ||
		state.Status != StatusPlaying {
		return nil
	}
	delayRange := int((aiMaxDelay - aiMinDelay) / time.Millisecond)
	delayOffset := int(
		(state.RandomSeed + uint64(len(state.Log))*7919) %
			uint64(delayRange+1),
	)
	fallback := contracts.RoomCommand{
		Type:     CommandTimeout,
		PlayerID: player.PlayerID,
	}
	return &game.AIRequest{
		RoomID:          state.RoomID,
		GameType:        GameType,
		PlayerID:        player.PlayerID,
		State:           cloneState(state),
		Delay:           aiMinDelay + time.Duration(delayOffset)*time.Millisecond,
		Timeout:         aiTimeout,
		FallbackCommand: &fallback,
	}
}

func updateTurnDeadline(state *State, commandTime time.Time) {
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	if state.Status != StatusPlaying {
		return
	}
	player := state.Roster[state.CurrentPlayer]
	if player == nil || player.AI {
		return
	}
	if commandTime.IsZero() {
		commandTime = time.Now().UTC()
	}
	state.TurnTimeout = turnTimeout
	state.TurnDeadline = commandTime.UTC().Add(turnTimeout)
}

func asState(state game.State) (*State, error) {
	switch typed := state.(type) {
	case *State:
		if typed == nil {
			return nil, errors.New("hex state is nil")
		}
		ensureState(typed)
		return typed, nil
	case State:
		ensureState(&typed)
		return &typed, nil
	default:
		return nil, fmt.Errorf("unexpected hex state %T", state)
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
	if state.Board == nil {
		state.Board = []Stone{}
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

func deadlineValue(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}
