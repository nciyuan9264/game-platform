package davinci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

const (
	CommandConnect     = "connect"
	CommandDisconnect  = "disconnect"
	CommandMatchReady  = "match_ready"
	CommandMatchBegin  = "match_begin"
	CommandAddAI       = "match_add_ai"
	CommandRemove      = "match_remove_player"
	CommandGameReady   = "game_ready"
	CommandGetCard     = "game_get_card"
	CommandGuessCard   = "game_guess_card"
	CommandSetCard     = "game_set_card"
	CommandRestart     = "game_restart_game"
	CommandTurnTimeout = "turn_timeout"
	CommandAIDecide    = "ai_decide"
	StatusWaiting      = "waiting"
	StatusGetCard      = "getCard"
	StatusGuessCard    = "guessCard"
	StatusSetCard      = "setCard"
	StatusEnd          = "end"
	NumMinus1          = -1
)

var turnTimeoutByStatus = map[string]time.Duration{
	StatusGetCard:   20 * time.Second,
	StatusGuessCard: 60 * time.Second,
	StatusSetCard:   20 * time.Second,
}

type Card struct {
	ID         string `json:"id"`
	Color      int    `json:"color"`
	Num        int    `json:"num"`
	IsRevealed bool   `json:"isRevealed"`
	Index      int    `json:"index"`
}

type Player struct {
	PlayerID  string `json:"playerID"`
	Online    bool   `json:"online"`
	Ready     bool   `json:"ready"`
	AI        bool   `json:"ai"`
	SeatIndex int    `json:"seatIndex"`
	Cards     []Card `json:"cards"`
}

type State struct {
	RoomID        string             `json:"roomID"`
	OwnerID       string             `json:"ownerID"`
	Status        string             `json:"status"`
	MaxPlayers    int                `json:"maxPlayers"`
	CurrentPlayer string             `json:"currentPlayer"`
	PlayerSeq     []string           `json:"playerSeq"`
	Players       map[string]*Player `json:"players"`
	Deck          []Card             `json:"deck"`
	LastAction    any                `json:"lastData,omitempty"`
	WinnerID      string             `json:"winnerID,omitempty"`
	Seq           int64              `json:"seq"`
	RandomSeed    uint64             `json:"randomSeed"`
	RandomCounter uint64             `json:"randomCounter"`
	TurnDeadline  time.Time          `json:"turnDeadline,omitempty"`
	TurnTimeout   time.Duration      `json:"turnTimeout,omitempty"`
}

type Engine struct{}

func NewEngine() *Engine { return &Engine{} }

const EngineVersion = "davinci-v2"

func Definition() game.Definition {
	plugin := NewPlugin()
	return game.Definition{
		GameType:          contracts.GameDaVinci,
		Version:           EngineVersion,
		DefaultMaxPlayers: 2,
		Engine:            plugin,
		AI:                NewAIPlanner(),
		Scheduler:         plugin,
	}
}

type Plugin struct {
	engine *Engine
}

func NewPlugin() *Plugin {
	return &Plugin{engine: NewEngine()}
}

func (p *Plugin) GameType() string { return contracts.GameDaVinci }

func (p *Plugin) Version() string { return EngineVersion }

func (p *Plugin) Roster(ctx context.Context, state game.State) ([]game.RosterPlayer, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	players := make([]game.RosterPlayer, 0, len(typed.PlayerSeq))
	for _, playerID := range typed.PlayerSeq {
		player := typed.Players[playerID]
		if player == nil {
			continue
		}
		userID := playerID
		displayName := playerID
		if !player.AI {
			displayName = ""
			if index := strings.LastIndex(playerID, "-"); index >= 0 && index+1 < len(playerID) {
				userID = playerID[index+1:]
			}
		}
		players = append(players, game.RosterPlayer{
			UserID:      userID,
			PlayerID:    playerID,
			DisplayName: displayName,
			Online:      player.Online,
			IsAI:        player.AI,
			SeatIndex:   player.SeatIndex,
			Ready:       player.Ready,
		})
	}
	return players, nil
}

