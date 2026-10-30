package acquire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
	acquireai "github.com/nciyuan9264/game-platform/internal/plugins/acquire/ai"
	"github.com/nciyuan9264/game-platform/internal/plugins/acquire/domain/data"
	"github.com/nciyuan9264/game-platform/internal/plugins/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform/internal/plugins/acquire/logger"
	"github.com/nciyuan9264/game-platform/internal/plugins/acquire/roomcore"
	rules "github.com/nciyuan9264/game-platform/internal/plugins/acquire/rules"
)

const (
	GameType         = "acquire"
	EngineVersion    = "acquire-v6"
	maxNoHumanChecks = 3
)

const ReplaySchemaVersion = 2

var aiNames = []string{"摩根", "诺兰", "薇拉", "里德", "艾弗里"}

var timeoutByStatus = map[domain.RoomStatus]time.Duration{
	domain.RoomStatusSetTile:          30 * time.Second,
	domain.RoomStatusCreateCompany:    20 * time.Second,
	domain.RoomStatusBuyStock:         40 * time.Second,
	domain.RoomStatusMergingSelection: 20 * time.Second,
	domain.RoomStatusMergingSettle:    60 * time.Second,
}

type Plugin struct{}

func Definition() game.Definition {
	plugin := &Plugin{}
	return game.Definition{
		GameType:          GameType,
		Version:           EngineVersion,
		DefaultMaxPlayers: domain.DefaultMaxPlayers,
		Engine:            plugin,
		AI:                plugin,
		Scheduler:         plugin,
		History:           plugin,
		LegacyReplay:      plugin,
	}
}

func (p *Plugin) GameType() string { return GameType }

func (p *Plugin) Version() string { return EngineVersion }

func (p *Plugin) RandomSeed(ctx context.Context, state game.State) (uint64, error) {
	room, err := asRoom(state)
	if err != nil {
		return 0, err
	}
	return room.State.RandomSeed, nil
}

func (p *Plugin) RoomMetadata(ctx context.Context, state game.State) (game.RoomMetadata, error) {
	room, err := asRoom(state)
	if err != nil {
		return game.RoomMetadata{}, err
	}
	return game.RoomMetadata{Visibility: room.State.Visibility}, nil
}

func (p *Plugin) Roster(ctx context.Context, state game.State) ([]game.RosterPlayer, error) {
	room, err := asRoom(state)
	if err != nil {
		return nil, err
	}
	players := make([]game.RosterPlayer, 0, len(room.PlayerSeq))
	for _, playerID := range room.PlayerSeq {
		connection := room.Connections[playerID]
		if connection == nil {
			continue
		}
		displayName := ""
		if connection.AI {
			displayName = playerID
			if name, ok := game.AIName(playerID, aiNames); ok {
				displayName = name
			}
		}
		players = append(players, game.RosterPlayer{
			PlayerID:    playerID,
			DisplayName: displayName,
			Online:      connection.Online,
			IsAI:        connection.AI,
			SeatIndex:   connection.SeatIndex,
			Ready:       connection.Ready,
		})
	}
	return players, nil
}

func (p *Plugin) Progress(ctx context.Context, state game.State) (game.RoomProgress, error) {
	room, err := asRoom(state)
	if err != nil {
		return game.RoomProgress{}, err
	}
	placedTiles := 0
	for _, tile := range room.State.BoardTiles {
		if tile != nil && tile.Belong != "" {
			placedTiles++
		}
	}
	return game.RoomProgress{Current: placedTiles, Total: 108}, nil
}

func (p *Plugin) ObserveHumanPresence(ctx context.Context, state game.State, hasHuman bool) (game.HumanPresenceCheck, error) {
	room, err := asRoom(state)
	if err != nil {
		return game.HumanPresenceCheck{}, err
	}
	if hasHuman {
		room.NoHumanChecks = 0
	} else {
		room.NoHumanChecks++
	}
	return game.HumanPresenceCheck{
		ConsecutiveEmpty: room.NoHumanChecks,
		Limit:            maxNoHumanChecks,
		ShouldClose:      room.NoHumanChecks >= maxNoHumanChecks,
	}, nil
}

