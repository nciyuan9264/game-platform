package splendor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/splendor/catalog"
	"github.com/nciyuan9264/game-platform/internal/splendor/domain"
	"github.com/nciyuan9264/game-platform/internal/splendor/entities"
	"github.com/nciyuan9264/game-platform/internal/splendor/rules"
)

const (
	GameType          = "splendor"
	EngineVersion     = "splendor-v2"
	defaultMaxPlayers = 4
	maxNoHumanChecks  = 3
)

const ReplaySchemaVersion = 2

const splendorTurnTimeout = 60 * time.Second

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
	visibility := cfg.Visibility
	if visibility == "" {
		visibility = contracts.VisibilityPublic
	}
	return &domain.GameState{
		RoomID:      cfg.RoomID,
		RoomStatus:  domain.RoomStatusMatch,
		OwnerID:     cfg.OwnerPlayerID,
		MaxPlayers:  maxPlayers,
		Visibility:  visibility,
		RandomSeed:  cfg.RandomSeed,
		Roster:      map[string]*domain.Player{},
		NormalCards: map[string]*entities.NormalCard{},
		NobleCards:  map[string]*entities.NobleCard{},
		Gems:        map[string]int{},
		Players:     map[string]*domain.PlayerState{},
		PlayerOrder: []string{},
	}, nil
}

func (p *Plugin) EncodeState(_ context.Context, state game.State) ([]byte, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	return json.Marshal(typed)
}

func (p *Plugin) DecodeState(_ context.Context, raw []byte) (game.State, error) {
	var state domain.GameState
	if err := json.Unmarshal(raw, &state); err != nil {
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
	return game.RoomProgress{Current: maxScore(typed), Total: 15}, nil
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
		typed.RandomCounter = 0
	} else {
		typed.RandomCounter++
	}
	count := int(typed.RandomCounter)
	return game.HumanPresenceCheck{
		ConsecutiveEmpty: count,
		Limit:            maxNoHumanChecks,
		ShouldClose:      count >= maxNoHumanChecks,
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
	cmd := domain.Command{Type: command.Type, PlayerID: command.PlayerID, Payload: payload}
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
		err = removePlayer(typed, command.PlayerID, payload)
	case "room_update_settings":
		err = updateVisibility(typed, command.PlayerID, payload)
	case "match_begin":
		err = beginMatch(typed, command.PlayerID)
	case "game_ready":
		err = gameReady(typed, command.PlayerID, command.SentAt)
	case "game_get_gem", "game_buy_card", "game_preserve_card":
		if actionErr := rules.ValidateAction(domain.NewRoom(typed), cmd); actionErr != nil {
			err = actionErr
			break
		}
		switch command.Type {
		case "game_get_gem":
			rules.HandleGetGemMessage(domain.NewRoom(typed), cmd)
		case "game_buy_card":
			rules.HandleBuyCardMessage(domain.NewRoom(typed), cmd)
		case "game_preserve_card":
			rules.HandleReserveCardMessage(domain.NewRoom(typed), cmd)
		}
	case "turn_timeout":
		if command.ConnectionRef.Valid() {
			err = errors.New("客户端不能提交超时命令")
			break
		}
		rules.HandleTurnTimeoutMessage(domain.NewRoom(typed), cmd)
	case "game_restart_game":
		err = errors.New("终局房间不能原地重开，请开始下一局")
	default:
		err = fmt.Errorf("unknown splendor command %q", command.Type)
	}
	if err != nil {
		return game.ApplyResult{}, err
	}
	rules.RecomputeDerivedState(domain.NewRoom(typed))
	updateTurnDeadline(typed, command.SentAt)
	result := game.ApplyResult{
		Status:   string(typed.RoomStatus),
		Finished: typed.RoomStatus == domain.RoomStatusEnd,
	}
	if ai := nextAIRequest(typed); ai != nil {
		result.AIRequests = append(result.AIRequests, *ai)
	}
	return result, nil
}