func (p *Plugin) InitRoom(ctx context.Context, cfg game.RoomConfig) (game.State, error) {
	if cfg.RandomSeed == 0 {
		return nil, errors.New("random seed is required")
	}
	return p.engine.InitRoomWithSeed(cfg.RoomID, cfg.OwnerPlayerID, cfg.MaxPlayers, cfg.RandomSeed), nil
}

func (p *Plugin) EncodeState(ctx context.Context, state game.State) ([]byte, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	return json.Marshal(typed)
}

func (p *Plugin) DecodeState(ctx context.Context, data []byte) (game.State, error) {
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if state.RandomSeed == 0 {
		state.RandomSeed = davinciRoomSeed(state.RoomID)
	}
	normalizeSeats(&state)
	return &state, nil
}

func (p *Plugin) ApplyCommand(ctx context.Context, state game.State, cmd contracts.RoomCommand) (game.ApplyResult, error) {
	typed, err := asState(state)
	if err != nil {
		return game.ApplyResult{}, err
	}
	removedPlayerID := ""
	if cmd.Type == CommandRemove {
		removedPlayerID, _ = payloadString(cmd.Payload, "playerID")
	}
	result, err := p.engine.ApplyCommand(ctx, typed, cmd)
	if err != nil {
		return game.ApplyResult{}, err
	}
	commandTime := cmd.SentAt.UTC()
	if commandTime.IsZero() {
		commandTime = time.Now().UTC()
	}
	updateTurnDeadline(typed, commandTime)
	out := game.ApplyResult{Status: typed.Status, Finished: result.Finished}
	if removedPlayerID != "" {
		out.Effects = append(out.Effects, game.DeliveryEffect{
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
	for _, req := range result.AIRequests {
		fallback := contracts.RoomCommand{Type: CommandTurnTimeout, PlayerID: req.PlayerID}
		out.AIRequests = append(out.AIRequests, game.AIRequest{
			RoomID:          req.RoomID,
			GameType:        contracts.GameDaVinci,
			PlayerID:        req.PlayerID,
			State:           req.State,
			FallbackCommand: &fallback,
		})
	}
	return out, nil
}

func (p *Plugin) PendingCommands(ctx context.Context, state game.State) ([]game.ScheduledCommand, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	if typed.TurnDeadline.IsZero() || typed.TurnTimeout <= 0 || typed.CurrentPlayer == "" {
		return nil, nil
	}
	player := typed.Players[typed.CurrentPlayer]
	if player == nil || player.AI {
		return nil, nil
	}
	command, err := NewAIPlanner().Decide(ctx, game.AIRequest{
		RoomID:   typed.RoomID,
		GameType: contracts.GameDaVinci,
		PlayerID: typed.CurrentPlayer,
		State:    cloneState(typed),
	})
	if err != nil {
		command = contracts.RoomCommand{Type: CommandTurnTimeout, PlayerID: typed.CurrentPlayer}
	}
	return []game.ScheduledCommand{{
		Key:       fmt.Sprintf("%s:%s:%d", typed.Status, typed.CurrentPlayer, typed.TurnDeadline.UnixNano()),
		ExecuteAt: typed.TurnDeadline,
		PlayerID:  typed.CurrentPlayer,
		Type:      command.Type,
		Payload:   command.Payload,
	}}, nil
}

func (p *Plugin) BuildPlayerView(ctx context.Context, state game.State, req game.ViewRequest) (any, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	if typed.Status == contracts.RoomStatusMatch {
		view := p.engine.BuildMatchSync(typed, req.PlayerID)
		view.MatchID = req.MatchID
		view.Visibility = req.Visibility
		view.MaxPlayers = req.MaxPlayers
		view.PlayerOrder = append([]string{}, typed.PlayerSeq...)
		return view, nil
	}
	view := p.engine.BuildRoomSync(typed, req.PlayerID)
	view.MatchID = req.MatchID
	view.Seq = req.Seq
	return view, nil
}

func (p *Plugin) BuildSpectatorView(_ context.Context, state game.State, seq int64) (any, error) {
	typed, err := asState(state)
	if err != nil {
		return nil, err
	}
	players := make(map[string]contracts.RoomPlayer, len(typed.Players))
	playersData := make(map[string]*Player, len(typed.Players))
	boardCards := map[string]map[string]any{}
	for _, card := range typed.Deck {
		boardCards[card.ID] = map[string]any{"id": card.ID, "color": card.Color}
	}
	for playerID, player := range typed.Players {
		players[playerID] = contracts.RoomPlayer{
			PlayerID:  playerID,
			Online:    player.Online,
			AI:        player.AI,
			Ready:     player.Ready,
			SeatIndex: player.SeatIndex,
		}
		playersData[playerID] = player
	}
	return map[string]any{
		"type":    contracts.MessageTypeRoomSync,
		"roomID":  typed.RoomID,
		"ownerID": typed.OwnerID,
		"seq":     seq,
		"roomData": map[string]any{
			"currentPlayer": typed.CurrentPlayer,
			"gameStatus":    typed.Status,
			"players":       players,
			"boardCards":    boardCards,
			"lastData":      typed.LastAction,
			"turnDeadline":  turnDeadlineValue(typed.TurnDeadline),
			"turnTimeoutMs": typed.TurnTimeout.Milliseconds(),
		},
		"playersData": playersData,
		"result":      map[string]any{"winnerID": typed.WinnerID},
	}, nil
}

func asState(state game.State) (*State, error) {
	switch typed := state.(type) {
	case *State:
		if typed == nil {
			return nil, errors.New("davinci state is nil")
		}
		return typed, nil
	case State:
		return &typed, nil
	default:
		return nil, fmt.Errorf("unexpected davinci state %T", state)
	}
}

func (e *Engine) InitRoom(roomID, ownerPlayerID string, maxPlayers int) *State {
	return e.InitRoomWithSeed(roomID, ownerPlayerID, maxPlayers, 0)
}

func (e *Engine) InitRoomWithSeed(roomID, ownerPlayerID string, maxPlayers int, randomSeed uint64) *State {
	if maxPlayers <= 0 {
		maxPlayers = 2
	}
	if randomSeed == 0 {
		randomSeed = davinciRoomSeed(roomID)
	}
	return &State{
		RoomID:     roomID,
		OwnerID:    ownerPlayerID,
		Status:     contracts.RoomStatusMatch,
		MaxPlayers: maxPlayers,
		Players:    map[string]*Player{},
		RandomSeed: randomSeed,
	}
}

func davinciRoomSeed(roomID string) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(roomID))
	return hash.Sum64()
}

type Result struct {
	AIRequests []AIRequest
	Finished   bool
}

type AIRequest struct {
	RoomID   string `json:"roomID"`
	PlayerID string `json:"playerID"`
	State    *State `json:"state"`
}

func (e *Engine) ApplyCommand(ctx context.Context, state *State, cmd contracts.RoomCommand) (Result, error) {
	switch cmd.Type {
	case CommandConnect:
		return Result{}, e.connect(state, cmd.PlayerID, false)
	case CommandDisconnect:
		return Result{}, e.disconnect(state, cmd.PlayerID)
	case CommandMatchReady:
		ready, ok := payloadBool(cmd.Payload, "ready")
		if !ok {
			return Result{}, errors.New("ready is required")
		}
		return Result{}, e.ready(state, cmd.PlayerID, ready)
	case CommandAddAI:
		seatIndex, hasSeat := payloadInt(cmd.Payload, "seatIndex")
		return Result{}, e.addAI(state, cmd.PlayerID, optionalInt(seatIndex, hasSeat))
	case CommandRemove:
		targetPlayerID, ok := payloadString(cmd.Payload, "playerID")
		if !ok {
			return Result{}, errors.New("playerID is required")
		}
		return Result{}, e.removePlayer(state, cmd.PlayerID, targetPlayerID)
	case CommandMatchBegin:
		return e.begin(state, cmd.PlayerID)
	case CommandGameReady:
		return e.gameReady(state)
	case CommandGetCard:
		return e.getCard(state, cmd.PlayerID, cmd.Payload)
	case CommandGuessCard:
		return e.guessCard(state, cmd.PlayerID, cmd.Payload)
	case CommandSetCard:
		return e.setCard(state, cmd.PlayerID, cmd.Payload)
	case CommandTurnTimeout:
		if cmd.ConnectionRef.Valid() {
			return Result{}, errors.New("clients cannot submit turn timeout")
		}
		return e.turnTimeout(state, cmd.PlayerID)
	case CommandRestart:
		return Result{}, e.restart(state, cmd.PlayerID)
	default:
		return Result{}, fmt.Errorf("unknown command %s", cmd.Type)
	}
}

func (e *Engine) connect(state *State, playerID string, ai bool) error {
	if p, ok := state.Players[playerID]; ok {
		p.Online = true
		return nil
	}
	if state.Status != contracts.RoomStatusMatch {
		return errors.New("game already started")
	}
	if len(state.Players) >= state.MaxPlayers {
		return errors.New("room full")
	}
	state.Players[playerID] = &Player{
		PlayerID:  playerID,
		Online:    true,
		Ready:     playerID == state.OwnerID,
		AI:        ai,
		SeatIndex: firstAvailableSeat(state),
	}
	state.PlayerSeq = append(state.PlayerSeq, playerID)
	sortPlayersBySeat(state)
	return nil
}

func (e *Engine) disconnect(state *State, playerID string) error {
	if p, ok := state.Players[playerID]; ok {
		p.Online = false
	}
	return nil
}

func (e *Engine) ready(state *State, playerID string, ready bool) error {
	if state.Status != contracts.RoomStatusMatch {
		return errors.New("not in match")
	}
	if p, ok := state.Players[playerID]; ok {
		if playerID == state.OwnerID && !ready {
			return errors.New("room owner is always ready")
		}
		p.Ready = ready
		return nil
	}
	return errors.New("player not found")
}

func (e *Engine) addAI(state *State, actor string, requestedSeat *int) error {
	if actor != state.OwnerID {
		return errors.New("only the room owner can add AI players")
	}
	if state.Status != contracts.RoomStatusMatch {
		return errors.New("not in match")
	}
	if len(state.Players) >= state.MaxPlayers {
		return errors.New("room full")
	}
	seatIndex := firstAvailableSeat(state)
	if requestedSeat != nil {
		if *requestedSeat < 0 || *requestedSeat >= state.MaxPlayers || seatOccupied(state, *requestedSeat) {
			return errors.New("invalid or occupied seat")
		}
		seatIndex = *requestedSeat
	}
	id := fmt.Sprintf("AI_%d", len(state.Players)+1)
	state.Players[id] = &Player{
		PlayerID:  id,
		Online:    true,
		Ready:     true,
		AI:        true,
		SeatIndex: seatIndex,
	}
	state.PlayerSeq = append(state.PlayerSeq, id)
	sortPlayersBySeat(state)
	return nil
}

func (e *Engine) removePlayer(state *State, actor, target string) error {
	if actor != state.OwnerID {
		return errors.New("only the room owner can remove players")
	}
	if state.Status != contracts.RoomStatusMatch {
		return errors.New("not in match")
	}
	if target == state.OwnerID || target == actor {
		return errors.New("cannot remove room owner")
	}
	if state.Players[target] == nil {
		return errors.New("player not found")
	}
	delete(state.Players, target)
	state.PlayerSeq = removePlayerID(state.PlayerSeq, target)
	return nil
}

func (e *Engine) begin(state *State, actor string) (Result, error) {
	if actor != state.OwnerID {
		return Result{}, errors.New("only the room owner can start the game")
	}
	if state.Status != contracts.RoomStatusMatch {
		return Result{}, errors.New("not in match")
	}
	if len(state.Players) < 2 {
		return Result{}, errors.New("need at least two players")
	}
	for _, p := range state.Players {
		if p == nil || !p.Ready || !p.Online {
			return Result{}, errors.New("not all players ready")
		}
	}
	round := state.RandomCounter
	state.RandomCounter++
	state.Deck = buildDeck(state.RandomSeed, round)
	for _, pid := range state.PlayerSeq {
		for i := 0; i < 5; i++ {
			if err := drawInitialCard(state, pid); err != nil {
				return Result{}, err
			}
		}
	}
	state.Status = StatusWaiting
	return Result{}, nil
}

func (e *Engine) restart(state *State, actor string) error {
	if actor != state.OwnerID {
		return errors.New("only the room owner can restart the game")
	}
	return errors.New("terminal rooms cannot be restarted in place; create a new room")
}

func (e *Engine) gameReady(state *State) (Result, error) {
	if state.Status != StatusWaiting {
		return Result{}, errors.New("not waiting")
	}
	if state.CurrentPlayer == "" {
		if len(state.PlayerSeq) == 0 {
			return Result{}, errors.New("no players")
		}
		state.CurrentPlayer = state.PlayerSeq[0]
	}
	if len(state.Deck) == 0 {
		state.Status = StatusGuessCard
	} else {
		state.Status = StatusGetCard
	}
	return e.maybeAI(state), nil
}

func (e *Engine) getCard(state *State, playerID string, payload any) (Result, error) {
	if state.Status != StatusGetCard || state.CurrentPlayer != playerID {
		return Result{}, errors.New("not allowed to get card")
	}
	id, _ := payloadString(payload, "id")
	if id == "" {
		return Result{}, errors.New("card id is required")
	}
	if err := drawBoardCard(state, playerID, id); err != nil {
		return Result{}, err
	}
	state.Status = StatusGuessCard
	state.LastAction = map[string]any{"action": CommandGetCard, "playerID": playerID, "payload": map[string]any{"id": id}}
	return e.maybeAI(state), nil
}

func (e *Engine) guessCard(state *State, playerID string, payload any) (Result, error) {
	if state.Status != StatusGuessCard && state.Status != StatusSetCard || state.CurrentPlayer != playerID {
		return Result{}, errors.New("not allowed to guess")
	}
	targetCardID, _ := payloadString(payload, "id")
	if targetCardID == "" {
		targetCardID, _ = payloadString(payload, "targetCardID")
	}
	guessNum, ok := payloadInt(payload, "num")
	if !ok {
		guessNum, ok = payloadInt(payload, "guessNum")
	}
	if !ok {
		return Result{}, errors.New("guess number is required")
	}
	var target *Card
	owner := ""
	for pid, p := range state.Players {
		for i := range p.Cards {
			if p.Cards[i].ID == targetCardID {
				target = &p.Cards[i]
				owner = pid
				break
			}
		}
		if target != nil {
			break
		}
	}
	if target == nil {
		return Result{}, errors.New("target card not found")
	}
	if owner == playerID {
		return Result{}, errors.New("cannot guess own card")
	}
	if target.IsRevealed {
		return Result{}, errors.New("cannot guess a revealed card")
	}
	if guessNum < NumMinus1 || guessNum > 11 {
		return Result{}, errors.New("guess number is out of range")
	}
	correct := target.Num == guessNum
	state.LastAction = map[string]any{"action": "guess_card", "playerID": playerID, "payload": map[string]any{"targetPlayerID": owner, "targetCardID": targetCardID, "guessNum": guessNum, "correct": correct}}
	if correct {
		target.IsRevealed = true
		if e.allCardsRevealed(state, owner) {
			state.Status = StatusEnd
			state.WinnerID = e.findWinner(state)
			return Result{Finished: true}, nil
		}
		state.Status = StatusSetCard
		return e.maybeAI(state), nil
	}
	if err := revealPendingCard(state, playerID); err != nil {
		return Result{}, err
	}
	e.nextPlayer(state)
	if len(state.Deck) == 0 {
		state.Status = StatusGuessCard
	} else {
		state.Status = StatusGetCard
	}
	return e.maybeAI(state), nil
}

func (e *Engine) setCard(state *State, playerID string, payload any) (Result, error) {
	if state.Status != StatusSetCard || state.CurrentPlayer != playerID {
		return Result{}, errors.New("not allowed to set card")
	}
	cardID, _ := payloadString(payload, "id")
	if cardID == "" {
		cardID, _ = payloadString(payload, "cardID")
	}
	index, ok := payloadInt(payload, "index")
	if !ok {
		return Result{}, errors.New("card index is required")
	}
	p := state.Players[playerID]
	if p == nil {
		return Result{}, errors.New("player not found")
	}
	targetIndex := -1
	for i := range p.Cards {
		if p.Cards[i].ID == cardID {
			targetIndex = i
			break
		}
	}
	if targetIndex < 0 {
		return Result{}, errors.New("card not found")
	}
	target := &p.Cards[targetIndex]
	if target.Index != -1 {
		return Result{}, errors.New("only the card drawn this turn can be placed")
	}
	if index < 0 || index > len(p.Cards)-1 {
		return Result{}, errors.New("card index is out of range")
	}
	if target.Num != NumMinus1 && index != computeInsertIndex(p.Cards, target) {
		return Result{}, errors.New("number cards must remain sorted")
	}
	for i := range p.Cards {
		if p.Cards[i].ID == target.ID {
			continue
		}
		if p.Cards[i].Index >= index {
			p.Cards[i].Index++
		}
	}
	target.Index = index
	if len(state.Deck) == 0 {
		state.Status = StatusGuessCard
	} else {
		state.Status = StatusGetCard
	}
	e.nextPlayer(state)
	return e.maybeAI(state), nil
}

func (e *Engine) turnTimeout(state *State, playerID string) (Result, error) {
	if state.Status == StatusEnd {
		return Result{Finished: true}, nil
	}
	if state.CurrentPlayer != playerID {
		return Result{}, errors.New("stale turn timeout")
	}
	e.nextPlayer(state)
	if len(state.Deck) == 0 {
		state.Status = StatusGuessCard
	} else {
		state.Status = StatusGetCard
	}
	return e.maybeAI(state), nil
}

func (e *Engine) maybeAI(state *State) Result {
	if state.Status == StatusEnd || state.CurrentPlayer == "" {
		return Result{Finished: state.Status == StatusEnd}
	}
	p := state.Players[state.CurrentPlayer]
	if p != nil && p.AI {
		return Result{AIRequests: []AIRequest{{RoomID: state.RoomID, PlayerID: p.PlayerID, State: cloneState(state)}}}
	}
	return Result{}
}

func (e *Engine) nextPlayer(state *State) {
	if len(state.PlayerSeq) == 0 {
		return
	}
	for i, pid := range state.PlayerSeq {
		if pid == state.CurrentPlayer {
			state.CurrentPlayer = state.PlayerSeq[(i+1)%len(state.PlayerSeq)]
			return
		}
	}
	state.CurrentPlayer = state.PlayerSeq[0]
}

func (e *Engine) allCardsRevealed(state *State, playerID string) bool {
	p := state.Players[playerID]
	if p == nil {
		return false
	}
	for _, c := range p.Cards {
		if !c.IsRevealed {
			return false
		}
	}
	return true
}

func (e *Engine) BuildMatchSync(state *State, playerID string) contracts.MatchSync {
	players := map[string]contracts.RoomPlayer{}
	for id, p := range state.Players {
		players[id] = contracts.RoomPlayer{
			PlayerID:  id,
			Online:    p.Online,
			AI:        p.AI,
			Ready:     p.Ready,
			SeatIndex: p.SeatIndex,
		}
	}
	return contracts.MatchSync{Type: contracts.MessageTypeMatchSync, RoomID: state.RoomID, OwnerID: state.OwnerID, Status: state.Status, PlayerID: playerID, Players: players}
}

func (e *Engine) BuildRoomSync(state *State, playerID string) contracts.RoomSync {
	players := map[string]contracts.RoomPlayer{}
	boardCards := map[string]map[string]any{}
	self := []Card{}
	opponents := []map[string]any{}
	for _, c := range state.Deck {
		boardCards[c.ID] = map[string]any{"id": c.ID, "color": c.Color}
	}
	for id, p := range state.Players {
		players[id] = contracts.RoomPlayer{
			PlayerID:  id,
			Online:    p.Online,
			AI:        p.AI,
			Ready:     p.Ready,
			SeatIndex: p.SeatIndex,
		}
		for _, c := range p.Cards {
			if id == playerID {
				self = append(self, c)
				continue
			}
			item := map[string]any{"id": c.ID, "color": c.Color, "index": c.Index, "isRevealed": c.IsRevealed}
			if c.IsRevealed || state.Status == StatusEnd {
				item["num"] = c.Num
			}
			opponents = append(opponents, item)
		}
	}
	return contracts.RoomSync{
		Type:     contracts.MessageTypeRoomSync,
		RoomID:   state.RoomID,
		PlayerID: playerID,
		OwnerID:  state.OwnerID,
		Seq:      state.Seq,
		RoomData: map[string]any{
			"currentPlayer": state.CurrentPlayer,
			"gameStatus":    state.Status,
			"players":       players,
			"boardCards":    boardCards,
			"lastData":      state.LastAction,
			"turnDeadline":  turnDeadlineValue(state.TurnDeadline),
			"turnTimeoutMs": state.TurnTimeout.Milliseconds(),
		},
		CardData: map[string]any{"self": self, "opponents": opponents},
		Result:   map[string]any{"winnerID": state.WinnerID},
	}
}

func buildDeck(seed, counter uint64) []Card {
	deck := make([]Card, 0, 26)
	for color := 0; color < 2; color++ {
		for num := NumMinus1; num <= 11; num++ {
			deck = append(deck, Card{Color: color, Num: num, Index: -1})
		}
	}
	value := seed + counter
	rand.New(rand.NewPCG(value, ^value)).Shuffle(len(deck), func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })
	for index := range deck {
		deck[index].ID = fmt.Sprintf("r%d_c%02d", counter, index)
	}
	return deck
}

