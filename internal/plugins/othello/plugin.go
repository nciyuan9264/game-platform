package othello

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

const (
	GameType              = "othello"
	EngineVersion         = "othello-v13"
	ReplaySchemaVersion   = 1
	defaultMaxPlayers     = 2
	maxNoHumanChecks      = 3
	defaultHintLimit      = 3
	defaultAdminHintLimit = 50
	maxHintLimit          = 100
	hintLimitEnv          = "OTHELLO_HINT_LIMIT"
	adminHintLimitEnv     = "OTHELLO_ADMIN_HINT_LIMIT"
)

var (
	aiNames        = []string{"墨白"}
	hintLimit      = configuredHintLimit()
	adminHintLimit = configuredAdminHintLimit()
)

const (
	CommandPlaceDisc   = "game_place_disc"
	CommandPass        = "game_pass"
	CommandResign      = "game_resign"
	CommandRequestHint = "game_request_hint"
	CommandTimeout     = "turn_timeout"
)

const (
	turnTimeout = 60 * time.Second
	aiMinDelay  = 1200 * time.Millisecond
	aiMaxDelay  = 2 * time.Second
	hardAIDelay = 100 * time.Millisecond
)

type Plugin struct {
	analyzer positionAnalyzer
}

func Definition() game.Definition {
	plugin := &Plugin{analyzer: configuredPositionAnalyzer()}
	return game.Definition{
		GameType:          GameType,
		Version:           EngineVersion,
		DefaultMaxPlayers: defaultMaxPlayers,
		Engine:            plugin,
		AI:                plugin,
		Background:        plugin,
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
		return nil, errors.New("othello requires exactly two seats")
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
		Board:       []Disc{},
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
	ensureState(&state)
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
	return game.RoomMetadata{Visibility: typed.Visibility}, nil
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
			UserID:       player.UserID,
			PlayerID:     player.PlayerID,
			DisplayName:  displayName,
			Online:       player.Online,
			IsAI:         player.AI,
			AIDifficulty: string(player.AIDifficulty),
			SeatIndex:    player.SeatIndex,
			Ready:        player.Ready,
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
	_, _, empty := counts(typed.Board)
	current := 0
	if len(typed.Board) == 64 {
		current = 60 - empty
		if current < 0 {
			current = 0
		}
	}
	return game.RoomProgress{Current: current, Total: 60}, nil
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
	previousPositionKey := positionKey(typed)
	payload, err := json.Marshal(command.Payload)
	if err != nil {
		return game.ApplyResult{}, err
	}
	removedPlayerID := ""
	advanceTurn := false
	switch command.Type {
	case "connect":
		err = connect(typed, command.PlayerID, command.Roles)
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
		wasActive := typed.Status == StatusPlaying ||
			typed.Status == StatusPass
		err = gameReady(typed, command.PlayerID, command.SentAt)
		advanceTurn = err == nil &&
			!wasActive &&
			(typed.Status == StatusPlaying ||
				typed.Status == StatusPass)
	case CommandPlaceDisc:
		var value struct {
			Index *int `json:"index"`
		}
		err = decodePayload(payload, &value)
		if err == nil && value.Index == nil {
			err = errors.New("index is required")
		}
		if err == nil {
			player := typed.Roster[command.PlayerID]
			err = playMove(
				typed,
				command.PlayerID,
				*value.Index,
				player != nil && player.AI,
			)
			advanceTurn = err == nil
		}
	case CommandPass:
		player := typed.Roster[command.PlayerID]
		err = passTurn(
			typed,
			command.PlayerID,
			player != nil && player.AI,
		)
		advanceTurn = err == nil
	case CommandResign:
		err = resign(typed, command.PlayerID)
		advanceTurn = err == nil
	case CommandRequestHint:
		err = requestHint(typed, command.PlayerID)
	case CommandTimeout:
		if command.ConnectionRef.Valid() {
			err = errors.New("client cannot submit timeout commands")
		} else {
			err = applyTimeout(ctx, typed, command.PlayerID)
			advanceTurn = err == nil
		}
	case CommandAnalysisResult:
		if command.ConnectionRef.Valid() {
			err = errors.New("client cannot submit analysis results")
			break
		}
		var value analysisResultPayload
		if err = decodePayload(payload, &value); err == nil {
			err = applyAnalysisResult(typed, value)
		}
	case CommandAnalysisUnavailable:
		if command.ConnectionRef.Valid() {
			err = errors.New("client cannot submit analysis results")
			break
		}
		var value analysisUnavailablePayload
		if err = decodePayload(payload, &value); err == nil {
			err = applyAnalysisUnavailable(typed, value)
		}
	case "game_restart_game":
		err = errors.New("finished rooms must start a new match")
	default:
		err = fmt.Errorf("unknown othello command %q", command.Type)
	}
	if err != nil {
		return game.ApplyResult{}, err
	}
	currentPositionKey := positionKey(typed)
	positionChanged := currentPositionKey != previousPositionKey
	if positionChanged {
		clearHints(typed)
	}
	if advanceTurn {
		updateTurnDeadline(typed, command.SentAt)
	}
	result := game.ApplyResult{
		Status:   typed.Status,
		Finished: typed.Status == StatusEnd,
	}
	if advanceTurn && typed.Status == StatusEnd {
		setFinalAnalysis(typed)
	} else if positionChanged && currentPositionKey != "" {
		result.BackgroundRequests = append(
			result.BackgroundRequests,
			prepareAnalysis(typed),
		)
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
	playerData := replayPlayerData(state)
	legal := []Move{}
	if state.Status == StatusPlaying || state.Status == StatusPass {
		legal = legalMoves(
			state.Board,
			sideForPlayer(state, state.CurrentPlayer),
		)
	}
	var lastAction any
	if state.LastAction != nil {
		lastAction = cloneAction(*state.LastAction)
	}
	var result any
	if state.Status == StatusEnd {
		result = resultView(state)
	}
	roomData := map[string]any{
		"board":         boardView(state.Board),
		"currentPlayer": state.CurrentPlayer,
		"currentSide":   currentSide(state),
		"blackPlayerID": state.BlackPlayerID,
		"whitePlayerID": state.WhitePlayerID,
		"roundNo":       state.RoundNo,
		"gameStatus":    state.Status,
		"legalMoves":    legal,
		"moveLog":       state.Log,
		"lastData":      lastAction,
		"turnDeadline":  deadlineValue(state.TurnDeadline),
		"turnTimeoutMs": state.TurnTimeout.Milliseconds(),
		"players":       players,
		"playerOrder":   state.PlayerOrder,
	}
	if analysis := analysisView(state.Analysis); analysis != nil {
		roomData["analysis"] = analysis
	}
	view := map[string]any{
		"type":       contracts.MessageTypeRoomSync,
		"roomID":     state.RoomID,
		"matchID":    matchID,
		"seq":        seq,
		"playerId":   playerID,
		"ownerID":    state.OwnerID,
		"visibility": state.Visibility,
		"playerData": playerData,
		"result":     result,
		"roomData":   roomData,
	}
	if hint := hintView(state, playerID); hint != nil {
		view["hint"] = hint
	}
	return view
}

func boardView(board []Disc) []string {
	result := make([]string, len(board))
	for index, disc := range board {
		result[index] = string(disc)
	}
	return result
}

func currentSide(state *State) Side {
	if state.CurrentPlayer == "" {
		return ""
	}
	return sideForPlayer(state, state.CurrentPlayer)
}

func resultView(state *State) map[string]any {
	black, white, _ := counts(state.Board)
	result := map[string]any{
		"blackCount": black,
		"whiteCount": white,
		"draw":       state.Status == StatusEnd && state.WinnerID == "",
		"reason":     state.EndReason,
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
				displayName = "墨白"
			} else {
				displayName = fmt.Sprintf("玩家 %s", playerID)
			}
		}
		view := map[string]any{
			"userID":      userID,
			"playerID":    playerID,
			"displayName": displayName,
			"avatar":      avatar,
			"online":      player.Online,
			"ai":          player.AI,
			"ready":       player.Ready,
			"seatIndex":   player.SeatIndex,
		}
		if player.AI {
			view["aiDifficulty"] = string(player.AIDifficulty)
		}
		result[playerID] = view
	}
	return result
}

