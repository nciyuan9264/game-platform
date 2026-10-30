package forsale

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
	GameType            = "forsale"
	EngineVersion       = "forsale-v3"
	ReplaySchemaVersion = 1
	defaultMaxPlayers   = 6
	maxNoHumanChecks    = 3
)

const (
	CommandBid          = "game_bid"
	CommandPass         = "game_pass"
	CommandLockSale     = "game_lock_sale"
	CommandRevealSale   = "game_reveal_sale"
	CommandNextRound    = "game_next_round"
	CommandStartSelling = "game_start_selling"
	CommandTurnTimeout  = "turn_timeout"
	CommandPhaseTimeout = "phase_timeout"
)

const (
	playerActionTimeout = 60 * time.Second
	phaseActionTimeout  = 8 * time.Second
	aiMinDelay          = 1200 * time.Millisecond
	aiMaxDelay          = 2 * time.Second
	aiDecisionTimeout   = 750 * time.Millisecond
)

var aiNames = []string{"小满", "阿橙", "松子", "米拉", "可可"}

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
		return nil, errors.New("for sale rooms require six seats")
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
		RoomID:            cfg.RoomID,
		OwnerID:           cfg.OwnerPlayerID,
		Status:            StatusMatch,
		MaxPlayers:        maximum,
		Visibility:        visibility,
		RandomSeed:        cfg.RandomSeed,
		MatchRoundNo:      roundNo,
		Roster:            map[string]*Player{},
		PlayerOrder:       []string{},
		PropertyDeck:      []int{},
		ChequeDeck:        []int{},
		RemovedProperties: []int{},
		RemovedCheques:    []int{},
		PropertyMarket:    []int{},
		ChequeMarket:      []int{},
		Purchases:         []RoundRecord{},
		Sales:             []RoundRecord{},
		ActionLog:         []Action{},
		Result:            map[string]PlayerResult{},
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
	ensureCollections(&state)
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
		TurnTimeoutSeconds: int(playerActionTimeout / time.Second),
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
	total := typed.TotalRounds * 2
	return game.RoomProgress{
		Current: len(typed.Purchases) + len(typed.Sales),
		Total:   total,
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
		err = setReady(staged, command.PlayerID, command.UserID, payload)
	case "match_add_ai":
		err = addAI(staged, command.PlayerID, command.UserID, payload)
	case "match_remove_player":
		removedPlayerID, err = removePlayer(staged, command.PlayerID, command.UserID, payload)
	case "room_update_settings":
		err = updateVisibility(staged, command.PlayerID, command.UserID, payload)
	case "match_begin":
		err = beginMatch(staged, command.PlayerID, command.UserID)
	case "game_ready":
		err = gameReady(staged, command.PlayerID, command.SentAt)
		refreshAutomation = err == nil
	case CommandBid:
		var value struct {
			Amount int `json:"amount"`
		}
		err = decodePayload(payload, &value)
		if err == nil {
			err = applyBid(staged, command.PlayerID, value.Amount, source)
			refreshAutomation = err == nil
		}
	case CommandPass:
		err = applyPass(staged, command.PlayerID, source)
		refreshAutomation = err == nil
	case CommandLockSale:
		var value struct {
			Property int `json:"property"`
		}
		err = decodePayload(payload, &value)
		if err == nil {
			err = lockSale(staged, command.PlayerID, value.Property, source)
			refreshAutomation = err == nil
		}
	case CommandRevealSale:
		err = requireParticipant(staged, command.PlayerID)
		if err == nil {
			err = revealSale(staged, source)
			refreshAutomation = err == nil
		}
	case CommandNextRound:
		err = requireParticipant(staged, command.PlayerID)
		if err == nil {
			err = advanceRound(staged, source)
			refreshAutomation = err == nil
		}
	case CommandStartSelling:
		err = requireParticipant(staged, command.PlayerID)
		if err == nil {
			appendAction(staged, Action{
				Round: staged.Round, Phase: StatusBridge, Kind: "startSelling",
				PlayerID: command.PlayerID, Source: source,
			})
			err = startSelling(staged)
			refreshAutomation = err == nil
		}
	case CommandTurnTimeout:
		if command.ConnectionRef.Valid() {
			err = errors.New("client cannot submit timeout commands")
		} else {
			err = p.applyTurnTimeout(ctx, staged, command.PlayerID)
			refreshAutomation = err == nil
		}
	case CommandPhaseTimeout:
		if command.ConnectionRef.Valid() {
			err = errors.New("client cannot submit phase timeout commands")
		} else {
			err = applyPhaseTimeout(staged)
			refreshAutomation = err == nil
		}
	case "game_restart_game":
		err = errors.New("finished rooms must start a new match")
	default:
		err = fmt.Errorf("unknown for sale command %q", command.Type)
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
	result.AIRequests = append(result.AIRequests, nextAIRequests(typed)...)
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
	result := []game.ScheduledCommand{}
	switch typed.Status {
	case StatusAuction:
		player := typed.Roster[typed.CurrentPlayer]
		if player != nil && !player.AI && !typed.TurnDeadline.IsZero() {
			result = append(result, game.ScheduledCommand{
				Key: fmt.Sprintf("auction:%d:%s:%d",
					typed.Round, typed.CurrentPlayer, typed.TurnDeadline.UnixNano()),
				ExecuteAt: typed.TurnDeadline,
				PlayerID:  typed.CurrentPlayer,
				Type:      CommandTurnTimeout,
			})
		}
	case StatusSellSelect:
		for _, playerID := range typed.PlayerOrder {
			player := typed.Roster[playerID]
			if player == nil || player.AI || player.LockedProperty != 0 ||
				player.ActionDeadline.IsZero() {
				continue
			}
			result = append(result, game.ScheduledCommand{
				Key: fmt.Sprintf("sell:%d:%s:%d",
					typed.Round, playerID, player.ActionDeadline.UnixNano()),
				ExecuteAt: player.ActionDeadline,
				PlayerID:  playerID,
				Type:      CommandTurnTimeout,
			})
		}
	case StatusBuyResult, StatusBridge, StatusSellLocked, StatusSellReveal:
		if !typed.TransitionDeadline.IsZero() {
			result = append(result, game.ScheduledCommand{
				Key: fmt.Sprintf("phase:%s:%d:%d",
					typed.Status, typed.Round, typed.TransitionDeadline.UnixNano()),
				ExecuteAt: typed.TransitionDeadline,
				PlayerID:  typed.OwnerID,
				Type:      CommandPhaseTimeout,
			})
		}
	}
	return result, nil
}