func (p *Plugin) InitRoom(ctx context.Context, cfg game.RoomConfig) (game.State, error) {
	maxPlayers := cfg.MaxPlayers
	if maxPlayers <= 0 {
		maxPlayers = domain.DefaultMaxPlayers
	}
	if cfg.RandomSeed == 0 {
		return nil, errors.New("random seed is required")
	}
	visibility := cfg.Visibility
	if visibility == "" {
		visibility = contracts.VisibilityPublic
	}
	base := roomcore.NewBase(cfg.RoomID, 128)
	return &domain.Room{
		Base: base,
		State: &domain.GameState{
			RoomStatus:      domain.RoomStatusMatch,
			OwnerID:         cfg.OwnerPlayerID,
			MaxPlayers:      maxPlayers,
			RandomSeed:      cfg.RandomSeed,
			Visibility:      visibility,
			BoardTiles:      map[string]*domain.Tile{},
			Players:         map[string]*domain.PlayerState{},
			Companies:       map[string]*domain.CompanyState{},
			MergeSettleData: map[string]domain.SettleData{},
			EndGame:         domain.EndGameState{Votes: map[string]bool{}},
		},
	}, nil
}

func (p *Plugin) EncodeState(ctx context.Context, state game.State) ([]byte, error) {
	room, err := asRoom(state)
	if err != nil {
		return nil, err
	}
	return json.Marshal(room)
}

func (p *Plugin) DecodeState(ctx context.Context, raw []byte) (game.State, error) {
	var room domain.Room
	if err := json.Unmarshal(raw, &room); err != nil {
		return nil, err
	}
	if room.Base == nil {
		room.Base = roomcore.NewBase("", 128)
	}
	if room.Connections == nil {
		room.Connections = map[string]*roomcore.PlayerConn{}
	}
	room.CmdCh = make(chan roomcore.Command, 128)
	room.QuitCh = make(chan struct{})
	if room.State == nil {
		return nil, errors.New("acquire snapshot has no game state")
	}
	if room.State.BoardTiles == nil {
		room.State.BoardTiles = map[string]*domain.Tile{}
	}
	if room.State.Players == nil {
		room.State.Players = map[string]*domain.PlayerState{}
	}
	if room.State.Companies == nil {
		room.State.Companies = map[string]*domain.CompanyState{}
	}
	if room.State.MergeSettleData == nil {
		room.State.MergeSettleData = map[string]domain.SettleData{}
	}
	if room.State.EndGame.Votes == nil {
		room.State.EndGame.Votes = map[string]bool{}
	}
	if room.State.Visibility == "" {
		room.State.Visibility = contracts.VisibilityPublic
	}
	normalizeAcquireSeats(&room)
	return &room, nil
}

