package onitama

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
	GameType            = "onitama"
	EngineVersion       = "onitama-v1"
	ReplaySchemaVersion = 1
	defaultMaxPlayers   = 2
	maxNoHumanChecks    = 3
)

const (
	CommandMovePiece = "game_move_piece"
	CommandPass      = "game_pass"
	CommandResign    = "game_resign"
	CommandTimeout   = "turn_timeout"
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
		return nil, errors.New("onitama requires exactly two seats")
	}
	if cfg.TurnTimeoutSeconds != 0 &&
		cfg.TurnTimeoutSeconds != int(turnTimeout/time.Second) {
		return nil, errors.New("onitama uses a fixed 60 second turn")
	}
	visibility := cfg.Visibility
	if visibility == "" {
		visibility = contracts.VisibilityPublic
	}
	roundNo := cfg.RoundNo
	if roundNo <= 0 {
		roundNo = 1
	}
	state := &State{
		RoomID:       cfg.RoomID,
		OwnerID:      cfg.OwnerPlayerID,
		Status:       StatusMatch,
		MaxPlayers:   maxPlayers,
		Visibility:   visibility,
		RandomSeed:   cfg.RandomSeed,
		RoundNo:      roundNo,
		Roster:       map[string]*Player{},
		PlayerOrder:  []string{},
		Pieces:       map[string]*Piece{},
		Hands:        map[Side][]string{},
		DrawnCardIDs: []string{},
		Log:          []Action{},
	}
	if err := validateState(state); err != nil {
		return nil, err
	}
	return state, nil
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
	refreshDeadline := false
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
		removedPlayerID, err = removePlayer(
			staged,
			command.PlayerID,
			payload,
		)
	case "room_update_settings":
		err = updateVisibility(staged, command.PlayerID, payload)
	case "match_begin":
		err = beginMatch(staged, command.PlayerID)
	case "game_ready":
		wasPlaying := staged.Status == StatusPlaying
		err = gameReady(staged, command.PlayerID, command.SentAt)
		refreshDeadline = err == nil && !wasPlaying &&
			staged.Status == StatusPlaying
	case CommandMovePiece:
		var value struct {
			CardID  string `json:"cardID"`
			PieceID string `json:"pieceID"`
			To      *Cell  `json:"to"`
		}
		err = decodePayload(payload, &value)
		if err == nil &&
			(value.CardID == "" ||
				value.PieceID == "" ||
				value.To == nil) {
			err = errors.New(
				"cardID, pieceID and to are required",
			)
		}
		if err == nil {
			player := staged.Roster[command.PlayerID]
			err = movePiece(
				staged,
				command.PlayerID,
				value.CardID,
				value.PieceID,
				*value.To,
				player != nil && player.AI,
			)
			refreshDeadline = err == nil
		}
	case CommandPass:
		var value struct {
			CardID string `json:"cardID"`
		}
		err = decodePayload(payload, &value)
		if err == nil && value.CardID == "" {
			err = errors.New("cardID is required")
		}
		if err == nil {
			player := staged.Roster[command.PlayerID]
			err = passTurn(
				staged,
				command.PlayerID,
				value.CardID,
				player != nil && player.AI,
			)
			refreshDeadline = err == nil
		}
	case CommandResign:
		err = resign(staged, command.PlayerID)
		refreshDeadline = err == nil
	case CommandTimeout:
		if command.ConnectionRef.Valid() {
			err = errors.New("client cannot submit timeout commands")
		} else {
			err = applyTimeout(ctx, staged, command.PlayerID)
			refreshDeadline = err == nil
		}
	case "game_restart_game":
		err = errors.New("finished rooms must start a new match")
	default:
		err = fmt.Errorf("unknown onitama command %q", command.Type)
	}
	if err != nil {
		return game.ApplyResult{}, err
	}
	if refreshDeadline {
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
		"roomData": roomDataView(
			state,
			players,
			interactive,
		),
		"result": result,
	}
}

