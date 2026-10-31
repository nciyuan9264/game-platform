package loveletter

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
	GameType            = "loveletter"
	EngineVersion       = "loveletter-v3"
	ReplaySchemaVersion = 1
	defaultMaxPlayers   = 6
	maxNoHumanChecks    = 3
)

var aiNames = []string{"艾琳", "奥利弗", "莉娅", "诺亚", "苏菲", "米娅"}

const (
	CommandDrawCard         = "game_draw_card"
	CommandPlayCard         = "game_play_card"
	CommandSettleChancellor = "game_settle_chancellor"
	CommandNextRound        = "game_next_round"
	CommandTimeout          = "turn_timeout"
	CommandRoundTransition  = "round_transition_timeout"
)

const (
	turnTimeout          = 60 * time.Second
	roundTransitionDelay = 15 * time.Second
	aiMinDelay           = 3 * time.Second
	aiMaxDelay           = 5 * time.Second
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
		return nil, errors.New("loveletter requires six room seats")
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
		Deck:               []Card{},
		Burn:               []Card{},
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
	maxFavor := 0
	for _, player := range typed.Roster {
		if player != nil && player.Favors > maxFavor {
			maxFavor = player.Favors
		}
	}
	return game.RoomProgress{Current: maxFavor, Total: typed.Goal}, nil
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
		removedPlayerID, err = removePlayer(typed, command.PlayerID, payload)
	case "room_update_settings":
		err = updateVisibility(typed, command.PlayerID, payload)
	case "match_begin":
		err = beginMatch(typed, command.PlayerID)
	case "game_ready":
		err = gameReady(typed, command.PlayerID, command.SentAt)
	case CommandDrawCard:
		err = drawCard(typed, command.PlayerID, automatic)
	case CommandPlayCard:
		var value struct {
			CardID         string `json:"cardID"`
			TargetPlayerID string `json:"targetPlayerID"`
			GuessRank      *int   `json:"guessRank"`
		}
		err = decodePayload(payload, &value)
		if err == nil && value.CardID == "" {
			err = errors.New("cardID is required")
		}
		if err == nil {
			err = playCard(
				typed,
				command.PlayerID,
				value.CardID,
				value.TargetPlayerID,
				value.GuessRank,
				automatic,
			)
		}
	case CommandSettleChancellor:
		var value struct {
			KeepCardID    string   `json:"keepCardID"`
			BottomCardIDs []string `json:"bottomCardIDs"`
		}
		err = decodePayload(payload, &value)
		if err == nil {
			err = settleChancellor(
				typed,
				command.PlayerID,
				value.KeepCardID,
				value.BottomCardIDs,
				automatic,
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
		err = fmt.Errorf("unknown loveletter command %q", command.Type)
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
		return drawCard(state, playerID, true)
	case CommandPlayCard:
		var value struct {
			CardID         string `json:"cardID"`
			TargetPlayerID string `json:"targetPlayerID"`
			GuessRank      *int   `json:"guessRank"`
		}
		if err := decodePayload(payload, &value); err != nil {
			return err
		}
		return playCard(
			state,
			playerID,
			value.CardID,
			value.TargetPlayerID,
			value.GuessRank,
			true,
		)
	case CommandSettleChancellor:
		var value struct {
			KeepCardID    string   `json:"keepCardID"`
			BottomCardIDs []string `json:"bottomCardIDs"`
		}
		if err := decodePayload(payload, &value); err != nil {
			return err
		}
		return settleChancellor(
			state,
			playerID,
			value.KeepCardID,
			value.BottomCardIDs,
			true,
		)
	default:
		return errors.New("AI returned an unsupported timeout command")
	}
}

func nextAIRequest(state *State) *game.AIRequest {
	player := state.Roster[state.CurrentPlayer]
	if player == nil || !player.AI ||
		(state.Status != StatusDraw &&
			state.Status != StatusPlay &&
			state.Status != StatusChancellor) {
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

func updateDeadlines(state *State, commandTime time.Time) {
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	if commandTime.IsZero() {
		commandTime = time.Now().UTC()
	}
	if state.Status == StatusRoundEnd {
		if state.RoundTransitionDeadline.IsZero() {
			state.RoundTransitionDeadline = commandTime.UTC().
				Add(roundTransitionDelay)
		}
		return
	}
	state.RoundTransitionDeadline = time.Time{}
	if state.Status != StatusDraw &&
		state.Status != StatusPlay &&
		state.Status != StatusChancellor {
		return
	}
	player := state.Roster[state.CurrentPlayer]
	if player == nil || player.AI {
		return
	}
	state.TurnTimeout = turnTimeout
	state.TurnDeadline = commandTime.UTC().Add(turnTimeout)
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
		"roomData":    roomDataView(state, playerID, players),
	}
}

func roomDataView(
	state *State,
	playerID string,
	players map[string]any,
) map[string]any {
	logs := make([]Action, 0, len(state.Log))
	for _, action := range state.Log {
		logs = append(logs, projectAction(action, playerID))
	}
	var lastAction any
	if len(logs) > 0 {
		lastAction = logs[len(logs)-1]
	}
	var roundResult any
	if state.RoundResult != nil {
		roundResult = cloneRoundResult(state.RoundResult)
	}
	return map[string]any{
		"currentPlayer": state.CurrentPlayer,
		"gameStatus":    state.Status,
		"roundNo":       state.RoundNo,
		"goal":          state.Goal,
		"deckCount":     len(state.Deck),
		"reserveCount":  boolInt(state.Reserve != nil),
		"burnCards":     append([]Card(nil), state.Burn...),
		"activityLog":   logs,
		"lastData":      lastAction,
		"roundResult":   roundResult,
		"turnDeadline":  deadlineValue(state.TurnDeadline),
		"turnTimeoutMs": state.TurnTimeout.Milliseconds(),
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
			"favors":    player.Favors,
			"alive":     player.Alive,
			"protected": player.Protected,
			"spyUsed":   player.SpyUsed,
			"handCount": len(player.Hand),
			"discards":  append([]Card(nil), player.Discards...),
		}
	}
	return result
}

func privateDataView(state *State, playerID string) map[string]any {
	player := state.Roster[playerID]
	hand := []Card{}
	effect := any(nil)
	if player != nil {
		hand = append(hand, player.Hand...)
		effect = clonePrivateEffect(player.Private)
	}
	targets := map[string][]string{}
	for _, cardID := range legalCardIDs(state, playerID) {
		card, ok := cardByID(player, cardID)
		if ok {
			targets[cardID] = legalTargets(state, playerID, card.Rank)
		}
	}
	return map[string]any{
		"hand":            hand,
		"legalCardIDs":    legalCardIDs(state, playerID),
		"legalTargets":    targets,
		"guardGuessRanks": []int{0, 2, 3, 4, 5, 6, 7, 8, 9},
		"effect":          effect,
	}
}

func projectAction(action Action, playerID string) Action {
	result := cloneAction(action)
	if result.Private && result.PlayerID != playerID {
		result.Summary = "完成了一项私密操作。"
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
				displayName = aiName(playerID)
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
		UserID:     playerID,
		PlayerID:   playerID,
		Online:     true,
		Ready:      playerID == state.OwnerID,
		SeatIndex:  firstFreeSeat(state),
		Hand:       []Card{},
		Discards:   []Card{},
		KnownCards: map[string]KnownCard{},
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
		Hand:        []Card{},
		Discards:    []Card{},
		KnownCards:  map[string]KnownCard{},
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
	if len(state.PlayerOrder) < 2 || len(state.PlayerOrder) > 6 {
		return errors.New("loveletter requires two to six players")
	}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil || !player.Ready || !player.Online {
			return errors.New("all players must be ready and online")
		}
		player.Ready = player.AI
		player.GameReady = player.AI
		player.Favors = 0
		player.Hand = []Card{}
		player.Discards = []Card{}
		player.Private = nil
		player.KnownCards = map[string]KnownCard{}
	}
	state.Goal = goalFor(len(state.PlayerOrder))
	state.RoundNo = 0
	state.Deck = []Card{}
	state.Reserve = nil
	state.Burn = []Card{}
	state.Log = []Action{}
	state.LastAction = nil
	state.RoundResult = nil
	state.Result = nil
	state.CurrentPlayer = ""
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
	first := state.PlayerOrder[nextRandom(state, len(state.PlayerOrder))]
	return startRound(state, first)
}

func asState(state game.State) (*State, error) {
	switch typed := state.(type) {
	case *State:
		if typed == nil {
			return nil, errors.New("loveletter state is nil")
		}
		ensureState(typed)
		return typed, nil
	case State:
		ensureState(&typed)
		return &typed, nil
	default:
		return nil, fmt.Errorf("unexpected loveletter state %T", state)
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
	if state.Deck == nil {
		state.Deck = []Card{}
	}
	if state.Burn == nil {
		state.Burn = []Card{}
	}
	if state.Log == nil {
		state.Log = []Action{}
	}
	for _, player := range state.Roster {
		if player == nil {
			continue
		}
		if player.Hand == nil {
			player.Hand = []Card{}
		}
		if player.Discards == nil {
			player.Discards = []Card{}
		}
		if player.KnownCards == nil {
			player.KnownCards = map[string]KnownCard{}
		}
	}
}

func validateState(state *State) error {
	if state.RandomSeed == 0 {
		return errors.New("loveletter state is missing random seed")
	}
	if state.MaxPlayers != defaultMaxPlayers {
		return errors.New("loveletter state must expose six seats")
	}
	if state.Visibility != contracts.VisibilityPublic &&
		state.Visibility != contracts.VisibilityPrivate {
		return errors.New("loveletter state has invalid visibility")
	}
	switch state.Status {
	case StatusMatch, StatusWaiting, StatusDraw, StatusPlay,
		StatusChancellor, StatusRoundEnd, StatusEnd:
	default:
		return errors.New("loveletter state has invalid status")
	}
	if len(state.PlayerOrder) > state.MaxPlayers {
		return errors.New("loveletter roster exceeds room capacity")
	}
	seenSeats := map[int]bool{}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil || player.PlayerID != playerID ||
			player.SeatIndex < 0 || player.SeatIndex >= state.MaxPlayers ||
			seenSeats[player.SeatIndex] {
			return errors.New("loveletter roster is inconsistent")
		}
		seenSeats[player.SeatIndex] = true
	}
	if state.Status != StatusMatch && state.Status != StatusWaiting {
		if state.Goal != goalFor(len(state.PlayerOrder)) {
			return errors.New("loveletter goal is inconsistent")
		}
		if err := validateCardConservation(state); err != nil {
			return err
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

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func aiName(playerID string) string {
	if name, ok := game.AIName(playerID, aiNames); ok {
		return name
	}
	return playerID
}