func (p *Plugin) ApplyCommand(ctx context.Context, state game.State, cmd contracts.RoomCommand) (game.ApplyResult, error) {
	room, err := asRoom(state)
	if err != nil {
		return game.ApplyResult{}, err
	}
	executionMode := contracts.ExecutionModeLive
	if cmd.Replay {
		executionMode = contracts.ExecutionModeReplay
	}
	previousLogger := room.CommandLogger
	room.CommandLogger = logger.DefaultLogger.With(
		logger.F("execution_mode", executionMode),
		logger.F("is_replay", cmd.Replay),
		logger.F("room_id", room.ID),
		logger.F("match_id", cmd.MatchID),
		logger.F("command_id", cmd.CommandID),
		logger.F("command_type", cmd.Type),
	)
	defer func() {
		room.CommandLogger = previousLogger
	}()
	commandTime := cmd.SentAt.UTC()
	if commandTime.IsZero() {
		commandTime = time.Now().UTC()
	}
	payload, err := json.Marshal(cmd.Payload)
	if err != nil {
		return game.ApplyResult{}, fmt.Errorf("encode command payload: %w", err)
	}
	if err := validateAcquireCommand(room, cmd, payload); err != nil {
		return game.ApplyResult{}, err
	}
	legacy := domain.Command{Type: cmd.Type, PlayerID: cmd.PlayerID, Payload: payload, Replay: cmd.Replay}
	result := game.ApplyResult{}
	previousStatus := room.State.RoomStatus
	previousPlayer := room.State.CurrentPlayer
	previousTimedPlayer := acquireai.CurrentTimeoutPlayer(room)

	if connection := room.Connections[cmd.PlayerID]; connection != nil && connection.AI {
		room.AIRunning = false
	}

	switch cmd.Type {
	case "connect":
		err = connect(room, cmd.PlayerID)
	case "disconnect":
		disconnect(room, cmd.PlayerID)
	case "room_update_settings":
		var settings struct {
			Visibility string `json:"visibility"`
		}
		if err = json.Unmarshal(payload, &settings); err == nil {
			room.State.Visibility = settings.Visibility
		}
	case "match_ready":
		err = setReady(room, cmd.PlayerID, payload)
	case "match_begin":
		err = beginMatch(room, cmd.PlayerID)
	case "match_add_ai":
		var request game.AddAIRequest
		if request, err = game.DecodeAddAIRequest(payload); err == nil {
			err = addAI(room, cmd.PlayerID, request)
		}
	case "match_remove_player":
		var target struct {
			PlayerID string `json:"playerID"`
		}
		if unmarshalErr := json.Unmarshal(payload, &target); unmarshalErr != nil {
			err = unmarshalErr
			break
		}
		err = removePlayer(room, cmd.PlayerID, target.PlayerID)
		if err == nil {
			result.Effects = append(result.Effects, game.DeliveryEffect{
				Audience:       game.AudiencePlayer,
				TargetPlayerID: target.PlayerID,
				Reliable:       true,
				CloseAfter:     true,
				Payload:        contracts.ErrorMessage{Type: contracts.MessageTypeError, Code: "REMOVED", Message: "你已被移出房间"},
			})
		}
	case "game_ready":
		err = gameReady(room, commandTime)
	case "game_place_tile":
		rules.HandlePlaceTileMessage(room, legacy)
	case "game_create_company":
		rules.HandleCreateCompanyMessage(room, legacy)
	case "game_merging_selection":
		rules.HandleMergingSelectionMessage(room, legacy)
	case "game_merging_settle":
		rules.HandleMergingSettleMessage(room, legacy)
	case "game_buy_stock":
		rules.HandleBuyStockMessage(room, legacy)
	case "game_end_vote":
		rules.HandleEndVoteMessage(room, legacy)
	case "turn_timeout":
		rules.HandleTurnTimeoutMessage(room, legacy)
	case "game_play_audio":
		var audio struct {
			AudioType string `json:"audioType"`
		}
		if unmarshalErr := json.Unmarshal(payload, &audio); unmarshalErr != nil {
			err = unmarshalErr
			break
		}
		result.Effects = append(result.Effects, game.DeliveryEffect{
			Audience: game.AudienceAll,
			Reliable: false,
			Payload:  map[string]any{"type": "audio", "message": audio.AudioType},
		})
	case "game_restart_game":
		if cmd.PlayerID != room.State.OwnerID {
			err = errors.New("只有房主可以重新开始游戏")
		} else {
			err = errors.New("终局房间不能原地重开，请创建新房间")
		}
	default:
		err = fmt.Errorf("unknown acquire command %q", cmd.Type)
	}
	if err != nil {
		return game.ApplyResult{}, err
	}

	acquireai.AutoSettleDisconnectedHolder(room)
	if room.State.RoomStatus != domain.RoomStatusMatch && room.State.RoomStatus != domain.RoomStatusWaiting {
		_, _ = rules.RecomputeDerivedState(room)
		if rules.ShouldFinalizeEnd(room, acquireCommandCompletedTurn(cmd.Type, previousStatus, previousPlayer, room.State)) {
			room.State.RoomStatus = domain.RoomStatusEnd
		}
	}
	result.Status = platformStatus(room.State.RoomStatus)
	result.Finished = room.State.RoomStatus == domain.RoomStatusEnd
	connectionLifecycle := cmd.Type == "connect" || cmd.Type == "disconnect"
	if !connectionLifecycle ||
		previousStatus != room.State.RoomStatus ||
		previousTimedPlayer != acquireai.CurrentTimeoutPlayer(room) {
		updateAutomation(room, commandTime)
	}
	result.AIRequests = append(result.AIRequests, nextAIRequest(room)...)
	return result, nil
}