func (p *Plugin) PendingCommands(ctx context.Context, state game.State) ([]game.ScheduledCommand, error) {
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
	command, err := p.Decide(ctx, game.AIRequest{
		RoomID:   typed.RoomID,
		GameType: GameType,
		PlayerID: typed.CurrentPlayer,
		State:    typed,
	})
	if err != nil {
		command = contracts.RoomCommand{Type: "turn_timeout", PlayerID: typed.CurrentPlayer}
	}
	return []game.ScheduledCommand{{
		Key:       fmt.Sprintf("%s:%s:%d", typed.RoomStatus, typed.CurrentPlayer, typed.TurnDeadline.UnixNano()),
		ExecuteAt: typed.TurnDeadline,
		PlayerID:  typed.CurrentPlayer,
		Type:      command.Type,
		Payload:   command.Payload,
	}}, nil
}

func (p *Plugin) BuildPlayerView(_ context.Context, state game.State, req game.ViewRequest) (any, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	if typed.Roster[req.PlayerID] == nil {
		return nil, errors.New("player is not in room")
	}
	players := playerViews(typed, req.PlayerProfiles)
	if typed.RoomStatus == domain.RoomStatusMatch {
		return map[string]any{
			"type":        contracts.MessageTypeMatchSync,
			"roomID":      typed.RoomID,
			"matchID":     req.MatchID,
			"ownerID":     typed.OwnerID,
			"status":      typed.RoomStatus,
			"playerID":    req.PlayerID,
			"visibility":  typed.Visibility,
			"maxPlayers":  typed.MaxPlayers,
			"players":     players,
			"playerOrder": typed.PlayerOrder,
		}, nil
	}
	result := rules.RecomputeDerivedState(domain.NewRoom(typed))
	var finalResult any
	if typed.RoomStatus == domain.RoomStatusEnd {
		finalResult = result
	}
	return map[string]any{
		"type":       contracts.MessageTypeRoomSync,
		"roomID":     typed.RoomID,
		"matchID":    req.MatchID,
		"seq":        req.Seq,
		"playerId":   req.PlayerID,
		"ownerID":    typed.OwnerID,
		"visibility": typed.Visibility,
		"playerData": typed.Players,
		"result":     finalResult,
		"roomData": map[string]any{
			"card":          revealedCards(typed),
			"deckCounts":    deckCounts(typed),
			"gems":          typed.Gems,
			"nobles":        revealedNobles(typed),
			"currentPlayer": typed.CurrentPlayer,
			"gameStatus":    typed.RoomStatus,
			"lastData":      typed.LastData,
			"players":       players,
			"playerOrder":   typed.PlayerOrder,
			"turnDeadline":  deadlineValue(typed.TurnDeadline),
			"turnTimeoutMs": typed.TurnTimeout.Milliseconds(),
		},
	}, nil
}

func (p *Plugin) Decide(_ context.Context, request game.AIRequest) (contracts.RoomCommand, error) {
	typed, err := asState(request.State)
	if err != nil {
		return contracts.RoomCommand{}, err
	}
	player := typed.Players[request.PlayerID]
	if player == nil {
		return contracts.RoomCommand{}, errors.New("AI player state is missing")
	}
	cards := revealedCardPointers(typed)
	sort.Slice(cards, func(i, j int) bool {
		if cards[i].Points != cards[j].Points {
			return cards[i].Points > cards[j].Points
		}
		return cards[i].ID < cards[j].ID
	})
	for _, card := range cards {
		if rules.CanBuyCard(player, card) {
			return contracts.RoomCommand{Type: "game_buy_card", PlayerID: request.PlayerID, Payload: card.ID}, nil
		}
	}
	if len(player.ReserveCard) < 3 && len(cards) > 0 {
		returns := map[string]int{}
		if tokenCount(player) >= 10 && typed.Gems["Gold"] > 0 {
			returns["Gold"] = 1
		}
		return contracts.RoomCommand{
			Type:     "game_preserve_card",
			PlayerID: request.PlayerID,
			Payload:  map[string]any{"cardID": cards[0].ID, "returns": returns},
		}, nil
	}
	take := map[string]int{}
	for _, color := range orderedGemColors[:5] {
		if typed.Gems[color] > 0 && len(take) < 3 && tokenCount(player)+len(take) < 10 {
			take[color] = 1
		}
	}
	return contracts.RoomCommand{
		Type:     "game_get_gem",
		PlayerID: request.PlayerID,
		Payload:  map[string]any{"take": take},
	}, nil
}

