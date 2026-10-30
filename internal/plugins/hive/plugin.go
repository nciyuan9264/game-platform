package hive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

const (
	GameType            = "hive"
	EngineVersion       = "hive-v2"
	ReplaySchemaVersion = 1
	defaultMaxPlayers   = 2
	maxNoHumanChecks    = 3
)

var aiNames = []string{"行舟"}

const (
	CommandPlacePiece = "game_place_piece"
	CommandMovePiece  = "game_move_piece"
	CommandResign     = "game_resign"
	CommandTimeout    = "turn_timeout"
	CommandForcedPass = "forced_pass"
)

const (
	turnTimeout = 120 * time.Second
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
		return nil, errors.New("hive requires exactly two seats")
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
		Board:       map[string][]string{},
		Turns:       map[Side]int{SideWhite: 0, SideBlack: 0},
		Log:         []Action{},
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

func (p *Plugin) PrepareNextMatch(
	_ context.Context,
	previous game.State,
	next game.State,
) error {
	if _, err := asState(previous); err != nil {
		return err
	}
	_, err := asState(next)
	return err
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
	return game.RoomProgress{Current: len(typed.Log), Total: len(pieces)}, nil
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
	case CommandPlacePiece:
		var value struct {
			PieceID string `json:"pieceID"`
			To      *Hex   `json:"to"`
		}
		err = decodePayload(payload, &value)
		if err == nil && (value.PieceID == "" || value.To == nil) {
			err = errors.New("pieceID and to are required")
		}
		if err == nil {
			player := staged.Roster[command.PlayerID]
			err = placePiece(
				staged,
				command.PlayerID,
				value.PieceID,
				*value.To,
				player != nil && player.AI,
			)
			advanceTurn = err == nil
		}
	case CommandMovePiece:
		var value struct {
			PieceID string `json:"pieceID"`
			To      *Hex   `json:"to"`
		}
		err = decodePayload(payload, &value)
		if err == nil && (value.PieceID == "" || value.To == nil) {
			err = errors.New("pieceID and to are required")
		}
		if err == nil {
			player := staged.Roster[command.PlayerID]
			err = movePiece(
				staged,
				command.PlayerID,
				value.PieceID,
				*value.To,
				player != nil && player.AI,
			)
			advanceTurn = err == nil
		}
	case CommandResign:
		err = resign(staged, command.PlayerID)
	case CommandTimeout:
		if command.ConnectionRef.Valid() {
			err = errors.New("client cannot submit timeout commands")
		} else {
			err = applyTimeout(ctx, staged, command.PlayerID)
			advanceTurn = err == nil
		}
	case CommandForcedPass:
		if command.ConnectionRef.Valid() {
			err = errors.New("client cannot submit forced pass commands")
		} else {
			err = forcePass(staged, command.PlayerID)
			advanceTurn = err == nil
		}
	case "game_restart_game":
		err = errors.New("finished rooms must start a new match")
	default:
		err = fmt.Errorf("unknown hive command %q", command.Type)
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
	if typed.Status != StatusPlaying || typed.CurrentPlayer == "" {
		return nil, nil
	}
	if len(legalActions(typed)) == 0 {
		return []game.ScheduledCommand{{
			Key: fmt.Sprintf(
				"forced-pass:%s:%d",
				typed.CurrentPlayer,
				len(typed.Log),
			),
			ExecuteAt: time.Now().UTC(),
			PlayerID:  typed.CurrentPlayer,
			Type:      CommandForcedPass,
		}}, nil
	}
	player := typed.Roster[typed.CurrentPlayer]
	if player == nil || player.AI || typed.TurnDeadline.IsZero() {
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
		request.PlayerID == typed.CurrentPlayer,
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
	return roomView(
		typed,
		"",
		"",
		seq,
		playerViews(typed, nil),
		false,
	), nil
}

func roomView(
	state *State,
	playerID,
	matchID string,
	seq int64,
	players map[string]any,
	interactive bool,
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
		"roomData":   roomDataView(state, players, interactive),
		"result":     result,
	}
}

func roomDataView(
	state *State,
	players map[string]any,
	interactive bool,
) map[string]any {
	legal := []map[string]any{}
	if interactive && state.Status == StatusPlaying {
		for _, option := range legalActions(state) {
			legal = append(legal, candidateView(option))
		}
	}
	stacks := make([]map[string]any, 0, len(state.Board))
	positions := make([]Hex, 0, len(state.Board))
	for position := range state.Board {
		parsed, ok := parseHex(position)
		if ok {
			positions = append(positions, parsed)
		}
	}
	sort.Slice(positions, func(i, j int) bool {
		if positions[i].Q != positions[j].Q {
			return positions[i].Q < positions[j].Q
		}
		return positions[i].R < positions[j].R
	})
	for _, position := range positions {
		stack := make([]Piece, 0, len(state.Board[key(position)]))
		for _, pieceID := range state.Board[key(position)] {
			stack = append(stack, piecesByID[pieceID])
		}
		stacks = append(stacks, map[string]any{
			"position": position,
			"pieces":   stack,
		})
	}
	var lastAction any
	if state.LastAction != nil {
		lastAction = cloneAction(*state.LastAction)
	}
	return map[string]any{
		"stacks":        stacks,
		"currentPlayer": state.CurrentPlayer,
		"currentSide":   state.CurrentSide,
		"whitePlayerID": state.WhitePlayerID,
		"blackPlayerID": state.BlackPlayerID,
		"firstPlayerID": state.FirstPlayerID,
		"roundNo":       state.RoundNo,
		"gameStatus":    state.Status,
		"legalActions":  legal,
		"moveLog":       cloneActions(state.Log),
		"lastAction":    lastAction,
		"turnDeadline":  deadlineValue(state.TurnDeadline),
		"turnTimeoutMs": timeoutValue(state.TurnTimeout),
		"players":       players,
		"playerOrder":   append([]string(nil), state.PlayerOrder...),
	}
}

func candidateView(option candidate) map[string]any {
	return map[string]any{
		"kind":    option.Kind,
		"pieceID": option.PieceID,
		"to":      option.To,
		"path":    append([]Hex(nil), option.Path...),
	}
}

func resultView(state *State) map[string]any {
	placed := 0
	maxHeight := 0
	for _, stack := range state.Board {
		placed += len(stack)
		if len(stack) > maxHeight {
			maxHeight = len(stack)
		}
	}
	result := map[string]any{
		"draw":             state.Draw,
		"reason":           state.EndReason,
		"turnCount":        len(state.Log),
		"placedPieceCount": placed,
		"maxStackHeight":   maxHeight,
	}
	if state.WinnerID != "" {
		result["winnerID"] = state.WinnerID
		result["winnerSide"] = state.WinnerSide
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
				displayName = "行舟"
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
			"gameRole":    player.Side,
		}
	}
	return result
}