func (p *Plugin) BuildPlayerView(ctx context.Context, state game.State, req game.ViewRequest) (any, error) {
	room, err := asRoom(state)
	if err != nil {
		return nil, err
	}
	connection := room.Connections[req.PlayerID]
	if connection == nil {
		return contracts.ErrorMessage{Type: contracts.MessageTypeError, Code: "NOT_IN_ROOM", Message: "玩家不在房间中"}, nil
	}
	players := make(map[string]contracts.RoomPlayer, len(room.Connections))
	for playerID, player := range room.Connections {
		if player == nil {
			continue
		}
		profile := req.PlayerProfile(playerID)
		players[playerID] = contracts.RoomPlayer{
			UserID:      profile.UserID,
			PlayerID:    playerID,
			DisplayName: profile.DisplayName,
			Avatar:      profile.Avatar,
			Online:      player.Online,
			AI:          player.AI,
			Ready:       player.Ready,
			SeatIndex:   player.SeatIndex,
		}
	}
	if room.State.RoomStatus == domain.RoomStatusMatch {
		return contracts.MatchSync{
			Type:        contracts.MessageTypeMatchSync,
			RoomID:      room.ID,
			MatchID:     req.MatchID,
			OwnerID:     room.State.OwnerID,
			Status:      string(room.State.RoomStatus),
			PlayerID:    req.PlayerID,
			Visibility:  req.Visibility,
			MaxPlayers:  req.MaxPlayers,
			Players:     players,
			PlayerOrder: append([]string{}, room.PlayerSeq...),
		}, nil
	}

	result, _ := rules.RecomputeDerivedState(room)
	var finalResult any
	if room.State.RoomStatus == domain.RoomStatusEnd {
		finalResult = result
	}
	var deadline any
	if !room.TurnDeadline.IsZero() {
		deadline = room.TurnDeadline.UTC().Format(time.RFC3339Nano)
	}
	playerViews := make(map[string]any, len(players))
	for playerID, player := range players {
		playerViews[playerID] = player
	}
	return map[string]any{
		"type":       contracts.MessageTypeRoomSync,
		"roomID":     req.RoomID,
		"matchID":    req.MatchID,
		"seq":        req.Seq,
		"result":     finalResult,
		"playerId":   req.PlayerID,
		"playerData": room.State.Players[req.PlayerID],
		"ownerID":    room.State.OwnerID,
		"visibility": req.Visibility,
		"roomData": map[string]any{
			"companyInfo":   room.State.Companies,
			"currentPlayer": room.State.CurrentPlayer,
			"gameStatus":    room.State.RoomStatus,
			"tiles":         room.State.BoardTiles,
			"players":       playerViews,
			"turnDeadline":  deadline,
			"turnTimeoutMs": room.TurnTimeout.Milliseconds(),
			"endGame":       room.State.EndGame,
		},
		"tempData": map[string]any{
			"last_tile_key":               room.State.LastTileKey,
			"merge_main_company_temp":     room.State.MergeMainCompany,
			"merge_selection_temp":        room.State.MergingSelection,
			"mergeSettleData":             room.State.MergeSettleData,
			"merge_settle_current_player": rules.CurrentMergeSettlePlayer(room),
		},
	}, nil
}

func (p *Plugin) Decide(ctx context.Context, req game.AIRequest) (contracts.RoomCommand, error) {
	room, err := asRoom(req.State)
	if err != nil {
		return contracts.RoomCommand{}, err
	}
	commandType, payload, ok := acquireai.DecideCommand(room, req.PlayerID)
	if !ok {
		return contracts.RoomCommand{Type: "turn_timeout", PlayerID: req.PlayerID, Payload: map[string]any{}}, nil
	}
	var decoded any
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &decoded); err != nil {
			return contracts.RoomCommand{}, err
		}
	}
	return contracts.RoomCommand{Type: commandType, PlayerID: req.PlayerID, Payload: decoded}, nil
}