func asState(state game.State) (*domain.GameState, error) {
	switch typed := state.(type) {
	case *domain.GameState:
		if typed == nil {
			return nil, errors.New("splendor state is nil")
		}
		ensureState(typed)
		return typed, nil
	case domain.GameState:
		ensureState(&typed)
		return &typed, nil
	default:
		return nil, fmt.Errorf("unexpected splendor state %T", state)
	}
}

func ensureState(state *domain.GameState) {
	if state.Roster == nil {
		state.Roster = map[string]*domain.Player{}
	}
	if state.NormalCards == nil {
		state.NormalCards = map[string]*entities.NormalCard{}
	}
	if state.NobleCards == nil {
		state.NobleCards = map[string]*entities.NobleCard{}
	}
	if state.Gems == nil {
		state.Gems = map[string]int{}
	}
	if state.Players == nil {
		state.Players = map[string]*domain.PlayerState{}
	}
	if state.Visibility == "" {
		state.Visibility = contracts.VisibilityPublic
	}
}

func connect(state *domain.GameState, playerID string) error {
	if player := state.Roster[playerID]; player != nil {
		player.Online = true
		return nil
	}
	if state.RoomStatus != domain.RoomStatusMatch {
		return errors.New("game already started")
	}
	if len(state.Roster) >= state.MaxPlayers {
		return errors.New("room is full")
	}
	state.Roster[playerID] = &domain.Player{
		UserID: playerID, PlayerID: playerID, Online: true, Ready: playerID == state.OwnerID,
		SeatIndex: len(state.PlayerOrder),
	}
	state.PlayerOrder = append(state.PlayerOrder, playerID)
	return nil
}

func setReady(state *domain.GameState, playerID string, payload []byte) error {
	if state.RoomStatus != domain.RoomStatusMatch {
		return errors.New("preparation phase has ended")
	}
	player := state.Roster[playerID]
	if player == nil {
		return errors.New("player not found")
	}
	var value struct {
		Ready *bool `json:"ready"`
	}
	if json.Unmarshal(payload, &value) != nil || value.Ready == nil {
		return errors.New("invalid ready payload")
	}
	if playerID == state.OwnerID && !*value.Ready {
		return errors.New("owner is always ready")
	}
	player.Ready = *value.Ready
	return nil
}