func replayPlayerData(state *State) map[string]any {
	result := map[string]any{}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil ||
			(player.Side != SideWhite && player.Side != SideBlack) {
			continue
		}
		reserveByKind := map[Kind]int{}
		for _, kind := range kindOrder {
			reserveByKind[kind] = 0
		}
		for _, piece := range reserve(state, player.Side) {
			reserveByKind[piece.Kind]++
		}
		_, queenPlaced := queenLocation(state, player.Side)
		result[playerID] = map[string]any{
			"side":          player.Side,
			"turnsTaken":    state.Turns[player.Side],
			"queenPlaced":   queenPlaced,
			"queenPressure": queenPressure(state, player.Side),
			"reserveByKind": reserveByKind,
		}
	}
	return result
}

func connect(state *State, playerID string) error {
	if playerID == "" {
		return errors.New("player ID is required")
	}
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
	if err := decodePayload(payload, &value); err != nil ||
		value.Ready == nil {
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
		return errors.New("hive requires two players")
	}
	for _, player := range state.Roster {
		if player == nil || !player.Ready || !player.Online {
			return errors.New("all players must be ready and online")
		}
		player.Ready = player.AI
		player.GameReady = player.AI
	}
	assignSides(state)
	state.Board = map[string][]string{}
	state.Turns = map[Side]int{SideWhite: 0, SideBlack: 0}
	state.Log = []Action{}
	state.LastAction = nil
	state.ConsecutivePasses = 0
	state.WinnerID = ""
	state.WinnerSide = ""
	state.Draw = false
	state.EndReason = ""
	state.GameStartTime = time.Time{}
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	state.CurrentSide = SideWhite
	state.CurrentPlayer = state.WhitePlayerID
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
	state.CurrentSide = SideWhite
	state.CurrentPlayer = state.WhitePlayerID
	return nil
}