func (p *Plugin) PendingCommands(ctx context.Context, state game.State) ([]game.ScheduledCommand, error) {
	room, err := asRoom(state)
	if err != nil {
		return nil, err
	}
	if playerID := pendingMergeSettleAIHolder(room); playerID != "" && !room.AIRunning {
		commandType, payload, ok := acquireai.DecideCommand(room, playerID)
		if !ok {
			commandType = "turn_timeout"
			payload = []byte("{}")
		}
		var decoded any
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &decoded); err != nil {
				return nil, err
			}
		}
		return []game.ScheduledCommand{{
			Key:       fmt.Sprintf("%s:%s:%d", room.State.RoomStatus, playerID, time.Now().UTC().UnixNano()),
			ExecuteAt: time.Now().UTC().Add(5 * time.Second),
			PlayerID:  playerID,
			Type:      commandType,
			Payload:   decoded,
		}}, nil
	}
	if room.TurnDeadline.IsZero() || room.TurnTimeout <= 0 {
		return nil, nil
	}
	playerID := acquireai.CurrentTimeoutPlayer(room)
	if playerID == "" {
		return nil, nil
	}
	player := room.Connections[playerID]
	if player == nil || player.AI {
		return nil, nil
	}
	command, ok := acquireai.BuildTurnTimeoutCommand(room, playerID)
	if !ok {
		return nil, nil
	}
	var payload any
	if len(command.Payload) > 0 {
		if err := json.Unmarshal(command.Payload, &payload); err != nil {
			return nil, err
		}
	}
	return []game.ScheduledCommand{{
		Key:       fmt.Sprintf("%s:%s:%d", room.State.RoomStatus, playerID, room.TurnDeadline.UnixNano()),
		ExecuteAt: room.TurnDeadline,
		PlayerID:  playerID,
		Type:      command.Type,
		Payload:   payload,
	}}, nil
}

func (p *Plugin) ResetAutomationState(ctx context.Context, state game.State) error {
	room, err := asRoom(state)
	if err != nil {
		return err
	}
	room.AIRunning = false
	return nil
}

func (p *Plugin) IsRecordableCommand(commandType string) bool {
	switch commandType {
	case "game_place_tile", "game_create_company", "game_merging_selection", "game_merging_settle", "game_buy_stock", "game_end_vote", "turn_timeout":
		return true
	default:
		return false
	}
}

func (p *Plugin) ProjectHistory(ctx context.Context, state game.State) (game.HistoryProjection, error) {
	room, err := asRoom(state)
	if err != nil {
		return game.HistoryProjection{}, err
	}
	started := !room.State.GameStartTime.IsZero() &&
		room.State.RoomStatus != domain.RoomStatusMatch &&
		room.State.RoomStatus != domain.RoomStatusWaiting
	if !started {
		return game.HistoryProjection{}, nil
	}
	initialState, err := json.Marshal(room.State)
	if err != nil {
		return game.HistoryProjection{}, err
	}
	result, _ := rules.RecomputeDerivedState(room)
	stateBlob, err := rules.EncodeStateSnapshot(room.State, result)
	if err != nil {
		return game.HistoryProjection{}, err
	}

	type score struct {
		playerID string
		money    int
		stocks   int
		total    int
	}
	scores := make([]score, 0, len(room.PlayerSeq))
	for _, playerID := range room.PlayerSeq {
		player := room.State.Players[playerID]
		if player == nil {
			continue
		}
		stockValue := rules.CalculateTotalValue(player.Stocks, room.State.Companies, room.Log())
		scores = append(scores, score{playerID: playerID, money: player.Money, stocks: stockValue, total: player.Money + stockValue})
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].total != scores[j].total {
			return scores[i].total > scores[j].total
		}
		return scores[i].playerID < scores[j].playerID
	})
	rankByPlayer := map[string]int{}
	previousTotal := 0
	for index, item := range scores {
		if index == 0 || item.total != previousTotal {
			rankByPlayer[item.playerID] = index + 1
			previousTotal = item.total
		} else {
			rankByPlayer[item.playerID] = rankByPlayer[scores[index-1].playerID]
		}
	}
	finished := room.State.RoomStatus == domain.RoomStatusEnd
	winnerPlayerID := ""
	if finished && len(scores) > 0 {
		winnerPlayerID = scores[0].playerID
	}
	players := make([]game.HistoryPlayer, 0, len(scores))
	for index, item := range scores {
		connection := room.Connections[item.playerID]
		finalRank := rankByPlayer[item.playerID]
		finalScore := item.total
		finalMoney := item.money
		players = append(players, game.HistoryPlayer{
			PlayerID:    item.playerID,
			SeatIndex:   acquireSeatIndex(connection, index),
			IsAI:        connection != nil && connection.AI,
			FinalScore:  &finalScore,
			FinalMoney:  &finalMoney,
			FinalStocks: item.stocks,
			FinalRank:   &finalRank,
			IsWinner:    finished && item.playerID == winnerPlayerID,
		})
	}
	placedTiles := 0
	for _, tile := range room.State.BoardTiles {
		if tile != nil && tile.Belong != "" {
			placedTiles++
		}
	}
	return game.HistoryProjection{
		Started:             true,
		Finished:            finished,
		AbandonedEligible:   placedTiles*2 >= 108,
		StartedAt:           room.State.GameStartTime,
		MaxPlayers:          room.State.MaxPlayers,
		ReplaySchemaVersion: ReplaySchemaVersion,
		ReplayFrame:         acquireReplayFrame(room, result),
		InitialState:        initialState,
		StateBlob:           stateBlob,
		FinalResult:         result,
		WinnerPlayerID:      winnerPlayerID,
		Players:             players,
	}, nil
}