func drawInitialCard(state *State, playerID string) error {
	if len(state.Deck) == 0 {
		return errors.New("deck empty")
	}
	card := state.Deck[0]
	state.Deck = state.Deck[1:]
	p := state.Players[playerID]
	if p == nil {
		return errors.New("player not found")
	}
	p.Cards = append(p.Cards, card)
	sortInitialHand(p.Cards)
	return nil
}

func drawBoardCard(state *State, playerID, id string) error {
	if len(state.Deck) == 0 {
		return errors.New("deck empty")
	}
	idx := 0
	if id != "" {
		found := -1
		for i, c := range state.Deck {
			if c.ID == id {
				found = i
				break
			}
		}
		if found < 0 {
			return errors.New("board card not found")
		}
		idx = found
	}
	card := state.Deck[idx]
	state.Deck = append(state.Deck[:idx], state.Deck[idx+1:]...)
	card.Index = -1
	p := state.Players[playerID]
	if p == nil {
		return errors.New("player not found")
	}
	p.Cards = append(p.Cards, card)
	return nil
}

func revealPendingCard(state *State, playerID string) error {
	p := state.Players[playerID]
	if p == nil {
		return errors.New("player not found")
	}
	var target *Card
	for i := range p.Cards {
		if p.Cards[i].Index == -1 {
			target = &p.Cards[i]
			break
		}
	}
	if target == nil {
		return nil
	}
	target.IsRevealed = true
	pos := computeInsertIndex(p.Cards, target)
	for i := range p.Cards {
		if p.Cards[i].ID == target.ID {
			continue
		}
		if p.Cards[i].Index >= pos {
			p.Cards[i].Index++
		}
	}
	target.Index = pos
	return nil
}