func connect(state *State, playerID string, roles []string) error {
	if player := state.Roster[playerID]; player != nil {
		player.Online = true
		player.Admin = hasAdminRole(roles)
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
		Admin:     hasAdminRole(roles),
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
	difficulty, err := normalizeAIDifficulty(request.AIDifficulty)
	if err != nil {
		return err
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
		PlayerID:     identity.PlayerID,
		DisplayName:  identity.DisplayName,
		Online:       true,
		Ready:        true,
		GameReady:    true,
		AI:           true,
		AIDifficulty: difficulty,
		SeatIndex:    seatIndex,
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
		return errors.New("othello requires two players")
	}
	for _, player := range state.Roster {
		if player == nil || !player.Ready || !player.Online {
			return errors.New("all players must be ready and online")
		}
		player.Ready = player.AI
		player.GameReady = player.AI
		player.HintsUsed = 0
		player.HintPending = false
		player.HintPositionKey = ""
		player.Hint = nil
	}
	assignSides(state)
	state.Board = freshBoard()
	state.Log = []Action{}
	state.LastAction = nil
	state.WinnerID = ""
	state.WinnerSide = ""
	state.EndReason = ""
	state.Analysis = nil
	state.CurrentPlayer = state.BlackPlayerID
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
	state.CurrentPlayer = state.BlackPlayerID
	return nil
}

func assignSides(state *State) {
	if len(state.PlayerOrder) != 2 {
		return
	}
	blackIndex := 0
	if state.RoundNo%2 == 0 {
		blackIndex = 1
	}
	state.BlackPlayerID = state.PlayerOrder[blackIndex]
	state.WhitePlayerID = state.PlayerOrder[1-blackIndex]
	state.Roster[state.BlackPlayerID].Side = SideBlack
	state.Roster[state.WhitePlayerID].Side = SideWhite
}

func applyTimeout(
	ctx context.Context,
	state *State,
	playerID string,
) error {
	if state.CurrentPlayer != playerID {
		return errors.New("timeout player is no longer active")
	}
	switch state.Status {
	case StatusPass:
		return passTurn(state, playerID, true)
	case StatusPlaying:
		move, ok := bestMediumMove(
			ctx,
			state,
			sideForPlayer(state, playerID),
		)
		if !ok {
			return errors.New("no legal timeout move")
		}
		return playMove(state, playerID, move.Index, true)
	default:
		return errors.New("current phase cannot time out")
	}
}

func nextAIRequest(state *State) *game.AIRequest {
	player := state.Roster[state.CurrentPlayer]
	if player == nil ||
		!player.AI ||
		(state.Status != StatusPlaying && state.Status != StatusPass) {
		return nil
	}
	delay := hardAIDelay
	if player.AIDifficulty == game.AIDifficultyHard &&
		state.Status == StatusPlaying {
		if state.Analysis == nil ||
			state.Analysis.PositionKey != positionKey(state) ||
			state.Analysis.Status == analysisStatusPending {
			return nil
		}
	}
	if player.AIDifficulty != game.AIDifficultyHard {
		delayRange := int((aiMaxDelay - aiMinDelay) / time.Millisecond)
		delayOffset := int(
			(state.RandomSeed + uint64(len(state.Log))*7919) %
				uint64(delayRange+1),
		)
		delay = aiMinDelay + time.Duration(delayOffset)*time.Millisecond
	}
	fallback := contracts.RoomCommand{
		Type:     CommandTimeout,
		PlayerID: player.PlayerID,
	}
	return &game.AIRequest{
		RoomID:          state.RoomID,
		GameType:        GameType,
		PlayerID:        player.PlayerID,
		State:           cloneState(state),
		Delay:           delay,
		FallbackCommand: &fallback,
	}
}

func updateTurnDeadline(state *State, commandTime time.Time) {
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	if state.Status != StatusPlaying && state.Status != StatusPass {
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
			return nil, errors.New("othello state is nil")
		}
		ensureState(typed)
		return typed, nil
	case State:
		ensureState(&typed)
		return &typed, nil
	default:
		return nil, fmt.Errorf("unexpected othello state %T", state)
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
		state.Board = []Disc{}
	}
	if state.Log == nil {
		state.Log = []Action{}
	}
	for _, player := range state.Roster {
		if player == nil {
			continue
		}
		if player.AI {
			difficulty, err := normalizeAIDifficulty(
				string(player.AIDifficulty),
			)
			if err != nil {
				difficulty = game.AIDifficultyMedium
			}
			player.AIDifficulty = difficulty
		} else {
			player.AIDifficulty = ""
		}
	}
}