func (p *Plugin) BuildReplaySnapshot(ctx context.Context, state game.State, seq, total int, commandType, playerID string, payload any) (any, error) {
	room, err := asRoom(state)
	if err != nil {
		return nil, err
	}
	result, _ := rules.RecomputeDerivedState(room)
	snapshot := acquireReplayFrame(room, result)
	snapshot["seq"] = seq
	snapshot["totalEvents"] = total
	if commandType != "" {
		payloadJSON, _ := json.Marshal(payload)
		snapshot["currentEvent"] = map[string]any{
			"seq": seq, "playerID": playerID, "cmdType": commandType, "payloadJSON": string(payloadJSON),
		}
	}
	return snapshot, nil
}

func acquireReplayFrame(room *domain.Room, result map[string]interface{}) map[string]any {
	players := make(map[string]any, len(room.Connections))
	playersData := make(map[string]any, len(room.State.Players))
	for id, connection := range room.Connections {
		if connection == nil {
			continue
		}
		players[id] = map[string]any{
			"playerID":  id,
			"online":    connection.Online,
			"ai":        connection.AI,
			"ready":     connection.Ready,
			"seatIndex": connection.SeatIndex,
		}
	}
	for id, player := range room.State.Players {
		playersData[id] = player
	}
	return map[string]any{
		"roomData": map[string]any{
			"companyInfo":   room.State.Companies,
			"currentPlayer": room.State.CurrentPlayer,
			"gameStatus":    room.State.RoomStatus,
			"tiles":         room.State.BoardTiles,
			"players":       players,
			"endGame":       room.State.EndGame,
		},
		"playersData": playersData,
		"result":      result,
	}
}

func (p *Plugin) DecodeReplayState(
	ctx context.Context,
	initialState,
	stateSnapshot,
	stateBlob []byte,
	players []game.HistoryPlayer,
) (game.State, error) {
	var state *domain.GameState
	if len(stateBlob) > 0 {
		if decoded, _, ok := rules.DecodeStateSnapshot(stateBlob); ok {
			state = decoded
		}
	}
	if state == nil && len(stateSnapshot) > 0 {
		if decoded, _, ok := rules.DecodeStateSnapshot(stateSnapshot); ok {
			state = decoded
		}
	}
	if state == nil && len(initialState) > 0 {
		state = &domain.GameState{}
		if err := json.Unmarshal(initialState, state); err != nil {
			return nil, err
		}
	}
	if state == nil {
		return nil, errors.New("acquire replay state is unavailable")
	}
	base := roomcore.NewBase("", 1)
	for _, player := range players {
		base.PlayerSeq = append(base.PlayerSeq, player.PlayerID)
		base.Connections[player.PlayerID] = &roomcore.PlayerConn{
			PlayerID:  player.PlayerID,
			Ready:     true,
			AI:        player.IsAI,
			SeatIndex: player.SeatIndex,
		}
	}
	return &domain.Room{Base: base, State: state}, nil
}