func sortInitialHand(cards []Card) {
	sort.Slice(cards, func(i, j int) bool {
		if cards[i].Num == NumMinus1 && cards[j].Num != NumMinus1 {
			return false
		}
		if cards[j].Num == NumMinus1 && cards[i].Num != NumMinus1 {
			return true
		}
		if cards[i].Num != cards[j].Num {
			return cards[i].Num < cards[j].Num
		}
		return cards[i].Color == 1 && cards[j].Color == 0
	})
	for i := range cards {
		cards[i].Index = i
	}
}

func computeInsertIndex(cards []Card, newCard *Card) int {
	count := 0
	pos := -1
	for i := range cards {
		c := &cards[i]
		if c.ID == newCard.ID {
			continue
		}
		count++
		if c.Num == NumMinus1 {
			continue
		}
		if cardOrderLess(newCard.Num, newCard.Color, c.Num, c.Color) {
			if pos == -1 || c.Index < pos {
				pos = c.Index
			}
		}
	}
	if pos == -1 {
		return count
	}
	return pos
}

func cardOrderLess(numA, colorA, numB, colorB int) bool {
	if numA != numB {
		return numA < numB
	}
	return colorA == 1 && colorB == 0
}

func updateTurnDeadline(state *State, commandTime time.Time) {
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	if state.Status == StatusEnd {
		return
	}
	player := state.Players[state.CurrentPlayer]
	timeout, timed := turnTimeoutByStatus[state.Status]
	if !timed || player == nil || player.AI {
		return
	}
	state.TurnTimeout = timeout
	state.TurnDeadline = commandTime.Add(timeout)
}