func (p *Plugin) applyTurnTimeout(
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
	case CommandBid:
		amount := intPayload(command.Payload, "amount")
		return applyBid(state, playerID, amount, SourceTimeout)
	case CommandPass:
		return applyPass(state, playerID, SourceTimeout)
	case CommandLockSale:
		property := intPayload(command.Payload, "property")
		return lockSale(state, playerID, property, SourceTimeout)
	default:
		return errors.New("timeout planner returned an unsupported action")
	}
}

func applyPhaseTimeout(state *State) error {
	switch state.Status {
	case StatusBuyResult, StatusSellReveal:
		return advanceRound(state, SourceTimeout)
	case StatusBridge:
		appendAction(state, Action{
			Round: state.Round, Phase: state.Status, Kind: "startSelling",
			PlayerID: state.OwnerID, Source: SourceTimeout,
		})
		return startSelling(state)
	case StatusSellLocked:
		return revealSale(state, SourceTimeout)
	default:
		return errors.New("phase timeout is stale")
	}
}

func advanceRound(state *State, source string) error {
	appendAction(state, Action{
		Round: state.Round, Phase: state.Status, Kind: "nextRound",
		PlayerID: state.OwnerID, Source: source,
	})
	switch state.Status {
	case StatusBuyResult:
		return advanceBuyRound(state)
	case StatusSellReveal:
		return advanceSaleRound(state)
	default:
		return errors.New("next round is not available")
	}
}

func commandSource(state *State, command contracts.RoomCommand) string {
	if command.Type == CommandTurnTimeout || command.Type == CommandPhaseTimeout {
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
	case StatusAuction:
		clearTransitionDeadline(state)
		for _, player := range state.Roster {
			if player != nil {
				player.ActionDeadline = zeroTime()
			}
		}
		state.TurnDeadline = zeroTime()
		state.TurnTimeout = 0
		player := state.Roster[state.CurrentPlayer]
		if player != nil && !player.AI {
			state.TurnTimeout = playerActionTimeout
			state.TurnDeadline = sentAt.Add(playerActionTimeout)
		}
	case StatusSellSelect:
		state.TurnDeadline = zeroTime()
		state.TurnTimeout = 0
		clearTransitionDeadline(state)
		for _, playerID := range state.PlayerOrder {
			player := state.Roster[playerID]
			if player == nil || player.AI || player.LockedProperty != 0 {
				continue
			}
			if player.ActionDeadline.IsZero() {
				player.ActionDeadline = sentAt.Add(playerActionTimeout)
			}
		}
	case StatusBuyResult, StatusBridge, StatusSellLocked, StatusSellReveal:
		clearTurnDeadlines(state)
		if state.TransitionDeadline.IsZero() {
			state.TransitionTimeout = phaseActionTimeout
			state.TransitionDeadline = sentAt.Add(phaseActionTimeout)
		}
	default:
		clearTurnDeadlines(state)
		clearTransitionDeadline(state)
	}
}