func normalizeAIDifficulty(value string) (game.AIDifficulty, error) {
	switch game.AIDifficulty(value) {
	case "":
		return game.AIDifficultyMedium, nil
	case game.AIDifficultyEasy,
		game.AIDifficultyMedium,
		game.AIDifficultyHard:
		return game.AIDifficulty(value), nil
	default:
		return "", errors.New("invalid AI difficulty")
	}
}

func configuredHintLimit() int {
	return configuredHintLimitFromEnv(hintLimitEnv, defaultHintLimit)
}

func configuredAdminHintLimit() int {
	return configuredHintLimitFromEnv(
		adminHintLimitEnv,
		defaultAdminHintLimit,
	)
}

func configuredHintLimitFromEnv(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 0 || limit > maxHintLimit {
		panic(fmt.Sprintf(
			"%s must be an integer between 0 and %d",
			name,
			maxHintLimit,
		))
	}
	return limit
}

func hasAdminRole(roles []string) bool {
	for _, role := range roles {
		if strings.EqualFold(strings.TrimSpace(role), "admin") {
			return true
		}
	}
	return false
}

func hintLimitForPlayer(player *Player) int {
	if player != nil && player.Admin {
		return adminHintLimit
	}
	return hintLimit
}

func requestHint(state *State, playerID string) error {
	if state.Status != StatusPlaying || state.CurrentPlayer != playerID {
		return errors.New("hint is only available to the active player")
	}
	player := state.Roster[playerID]
	if player == nil || player.AI {
		return errors.New("hint is only available to human players")
	}
	if player.HintsUsed >= hintLimitForPlayer(player) {
		return errors.New("no hint attempts remaining")
	}
	if player.HintPending {
		return errors.New("hint analysis is already pending")
	}
	key := positionKey(state)
	if key == "" ||
		state.Analysis == nil ||
		state.Analysis.PositionKey != key {
		return errors.New("hint analysis is unavailable")
	}
	if player.Hint != nil && player.Hint.PositionKey == key {
		return errors.New("hint is already available for the current position")
	}
	player.Hint = nil
	switch state.Analysis.Status {
	case analysisStatusReady:
		if !completeHint(state, player, key, state.Analysis.BestMove) {
			return errors.New("hint analysis has no legal move")
		}
	case analysisStatusPending:
		player.HintPending = true
		player.HintPositionKey = key
	default:
		return errors.New("hint analysis is unavailable")
	}
	return nil
}