func turnDeadlineValue(deadline time.Time) any {
	if deadline.IsZero() {
		return nil
	}
	return deadline.UTC().Format(time.RFC3339Nano)
}

func optionalInt(value int, ok bool) *int {
	if !ok {
		return nil
	}
	return &value
}

func normalizeSeats(state *State) {
	used := make(map[int]bool, len(state.Players))
	for _, playerID := range state.PlayerSeq {
		player := state.Players[playerID]
		if player == nil {
			continue
		}
		if player.SeatIndex < 0 || player.SeatIndex >= state.MaxPlayers || used[player.SeatIndex] {
			player.SeatIndex = firstUnusedSeat(state.MaxPlayers, used)
		}
		used[player.SeatIndex] = true
	}
	sortPlayersBySeat(state)
}

func firstAvailableSeat(state *State) int {
	used := make(map[int]bool, len(state.Players))
	for _, player := range state.Players {
		if player != nil {
			used[player.SeatIndex] = true
		}
	}
	return firstUnusedSeat(state.MaxPlayers, used)
}

func firstUnusedSeat(maxPlayers int, used map[int]bool) int {
	for seatIndex := 0; seatIndex < maxPlayers; seatIndex++ {
		if !used[seatIndex] {
			return seatIndex
		}
	}
	return len(used)
}