func roomDataView(
	state *State,
	players map[string]any,
	interactive bool,
) map[string]any {
	moves := []LegalMove{}
	canPass := false
	if interactive && state.Status == StatusPlaying {
		side := sideForPlayer(state, state.CurrentPlayer)
		moves = legalMoves(state, side)
		canPass = len(moves) == 0
	}
	var lastAction any
	if state.LastAction != nil {
		lastAction = cloneAction(*state.LastAction)
	}
	return map[string]any{
		"pieces":        piecesView(state),
		"hands":         handsView(state),
		"transitCardID": state.TransitCardID,
		"currentPlayer": state.CurrentPlayer,
		"currentSide":   currentSide(state),
		"bluePlayerID":  state.BluePlayerID,
		"redPlayerID":   state.RedPlayerID,
		"firstPlayerID": state.FirstPlayerID,
		"roundNo":       state.RoundNo,
		"gameStatus":    state.Status,
		"legalMoves":    moves,
		"canPass":       canPass,
		"moveLog":       state.Log,
		"lastAction":    lastAction,
		"turnDeadline":  deadlineValue(state.TurnDeadline),
		"turnTimeoutMs": state.TurnTimeout.Milliseconds(),
		"players":       players,
		"playerOrder":   state.PlayerOrder,
	}
}

func piecesView(state *State) []Piece {
	values := sortedPieces(state.Pieces)
	result := make([]Piece, 0, len(values))
	for _, piece := range values {
		result = append(result, *piece)
	}
	return result
}

func handsView(state *State) map[Side][]string {
	return map[Side][]string{
		SideBlue: append([]string(nil), state.Hands[SideBlue]...),
		SideRed:  append([]string(nil), state.Hands[SideRed]...),
	}
}

func currentSide(state *State) Side {
	if state.CurrentPlayer == "" {
		return ""
	}
	return sideForPlayer(state, state.CurrentPlayer)
}

func resultView(state *State) map[string]any {
	result := map[string]any{
		"reason":           state.EndReason,
		"turnCount":        len(state.Log),
		"winnerPieceCount": pieceCount(state, state.WinnerSide),
	}
	if state.WinnerID != "" {
		result["winnerID"] = state.WinnerID
		result["winnerSide"] = state.WinnerSide
	}
	return result
}

func replayPlayerData(state *State) map[string]any {
	result := map[string]any{}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			continue
		}
		result[playerID] = map[string]any{
			"side":        player.Side,
			"pieceCount":  pieceCount(state, player.Side),
			"masterAlive": masterAlive(state, player.Side),
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
				displayName = "弈心"
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
		return errors.New(
			"only the owner can add AI during preparation",
		)
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
		DisplayName: "弈心",
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
	state.PlayerOrder = removeString(
		state.PlayerOrder,
		value.PlayerID,
	)
	return value.PlayerID, nil
}

func updateVisibility(
	state *State,
	actorID string,
	payload []byte,
) error {
	if actorID != state.OwnerID || state.Status != StatusMatch {
		return errors.New("only the owner can update room settings")
	}
	var value struct {
		Visibility         string `json:"visibility"`
		TurnTimeoutSeconds *int   `json:"turnTimeoutSeconds"`
	}
	if err := decodePayload(payload, &value); err != nil ||
		(value.Visibility != contracts.VisibilityPublic &&
			value.Visibility != contracts.VisibilityPrivate) {
		return errors.New("invalid visibility")
	}
	if value.TurnTimeoutSeconds != nil &&
		*value.TurnTimeoutSeconds != int(turnTimeout/time.Second) {
		return errors.New("onitama uses a fixed 60 second turn")
	}
	state.Visibility = value.Visibility
	return nil
}