func (p *Plugin) BuildSpectatorView(ctx context.Context, state game.State, seq int64) (any, error) {
	return p.BuildReplaySnapshot(ctx, state, int(seq), int(seq), "", "", nil)
}

func connect(room *domain.Room, playerID string) error {
	if playerID == "" {
		return errors.New("playerID is required")
	}
	if player := room.Connections[playerID]; player != nil {
		player.Online = true
		return nil
	}
	if room.State.RoomStatus != domain.RoomStatusMatch {
		return errors.New("游戏已开始，无法加入")
	}
	if len(room.Connections) >= room.State.MaxPlayers {
		return errors.New("房间已满")
	}
	return addAcquirePlayer(room, &roomcore.PlayerConn{
		PlayerID:  playerID,
		Online:    true,
		Ready:     playerID == room.State.OwnerID,
		SeatIndex: firstAvailableAcquireSeat(room),
	})
}

func disconnect(room *domain.Room, playerID string) {
	player := room.Connections[playerID]
	if player == nil {
		return
	}
	if room.State.RoomStatus == domain.RoomStatusMatch {
		delete(room.Connections, playerID)
		room.PlayerSeq = removeString(room.PlayerSeq, playerID)
	} else {
		player.Online = false
	}
	if room.State.OwnerID == playerID {
		for _, candidate := range room.PlayerSeq {
			if next := room.Connections[candidate]; next != nil && next.Online && !next.AI {
				room.State.OwnerID = candidate
				next.Ready = true
				break
			}
		}
	}
}