func assignSides(state *State) {
	if len(state.PlayerOrder) != defaultMaxPlayers {
		return
	}
	whiteIndex := (int(state.RandomSeed&1) + state.RoundNo - 1) % 2
	state.WhitePlayerID = state.PlayerOrder[whiteIndex]
	state.BlackPlayerID = state.PlayerOrder[1-whiteIndex]
	state.FirstPlayerID = state.WhitePlayerID
	state.Roster[state.WhitePlayerID].Side = SideWhite
	state.Roster[state.BlackPlayerID].Side = SideBlack
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
	option, ok := bestAction(ctx, state)
	if !ok {
		return forcePass(state, playerID)
	}
	if option.Kind == "place" {
		return placePiece(
			state,
			playerID,
			option.PieceID,
			option.To,
			true,
		)
	}
	return movePiece(
		state,
		playerID,
		option.PieceID,
		option.To,
		true,
	)
}

func nextAIRequest(state *State) *game.AIRequest {
	player := state.Roster[state.CurrentPlayer]
	if player == nil || !player.AI ||
		state.Status != StatusPlaying ||
		len(legalActions(state)) == 0 {
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
	if state.Status != StatusPlaying || len(legalActions(state)) == 0 {
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
	var typed *State
	switch value := state.(type) {
	case *State:
		typed = value
	case State:
		copyValue := value
		typed = &copyValue
	default:
		return nil, fmt.Errorf("unexpected hive state %T", state)
	}
	if typed == nil {
		return nil, errors.New("hive state is nil")
	}
	if err := validateState(typed); err != nil {
		return nil, err
	}
	return typed, nil
}

func validateState(state *State) error {
	if state.MaxPlayers != defaultMaxPlayers {
		return errors.New("hive state must have exactly two seats")
	}
	if state.RandomSeed == 0 || state.RoundNo <= 0 {
		return errors.New("hive state has invalid seed or round")
	}
	if state.Visibility != contracts.VisibilityPublic &&
		state.Visibility != contracts.VisibilityPrivate {
		return errors.New("hive state has invalid visibility")
	}
	switch state.Status {
	case StatusMatch, StatusWaiting, StatusPlaying, StatusEnd:
	default:
		return errors.New("hive state has invalid status")
	}
	if state.Roster == nil || state.PlayerOrder == nil ||
		state.Board == nil || state.Turns == nil ||
		state.Log == nil {
		return errors.New("hive state has nil collections")
	}
	if len(state.Roster) != len(state.PlayerOrder) ||
		len(state.Roster) > defaultMaxPlayers {
		return errors.New("hive roster is inconsistent")
	}
	seenSeats := map[int]bool{}
	seenPlayers := map[string]bool{}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil || player.PlayerID != playerID ||
			player.SeatIndex < 0 || player.SeatIndex >= defaultMaxPlayers ||
			seenSeats[player.SeatIndex] || seenPlayers[playerID] {
			return errors.New("hive roster contains invalid player")
		}
		seenSeats[player.SeatIndex] = true
		seenPlayers[playerID] = true
	}
	if state.Turns[SideWhite] < 0 || state.Turns[SideBlack] < 0 {
		return errors.New("hive turn counters are invalid")
	}
	if state.Status == StatusMatch {
		return validateLobbyState(state)
	}
	return validateGameState(state)
}

func validateLobbyState(state *State) error {
	if len(state.Board) != 0 || len(state.Log) != 0 ||
		state.CurrentPlayer != "" || state.CurrentSide != "" ||
		state.WhitePlayerID != "" || state.BlackPlayerID != "" ||
		state.FirstPlayerID != "" || state.WinnerID != "" ||
		state.WinnerSide != "" || state.Draw {
		return errors.New("hive lobby contains game state")
	}
	for _, player := range state.Roster {
		if player.Side != "" {
			return errors.New("hive lobby contains side assignment")
		}
	}
	return nil
}

func validateGameState(state *State) error {
	if len(state.Roster) != defaultMaxPlayers ||
		state.WhitePlayerID == "" || state.BlackPlayerID == "" ||
		state.WhitePlayerID == state.BlackPlayerID ||
		state.FirstPlayerID != state.WhitePlayerID ||
		state.Roster[state.WhitePlayerID] == nil ||
		state.Roster[state.BlackPlayerID] == nil ||
		state.Roster[state.WhitePlayerID].Side != SideWhite ||
		state.Roster[state.BlackPlayerID].Side != SideBlack {
		return errors.New("hive side assignment is inconsistent")
	}
	seenPieces := map[string]bool{}
	for position, stack := range state.Board {
		if _, ok := parseHex(position); !ok || len(stack) == 0 {
			return errors.New("hive board contains invalid position")
		}
		for level, pieceID := range stack {
			piece, ok := piecesByID[pieceID]
			if !ok || seenPieces[pieceID] ||
				(level > 0 && piece.Kind != KindBeetle) {
				return errors.New("hive board contains invalid piece")
			}
			seenPieces[pieceID] = true
		}
	}
	if len(seenPieces) > len(pieces) || !connected(state.Board) {
		return errors.New("hive board is disconnected or overfull")
	}
	if err := validateActionLog(state); err != nil {
		return err
	}
	switch state.Status {
	case StatusWaiting:
		if state.CurrentPlayer != state.WhitePlayerID ||
			state.CurrentSide != SideWhite ||
			!state.GameStartTime.IsZero() ||
			len(state.Board) != 0 || len(state.Log) != 0 {
			return errors.New("waiting hive state is inconsistent")
		}
	case StatusPlaying:
		if state.CurrentPlayer == "" ||
			state.Roster[state.CurrentPlayer] == nil ||
			state.CurrentSide != state.Roster[state.CurrentPlayer].Side ||
			state.GameStartTime.IsZero() ||
			state.WinnerID != "" || state.WinnerSide != "" ||
			state.Draw || state.EndReason != "" {
			return errors.New("playing hive state is inconsistent")
		}
	case StatusEnd:
		if state.CurrentPlayer != "" || state.CurrentSide != "" ||
			state.GameStartTime.IsZero() || state.EndReason == "" {
			return errors.New("finished hive state is inconsistent")
		}
		if state.Draw {
			if state.WinnerID != "" || state.WinnerSide != "" {
				return errors.New("draw hive state contains a winner")
			}
		} else if state.WinnerID == "" ||
			state.Roster[state.WinnerID] == nil ||
			state.WinnerSide != state.Roster[state.WinnerID].Side {
			return errors.New("finished hive winner is invalid")
		}
	}
	return nil
}

func validateActionLog(state *State) error {
	if len(state.Log) > 0 && state.LastAction == nil {
		return errors.New("hive last action is missing")
	}
	for index, action := range state.Log {
		player := state.Roster[action.PlayerID]
		if action.ID != index+1 || player == nil ||
			action.Side != player.Side || action.Path == nil {
			return errors.New("hive action log is invalid")
		}
		switch action.Kind {
		case "place", "move":
			piece, ok := piecesByID[action.PieceID]
			if !ok || piece.Side != action.Side || action.To == nil {
				return errors.New("hive piece action is invalid")
			}
			if action.Kind == "place" && action.From != nil {
				return errors.New("hive placement has a source")
			}
			if action.Kind == "move" && action.From == nil {
				return errors.New("hive move is missing a source")
			}
		case "pass", "resign":
			if action.PieceID != "" || action.From != nil ||
				action.To != nil || len(action.Path) != 0 {
				return errors.New("hive terminal action is invalid")
			}
		default:
			return errors.New("hive action kind is invalid")
		}
	}
	if len(state.Log) > 0 &&
		!reflect.DeepEqual(*state.LastAction, state.Log[len(state.Log)-1]) {
		return errors.New("hive last action is inconsistent")
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

func cloneActions(values []Action) []Action {
	result := make([]Action, len(values))
	for index, action := range values {
		result[index] = cloneAction(action)
	}
	return result
}

func deadlineValue(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func timeoutValue(value time.Duration) any {
	if value <= 0 {
		return nil
	}
	return value.Milliseconds()
}