func addAI(state *domain.GameState, actorID string, payload []byte) error {
	if actorID != state.OwnerID || state.RoomStatus != domain.RoomStatusMatch {
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
	state.Roster[playerID] = &domain.Player{
		PlayerID: playerID, DisplayName: "AI", AI: true, Ready: true, Online: true, SeatIndex: seatIndex,
	}
	state.PlayerOrder = append(state.PlayerOrder, playerID)
	sortPlayerOrder(state)
	return nil
}

func removePlayer(state *domain.GameState, actorID string, payload []byte) error {
	if actorID != state.OwnerID || state.RoomStatus != domain.RoomStatusMatch {
		return errors.New("only the owner can remove players during preparation")
	}
	var value struct {
		PlayerID string `json:"playerID"`
	}
	if json.Unmarshal(payload, &value) != nil || value.PlayerID == "" || value.PlayerID == state.OwnerID {
		return errors.New("invalid player")
	}
	if state.Roster[value.PlayerID] == nil {
		return errors.New("player not found")
	}
	delete(state.Roster, value.PlayerID)
	state.PlayerOrder = removeString(state.PlayerOrder, value.PlayerID)
	return nil
}

func updateVisibility(state *domain.GameState, actorID string, payload []byte) error {
	if actorID != state.OwnerID || state.RoomStatus != domain.RoomStatusMatch {
		return errors.New("only the owner can update room settings")
	}
	var value struct {
		Visibility string `json:"visibility"`
	}
	if json.Unmarshal(payload, &value) != nil ||
		(value.Visibility != contracts.VisibilityPublic && value.Visibility != contracts.VisibilityPrivate) {
		return errors.New("invalid visibility")
	}
	state.Visibility = value.Visibility
	return nil
}

func beginMatch(state *domain.GameState, actorID string) error {
	if actorID != state.OwnerID || state.RoomStatus != domain.RoomStatusMatch {
		return errors.New("only the owner can start the match")
	}
	if len(state.Roster) < 2 {
		return errors.New("at least two players are required")
	}
	for _, player := range state.Roster {
		if player == nil || !player.Ready || !player.Online {
			return errors.New("all players must be ready and online")
		}
		player.Ready = false
	}
	initBoard(state)
	state.RoomStatus = domain.RoomStatusWaiting
	return nil
}

func gameReady(state *domain.GameState, playerID string, sentAt time.Time) error {
	if state.RoomStatus != domain.RoomStatusWaiting {
		return nil
	}
	player := state.Roster[playerID]
	if player == nil {
		return errors.New("player not found")
	}
	player.Ready = true
	for _, current := range state.Roster {
		if current == nil || !current.Ready {
			return nil
		}
	}
	if sentAt.IsZero() {
		sentAt = time.Now().UTC()
	}
	state.GameStartTime = sentAt.UTC()
	if len(state.PlayerOrder) == 0 {
		return errors.New("room has no players")
	}
	index := int(state.RandomSeed % uint64(len(state.PlayerOrder)))
	state.CurrentPlayer = state.PlayerOrder[index]
	state.FirstPlayer = state.CurrentPlayer
	state.RoomStatus = domain.RoomStatusPlaying
	return nil
}

func initBoard(state *domain.GameState) {
	state.NormalCards = map[string]*entities.NormalCard{}
	state.NobleCards = map[string]*entities.NobleCard{}
	state.Players = map[string]*domain.PlayerState{}
	state.RandomCounter++
	random := rand.New(rand.NewPCG(state.RandomSeed, state.RandomCounter))
	for _, level := range []int{1, 2, 3} {
		cards := append([]entities.NormalCard(nil), catalog.SplendorCards[level]...)
		random.Shuffle(len(cards), func(i, j int) { cards[i], cards[j] = cards[j], cards[i] })
		for index := range cards {
			card := cards[index]
			card.State = entities.CardStateHidden
			if index < 4 {
				card.State = entities.CardStateRevealed
			}
			copy := card
			state.NormalCards[strconv.Itoa(card.ID)] = &copy
		}
	}
	nobles := append([]entities.NobleCard(nil), catalog.NobleTilesList...)
	random.Shuffle(len(nobles), func(i, j int) { nobles[i], nobles[j] = nobles[j], nobles[i] })
	for index := range nobles {
		noble := nobles[index]
		noble.State = entities.CardStateHidden
		if index < state.MaxPlayers+1 {
			noble.State = entities.CardStateRevealed
		}
		copy := noble
		state.NobleCards[noble.ID] = &copy
	}
	gemCount := 7
	if state.MaxPlayers == 2 {
		gemCount = 4
	} else if state.MaxPlayers == 3 {
		gemCount = 5
	}
	state.Gems = map[string]int{
		"White": gemCount, "Blue": gemCount, "Green": gemCount,
		"Red": gemCount, "Black": gemCount, "Gold": 5,
	}
	for _, playerID := range state.PlayerOrder {
		state.Players[playerID] = &domain.PlayerState{Gem: map[string]int{}}
	}
	state.CurrentPlayer = ""
	state.FirstPlayer = ""
	state.LastData = nil
}

func nextAIRequest(state *domain.GameState) *game.AIRequest {
	player := state.Roster[state.CurrentPlayer]
	if player == nil || !player.AI ||
		(state.RoomStatus != domain.RoomStatusPlaying && state.RoomStatus != domain.RoomStatusLastTurn) {
		return nil
	}
	return &game.AIRequest{
		RoomID:   state.RoomID,
		GameType: GameType,
		PlayerID: player.PlayerID,
		State:    state,
		FallbackCommand: &contracts.RoomCommand{
			Type:     "turn_timeout",
			PlayerID: player.PlayerID,
		},
	}
}

func updateTurnDeadline(state *domain.GameState, commandTime time.Time) {
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	if state.RoomStatus != domain.RoomStatusPlaying &&
		state.RoomStatus != domain.RoomStatusLastTurn {
		return
	}
	player := state.Roster[state.CurrentPlayer]
	if player == nil || player.AI {
		return
	}
	if commandTime.IsZero() {
		commandTime = time.Now().UTC()
	}
	state.TurnTimeout = splendorTurnTimeout
	state.TurnDeadline = commandTime.UTC().Add(splendorTurnTimeout)
}

func deadlineValue(deadline time.Time) any {
	if deadline.IsZero() {
		return nil
	}
	return deadline.UTC().Format(time.RFC3339Nano)
}

func firstFreeSeat(state *domain.GameState) int {
	for index := 0; index < state.MaxPlayers; index++ {
		if !seatOccupied(state, index) {
			return index
		}
	}
	return len(state.Roster)
}

func seatOccupied(state *domain.GameState, index int) bool {
	for _, player := range state.Roster {
		if player != nil && player.SeatIndex == index {
			return true
		}
	}
	return false
}

func sortPlayerOrder(state *domain.GameState) {
	sort.SliceStable(state.PlayerOrder, func(i, j int) bool {
		return state.Roster[state.PlayerOrder[i]].SeatIndex < state.Roster[state.PlayerOrder[j]].SeatIndex
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

var orderedGemColors = []string{"White", "Blue", "Green", "Red", "Black", "Gold"}

func tokenCount(player *domain.PlayerState) int {
	total := 0
	for _, count := range player.Gem {
		total += count
	}
	return total
}

func maxScore(state *domain.GameState) int {
	max := 0
	for _, player := range state.Players {
		if player != nil && player.Score > max {
			max = player.Score
		}
	}
	return max
}

func playerViews(state *domain.GameState, profiles map[string]game.PlayerProfile) map[string]any {
	result := make(map[string]any, len(state.Roster))
	for playerID, player := range state.Roster {
		if player == nil {
			continue
		}
		userID := player.UserID
		displayName := displayName(player)
		avatar := player.Avatar
		if profile, ok := profiles[playerID]; ok {
			if profile.UserID != "" {
				userID = profile.UserID
			}
			if profile.DisplayName != "" {
				displayName = profile.DisplayName
			}
			if profile.Avatar != "" {
				avatar = profile.Avatar
			}
		}
		if userID == "" {
			userID = playerID
		}
		result[playerID] = map[string]any{
			"userID": userID, "playerID": playerID, "displayName": displayName,
			"avatar": avatar, "seatIndex": player.SeatIndex, "online": player.Online,
			"ai": player.AI, "ready": player.Ready,
		}
	}
	return result
}

func displayName(player *domain.Player) string {
	if player.DisplayName != "" {
		return player.DisplayName
	}
	return player.PlayerID
}

func revealedCardPointers(state *domain.GameState) []*entities.NormalCard {
	result := []*entities.NormalCard{}
	for _, card := range state.NormalCards {
		if card != nil && card.State == entities.CardStateRevealed {
			result = append(result, card)
		}
	}
	return result
}

func revealedCards(state *domain.GameState) map[string][]entities.NormalCard {
	result := map[string][]entities.NormalCard{}
	for _, card := range state.NormalCards {
		if card == nil || card.State != entities.CardStateRevealed {
			continue
		}
		key := strconv.Itoa(card.Level)
		result[key] = append(result[key], *card)
	}
	for key := range result {
		sort.Slice(result[key], func(i, j int) bool { return result[key][i].ID < result[key][j].ID })
	}
	return result
}

func revealedNobles(state *domain.GameState) []entities.NobleCard {
	result := []entities.NobleCard{}
	for _, noble := range state.NobleCards {
		if noble != nil && noble.State == entities.CardStateRevealed {
			result = append(result, *noble)
		}
	}
	sort.Slice(result, func(i, j int) bool { return nobleIDLess(result[i].ID, result[j].ID) })
	return result
}

func nobleIDLess(left, right string) bool {
	leftNumber, leftErr := strconv.Atoi(strings.TrimPrefix(left, "N"))
	rightNumber, rightErr := strconv.Atoi(strings.TrimPrefix(right, "N"))
	if leftErr == nil && rightErr == nil && leftNumber != rightNumber {
		return leftNumber < rightNumber
	}
	return left < right
}

func deckCounts(state *domain.GameState) map[string]int {
	result := map[string]int{"1": 0, "2": 0, "3": 0}
	for _, card := range state.NormalCards {
		if card != nil && card.State == entities.CardStateHidden {
			result[strconv.Itoa(card.Level)]++
		}
	}
	return result
}