func beginMatch(state *State, actorID string) error {
	if actorID != state.OwnerID || state.Status != StatusMatch {
		return errors.New("only the owner can start the match")
	}
	if len(state.Roster) != defaultMaxPlayers {
		return errors.New("onitama requires two players")
	}
	for _, player := range state.Roster {
		if player == nil || !player.Ready || !player.Online {
			return errors.New("all players must be ready and online")
		}
		player.Ready = player.AI
		player.GameReady = player.AI
	}
	assignSides(state)
	drawn := drawCards(state.RandomSeed, state.RoundNo)
	state.DrawnCardIDs = drawn
	state.Hands = map[Side][]string{
		SideBlue: append([]string(nil), drawn[:2]...),
		SideRed:  append([]string(nil), drawn[2:4]...),
	}
	state.TransitCardID = drawn[4]
	state.Pieces = freshPieces()
	state.Log = []Action{}
	state.LastAction = nil
	state.WinnerID = ""
	state.WinnerSide = ""
	state.EndReason = ""
	state.GameStartTime = time.Time{}
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	transit, _ := cardByID(state.TransitCardID)
	state.FirstPlayerID = playerForSide(state, transit.Mark)
	state.CurrentPlayer = state.FirstPlayerID
	state.Status = StatusWaiting
	return nil
}

func gameReady(
	state *State,
	playerID string,
	sentAt time.Time,
) error {
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
	state.CurrentPlayer = state.FirstPlayerID
	return nil
}

func assignSides(state *State) {
	if len(state.PlayerOrder) != defaultMaxPlayers {
		return
	}
	blueIndex := 0
	if state.RoundNo%2 == 0 {
		blueIndex = 1
	}
	state.BluePlayerID = state.PlayerOrder[blueIndex]
	state.RedPlayerID = state.PlayerOrder[1-blueIndex]
	state.Roster[state.BluePlayerID].Side = SideBlue
	state.Roster[state.RedPlayerID].Side = SideRed
}

func applyTimeout(
	ctx context.Context,
	state *State,
	playerID string,
) error {
	if state.Status != StatusPlaying ||
		state.CurrentPlayer != playerID {
		return errors.New("timeout player is no longer active")
	}
	side := sideForPlayer(state, playerID)
	action, ok := bestAction(ctx, state, side)
	if !ok {
		return errors.New("no legal timeout action")
	}
	if action.Kind == "pass" {
		return passTurn(
			state,
			playerID,
			action.CardID,
			true,
		)
	}
	return movePiece(
		state,
		playerID,
		action.CardID,
		action.PieceID,
		action.To,
		true,
	)
}