func setReady(room *domain.Room, playerID string, payload []byte) error {
	player := room.Connections[playerID]
	if player == nil {
		return errors.New("玩家不在房间中")
	}
	var req struct {
		Ready bool `json:"ready"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return err
	}
	player.Ready = req.Ready
	return nil
}

func addAI(room *domain.Room, actor string, request game.AddAIRequest) error {
	if actor != room.State.OwnerID {
		return errors.New("只有房主可以添加 AI")
	}
	if room.State.RoomStatus != domain.RoomStatusMatch {
		return errors.New("游戏已开始，无法添加 AI")
	}
	if len(room.Connections) >= room.State.MaxPlayers {
		return errors.New("房间已满")
	}
	identity, err := game.ResolveAIIdentity(
		request.RestorePlayerID,
		func(playerID string) bool { return room.Connections[playerID] != nil },
		aiNames,
	)
	if err != nil {
		return err
	}
	seatIndex := firstAvailableAcquireSeat(room)
	if request.SeatIndex != nil {
		seatIndex = *request.SeatIndex
	}
	return addAcquirePlayer(room, &roomcore.PlayerConn{
		PlayerID:  identity.PlayerID,
		Online:    true,
		Ready:     true,
		AI:        true,
		SeatIndex: seatIndex,
	})
}

func removePlayer(room *domain.Room, actor, target string) error {
	if actor != room.State.OwnerID {
		return errors.New("只有房主可以移除玩家")
	}
	if room.State.RoomStatus != domain.RoomStatusMatch {
		return errors.New("游戏已开始，无法移除玩家")
	}
	if target == room.State.OwnerID {
		return errors.New("房主不能移除自己")
	}
	if room.Connections[target] == nil {
		return errors.New("玩家不在房间中")
	}
	delete(room.Connections, target)
	room.PlayerSeq = removeString(room.PlayerSeq, target)
	return nil
}

func beginMatch(room *domain.Room, actor string) error {
	if actor != room.State.OwnerID {
		return errors.New("只有房主可以开始游戏")
	}
	if room.State.RoomStatus != domain.RoomStatusMatch {
		return errors.New("not in match")
	}
	if len(room.Connections) < 2 {
		return errors.New("至少需要两名玩家")
	}
	for _, player := range room.Connections {
		if player == nil || !player.Ready {
			return errors.New("不是所有玩家都已准备")
		}
	}
	room.State.MaxPlayers = len(room.Connections)
	room.State.BoardTiles = make(map[string]*domain.Tile, 108)
	for column := 1; column <= 12; column++ {
		for row := 'A'; row <= 'I'; row++ {
			id := fmt.Sprintf("%d%c", column, row)
			room.State.BoardTiles[id] = &domain.Tile{ID: id}
		}
	}
	room.State.Companies = initialCompanies()
	room.State.Players = map[string]*domain.PlayerState{}
	for _, playerID := range room.PlayerSeq {
		if err := data.InitPlayerData(room, playerID); err != nil {
			return err
		}
	}
	room.State.RoomStatus = domain.RoomStatusWaiting
	return nil
}

func gameReady(room *domain.Room, commandTime time.Time) error {
	if room.State.RoomStatus != domain.RoomStatusWaiting {
		return nil
	}
	if len(room.Connections) != room.State.MaxPlayers {
		return errors.New("not all players joined")
	}
	if room.State.CurrentPlayer == "" {
		players := append([]string(nil), room.PlayerSeq...)
		sort.Strings(players)
		if len(players) == 0 {
			return errors.New("no players")
		}
		room.State.CurrentPlayer = players[0]
	}
	room.State.GameStartTime = commandTime
	room.State.RoomStatus = domain.RoomStatusSetTile
	updateAutomation(room, commandTime)
	return nil
}

func nextAIRequest(room *domain.Room) []game.AIRequest {
	playerID := nextAIPlayer(room)
	player := room.Connections[playerID]
	if player == nil || !player.AI || room.AIRunning || room.State.RoomStatus == domain.RoomStatusEnd {
		return nil
	}
	room.AIRunning = true
	return []game.AIRequest{{
		RoomID:   room.ID,
		GameType: GameType,
		PlayerID: playerID,
		State:    acquireai.CloneRoom(room),
		Delay:    5 * time.Second,
		Timeout:  5 * time.Second,
		FallbackCommand: &contracts.RoomCommand{
			Type:     "turn_timeout",
			PlayerID: playerID,
			Payload:  map[string]any{},
		},
	}}
}

func nextAIPlayer(room *domain.Room) string {
	if playerID := pendingMergeSettleAIHolder(room); playerID != "" {
		return playerID
	}
	return acquireai.CurrentTimeoutPlayer(room)
}

func pendingMergeSettleAIHolder(room *domain.Room) string {
	playerID := rules.CurrentMergeSettlePlayer(room)
	if playerID == "" {
		return ""
	}
	player := room.Connections[playerID]
	if player != nil && player.AI {
		return playerID
	}
	return ""
}

func updateAutomation(room *domain.Room, commandTime time.Time) {
	room.TurnDeadline = time.Time{}
	room.TurnTimeout = 0
	if room.State.RoomStatus == domain.RoomStatusEnd {
		return
	}
	playerID := acquireai.CurrentTimeoutPlayer(room)
	player := room.Connections[playerID]
	if player == nil || player.AI {
		return
	}
	timeout, ok := timeoutByStatus[room.State.RoomStatus]
	if !ok {
		return
	}
	room.TurnTimeout = timeout
	room.TurnDeadline = commandTime.Add(timeout)
}

func initialCompanies() map[string]*domain.CompanyState {
	companies := map[string]*domain.CompanyState{}
	for _, name := range []string{"Sackson", "Tower", "American", "Festival", "Worldwide", "Continental", "Imperial"} {
		companies[name] = &domain.CompanyState{Name: name, StockTotal: 25, StockPrice: 200}
	}
	return companies
}

func platformStatus(status domain.RoomStatus) string {
	switch status {
	case domain.RoomStatusMatch:
		return contracts.RoomStatusMatch
	default:
		return contracts.RoomStatusPlaying
	}
}

func asRoom(state game.State) (*domain.Room, error) {
	room, ok := state.(*domain.Room)
	if !ok || room == nil || room.Base == nil || room.State == nil {
		return nil, fmt.Errorf("unexpected acquire state %T", state)
	}
	return room, nil
}

func removeString(values []string, target string) []string {
	for index, value := range values {
		if value == target {
			return append(values[:index], values[index+1:]...)
		}
	}
	return values
}