func seatOccupied(state *State, seatIndex int) bool {
	for _, player := range state.Players {
		if player != nil && player.SeatIndex == seatIndex {
			return true
		}
	}
	return false
}

func sortPlayersBySeat(state *State) {
	sort.SliceStable(state.PlayerSeq, func(i, j int) bool {
		left := state.Players[state.PlayerSeq[i]]
		right := state.Players[state.PlayerSeq[j]]
		if left == nil || right == nil {
			return state.PlayerSeq[i] < state.PlayerSeq[j]
		}
		if left.SeatIndex != right.SeatIndex {
			return left.SeatIndex < right.SeatIndex
		}
		return left.PlayerID < right.PlayerID
	})
}

func removePlayerID(playerIDs []string, target string) []string {
	for index, playerID := range playerIDs {
		if playerID == target {
			return append(playerIDs[:index], playerIDs[index+1:]...)
		}
	}
	return playerIDs
}

func (e *Engine) findWinner(state *State) string {
	alive := make([]string, 0, len(state.Players))
	for pid, p := range state.Players {
		allRevealed := true
		for _, c := range p.Cards {
			if !c.IsRevealed {
				allRevealed = false
				break
			}
		}
		if !allRevealed {
			alive = append(alive, pid)
		}
	}
	sort.Strings(alive)
	if len(alive) == 0 {
		return ""
	}
	return alive[0]
}

func payloadString(payload any, key string) (string, bool) {
	m := payloadMap(payload)
	v, ok := m[key].(string)
	return v, ok
}

func payloadInt(payload any, key string) (int, bool) {
	m := payloadMap(payload)
	switch v := m[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
}

func payloadBool(payload any, key string) (bool, bool) {
	m := payloadMap(payload)
	v, ok := m[key].(bool)
	return v, ok
}

func payloadMap(payload any) map[string]any {
	if payload == nil {
		return map[string]any{}
	}
	if m, ok := payload.(map[string]any); ok {
		return m
	}
	b, _ := json.Marshal(payload)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func cloneState(state *State) *State {
	b, _ := json.Marshal(state)
	var out State
	_ = json.Unmarshal(b, &out)
	return &out
}