func nextAIRequests(state *State) []game.AIRequest {
	playerIDs := []string{}
	switch state.Status {
	case StatusAuction:
		player := state.Roster[state.CurrentPlayer]
		if player != nil && player.AI {
			playerIDs = append(playerIDs, state.CurrentPlayer)
		}
	case StatusSellSelect:
		for _, playerID := range state.PlayerOrder {
			player := state.Roster[playerID]
			if player != nil && player.AI && player.LockedProperty == 0 {
				playerIDs = append(playerIDs, playerID)
			}
		}
	}
	result := make([]game.AIRequest, 0, len(playerIDs))
	for _, playerID := range playerIDs {
		delayRange := int((aiMaxDelay - aiMinDelay) / time.Millisecond)
		offset := int((state.RandomSeed +
			uint64(state.ActionCounter*7919+playerSeat(state, playerID)*101)) %
			uint64(delayRange+1))
		fallback := contracts.RoomCommand{
			Type: CommandTurnTimeout, PlayerID: playerID,
		}
		result = append(result, game.AIRequest{
			RoomID: state.RoomID, GameType: GameType,
			PlayerID: playerID, State: cloneState(state),
			Delay:   aiMinDelay + time.Duration(offset)*time.Millisecond,
			Timeout: aiDecisionTimeout, FallbackCommand: &fallback,
		})
	}
	return result
}

func clearTurnDeadlines(state *State) {
	state.TurnDeadline = zeroTime()
	state.TurnTimeout = 0
	for _, player := range state.Roster {
		if player != nil {
			player.ActionDeadline = zeroTime()
		}
	}
}

func clearTransitionDeadline(state *State) {
	state.TransitionDeadline = zeroTime()
	state.TransitionTimeout = 0
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
	state.Roster[playerID] = &Player{
		UserID: userID, PlayerID: playerID, Online: true,
		Ready:      ownerActor(state, playerID, userID),
		SeatIndex:  firstFreeSeat(state),
		Properties: []int{}, Cheques: []int{},
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
	if err := decodePayload(payload, &value); err != nil || value.Ready == nil {
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
	seat := firstFreeSeat(state)
	request, err := game.DecodeAddAIRequest(payload)
	if err != nil {
		return errors.New("invalid seat")
	}
	if request.SeatIndex != nil {
		seat = *request.SeatIndex
		if seat < 0 || seat >= state.MaxPlayers || seatOccupied(state, seat) {
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
		PlayerID: identity.PlayerID, DisplayName: identity.DisplayName, Online: true,
		Ready: true, GameReady: true, AI: true, SeatIndex: seat,
		Properties: []int{}, Cheques: []int{},
	}
	state.PlayerOrder = append(state.PlayerOrder, identity.PlayerID)
	sortPlayerOrder(state)
	return nil
}

func removePlayer(state *State, actorID, actorUserID string, payload []byte) (string, error) {
	if !ownerActor(state, actorID, actorUserID) || state.Status != StatusMatch {
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
	if len(state.PlayerOrder) < minPlayers || len(state.PlayerOrder) > maxPlayers {
		return errors.New("for sale requires three to six players")
	}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil || !player.Ready || !player.Online {
			return errors.New("all players must be ready and online")
		}
		player.Ready = player.AI
		player.GameReady = player.AI
	}
	if err := prepareMatch(state); err != nil {
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
	state.Status = StatusAuction
	return nil
}

func requireParticipant(state *State, playerID string) error {
	if state.Roster[playerID] == nil {
		return errors.New("player is not in room")
	}
	return nil
}

func decodePayload(data []byte, target any) error {
	if err := json.Unmarshal(data, target); err != nil {
		return errors.New("invalid command payload")
	}
	return nil
}

func intPayload(payload any, key string) int {
	values, _ := payload.(map[string]any)
	switch value := values[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	default:
		return 0
	}
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

func removeString(values []string, target string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func ensureCollections(state *State) {
	if state.Roster == nil {
		state.Roster = map[string]*Player{}
	}
	if state.PlayerOrder == nil {
		state.PlayerOrder = []string{}
	}
	if state.PropertyDeck == nil {
		state.PropertyDeck = []int{}
	}
	if state.ChequeDeck == nil {
		state.ChequeDeck = []int{}
	}
	if state.RemovedProperties == nil {
		state.RemovedProperties = []int{}
	}
	if state.RemovedCheques == nil {
		state.RemovedCheques = []int{}
	}
	if state.PropertyMarket == nil {
		state.PropertyMarket = []int{}
	}
	if state.ChequeMarket == nil {
		state.ChequeMarket = []int{}
	}
	if state.Purchases == nil {
		state.Purchases = []RoundRecord{}
	}
	if state.Sales == nil {
		state.Sales = []RoundRecord{}
	}
	if state.ActionLog == nil {
		state.ActionLog = []Action{}
	}
	if state.Result == nil {
		state.Result = map[string]PlayerResult{}
	}
	for _, player := range state.Roster {
		if player == nil {
			continue
		}
		if player.Properties == nil {
			player.Properties = []int{}
		}
		if player.Cheques == nil {
			player.Cheques = []int{}
		}
	}
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
		return nil, fmt.Errorf("unexpected for sale state %T", state)
	}
	if typed == nil {
		return nil, errors.New("for sale state is nil")
	}
	ensureCollections(typed)
	if err := validateState(typed); err != nil {
		return nil, err
	}
	return typed, nil
}