func nextAIRequest(state *State) *game.AIRequest {
	player := state.Roster[state.CurrentPlayer]
	if player == nil || !player.AI || state.Status != StatusPlaying {
		return nil
	}
	delayRange := int(
		(aiMaxDelay - aiMinDelay) / time.Millisecond,
	)
	delayOffset := int(
		(state.RandomSeed + uint64(len(state.Log))*7919) %
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
	var typed *State
	switch value := state.(type) {
	case *State:
		typed = value
	case State:
		copyState := value
		typed = &copyState
	default:
		return nil, fmt.Errorf("unexpected onitama state %T", state)
	}
	if typed == nil {
		return nil, errors.New("onitama state is nil")
	}
	ensureState(typed)
	if err := validateState(typed); err != nil {
		return nil, err
	}
	return typed, nil
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
	if state.Pieces == nil {
		state.Pieces = map[string]*Piece{}
	}
	if state.Hands == nil {
		state.Hands = map[Side][]string{}
	}
	if state.DrawnCardIDs == nil {
		state.DrawnCardIDs = []string{}
	}
	if state.Log == nil {
		state.Log = []Action{}
	}
}

func validateState(state *State) error {
	if err := validateCardCatalog(); err != nil {
		return err
	}
	if state.MaxPlayers != defaultMaxPlayers ||
		state.RandomSeed == 0 ||
		state.RoundNo <= 0 {
		return errors.New("invalid onitama room configuration")
	}
	if state.Visibility != contracts.VisibilityPublic &&
		state.Visibility != contracts.VisibilityPrivate {
		return errors.New("invalid onitama visibility")
	}
	switch state.Status {
	case StatusMatch, StatusWaiting, StatusPlaying, StatusEnd:
	default:
		return errors.New("invalid onitama status")
	}
	if len(state.Roster) != len(state.PlayerOrder) ||
		len(state.Roster) > defaultMaxPlayers {
		return errors.New("invalid onitama roster")
	}
	seenPlayers := map[string]bool{}
	seenSeats := map[int]bool{}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil ||
			player.PlayerID != playerID ||
			seenPlayers[playerID] ||
			seenSeats[player.SeatIndex] ||
			player.SeatIndex < 0 ||
			player.SeatIndex >= defaultMaxPlayers {
			return errors.New("invalid onitama player")
		}
		seenPlayers[playerID] = true
		seenSeats[player.SeatIndex] = true
	}
	for index, action := range state.Log {
		if action.ID != index+1 ||
			(action.Side != SideBlue && action.Side != SideRed) {
			return errors.New("invalid onitama action log")
		}
	}
	if state.Status == StatusMatch {
		if len(state.Pieces) != 0 ||
			len(state.DrawnCardIDs) != 0 ||
			state.TransitCardID != "" {
			return errors.New("match state contains game data")
		}
		return nil
	}
	if len(state.Roster) != defaultMaxPlayers ||
		state.BluePlayerID == "" ||
		state.RedPlayerID == "" ||
		state.BluePlayerID == state.RedPlayerID ||
		state.Roster[state.BluePlayerID] == nil ||
		state.Roster[state.RedPlayerID] == nil ||
		state.Roster[state.BluePlayerID].Side != SideBlue ||
		state.Roster[state.RedPlayerID].Side != SideRed {
		return errors.New("invalid onitama side assignment")
	}
	if len(state.DrawnCardIDs) != 5 ||
		len(state.Hands[SideBlue]) != 2 ||
		len(state.Hands[SideRed]) != 2 ||
		state.TransitCardID == "" {
		return errors.New("invalid onitama card zones")
	}
	seenCards := map[string]bool{}
	for _, cardID := range append(
		append(
			append([]string{}, state.Hands[SideBlue]...),
			state.Hands[SideRed]...,
		),
		state.TransitCardID,
	) {
		if _, ok := cardsByID[cardID]; !ok || seenCards[cardID] {
			return errors.New("invalid onitama card")
		}
		seenCards[cardID] = true
	}
	drawnCards := map[string]bool{}
	for _, cardID := range state.DrawnCardIDs {
		if !seenCards[cardID] || drawnCards[cardID] {
			return errors.New("drawn cards do not match card zones")
		}
		drawnCards[cardID] = true
	}
	if len(drawnCards) != len(seenCards) {
		return errors.New("drawn cards do not match card zones")
	}
	seenCells := map[Cell]bool{}
	for id, piece := range state.Pieces {
		if piece == nil ||
			piece.ID != id ||
			(piece.Side != SideBlue && piece.Side != SideRed) ||
			(piece.Kind != PieceMaster &&
				piece.Kind != PieceStudent) ||
			!inside(piece.Cell) ||
			seenCells[piece.Cell] {
			return errors.New("invalid onitama piece")
		}
		seenCells[piece.Cell] = true
	}
	if len(state.Pieces) > 10 ||
		pieceCount(state, SideBlue) > 5 ||
		pieceCount(state, SideRed) > 5 {
		return errors.New("invalid onitama piece count")
	}
	if state.Status != StatusEnd &&
		(!masterAlive(state, SideBlue) ||
			!masterAlive(state, SideRed)) {
		return errors.New("active game is missing a master")
	}
	if state.FirstPlayerID == "" ||
		state.Roster[state.FirstPlayerID] == nil {
		return errors.New("invalid onitama first player")
	}
	if state.Status == StatusWaiting {
		if state.CurrentPlayer != state.FirstPlayerID {
			return errors.New("invalid waiting player")
		}
		return nil
	}
	if state.Status == StatusPlaying {
		if state.CurrentPlayer == "" ||
			state.Roster[state.CurrentPlayer] == nil ||
			state.GameStartTime.IsZero() {
			return errors.New("invalid active onitama turn")
		}
		return nil
	}
	if state.CurrentPlayer != "" ||
		state.WinnerID == "" ||
		state.Roster[state.WinnerID] == nil ||
		(state.WinnerSide != SideBlue &&
			state.WinnerSide != SideRed) ||
		(state.EndReason != "stone" &&
			state.EndReason != "stream" &&
			state.EndReason != "resign") {
		return errors.New("invalid onitama result")
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

func deadlineValue(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}