func completeHint(
	state *State,
	player *Player,
	key string,
	bestMove *int,
) bool {
	if state == nil ||
		player == nil ||
		player.AI ||
		state.Status != StatusPlaying ||
		state.CurrentPlayer != player.PlayerID ||
		player.HintsUsed >= hintLimitForPlayer(player) ||
		key == "" ||
		key != positionKey(state) ||
		bestMove == nil {
		return false
	}
	move := moveAt(state.Board, *bestMove, player.Side)
	if len(move.Flips) == 0 {
		return false
	}
	player.Hint = &HintAdvice{PositionKey: key, Index: *bestMove}
	player.HintsUsed++
	player.HintPending = false
	player.HintPositionKey = ""
	return true
}

func resolvePendingHints(state *State) {
	if state == nil || state.Analysis == nil {
		return
	}
	key := state.Analysis.PositionKey
	for _, player := range state.Roster {
		if player == nil ||
			!player.HintPending ||
			player.HintPositionKey != key {
			continue
		}
		if !completeHint(state, player, key, state.Analysis.BestMove) {
			player.HintPending = false
			player.HintPositionKey = ""
		}
	}
}

func clearPendingHints(state *State, key string) {
	if state == nil {
		return
	}
	for _, player := range state.Roster {
		if player == nil ||
			!player.HintPending ||
			player.HintPositionKey != key {
			continue
		}
		player.HintPending = false
		player.HintPositionKey = ""
		player.Hint = nil
	}
}

func clearHints(state *State) {
	if state == nil {
		return
	}
	for _, player := range state.Roster {
		if player == nil {
			continue
		}
		player.HintPending = false
		player.HintPositionKey = ""
		player.Hint = nil
	}
}

func hintView(state *State, playerID string) map[string]any {
	player := state.Roster[playerID]
	if player == nil || player.AI {
		return nil
	}
	remaining := hintLimitForPlayer(player) - player.HintsUsed
	if remaining < 0 {
		remaining = 0
	}
	key := positionKey(state)
	result := map[string]any{
		"remaining": remaining,
		"pending": player.HintPending &&
			player.HintPositionKey == key,
	}
	if player.Hint != nil && player.Hint.PositionKey == key {
		result["index"] = player.Hint.Index
	}
	return result
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
