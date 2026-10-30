package warchest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

const (
	GameType            = "warchest"
	EngineVersion       = "warchest-v3"
	ReplaySchemaVersion = 1
	defaultMaxPlayers   = 2
)

var aiNames = []string{"渡鸦"}

const (
	CommandSetSetupMode = "game_set_setup_mode"
	CommandDraftUnit    = "game_draft_unit"
	CommandPlayAction   = "game_play_action"
	CommandResign       = "game_resign"
	CommandTimeout      = "turn_timeout"
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
		return nil, errors.New("warchest requires exactly two seats")
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
		RoomID:             cfg.RoomID,
		OwnerID:            cfg.OwnerPlayerID,
		Status:             StatusMatch,
		MaxPlayers:         maxPlayers,
		Visibility:         visibility,
		TurnTimeoutSeconds: defaultTurnTimeout,
		RandomSeed:         cfg.RandomSeed,
		MatchRoundNo:       roundNo,
		SetupMode:          SetupRecommended,
		Roster:             map[string]*Player{},
		PlayerOrder:        []string{},
		Coins:              map[string]UnitType{},
		CoinOwners:         map[string]string{},
		Board:              map[string]*UnitStack{},
		Controls:           map[string]Side{},
		Log:                []ActionEntry{},
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
	if err := validateCoinConservation(typed); err != nil {
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
	if err := validateCoinConservation(&state); err != nil {
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
	previousState, err := asState(previous)
	if err != nil {
		return err
	}
	nextState, err := asState(next)
	if err != nil {
		return err
	}
	nextState.SetupMode = previousState.SetupMode
	nextState.Draft = nil
	return nil
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
		TurnTimeoutSeconds: defaultTurnTimeout,
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
	current := maxInt(
		controlledLocations(typed, SideOak),
		controlledLocations(typed, SideFalcon),
	)
	return game.RoomProgress{Current: current, Total: 6}, nil
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
	now := command.SentAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	removedPlayerID := ""
	automatic := false
	if player := typed.Roster[command.PlayerID]; player != nil {
		automatic = player.AI
	}
	switch command.Type {
	case "connect":
		err = connect(typed, command.PlayerID, now)
	case "disconnect":
		if player := typed.Roster[command.PlayerID]; player != nil {
			player.Online = false
		}
	case "match_ready":
		err = setReady(typed, command.PlayerID, payload)
	case "match_add_ai":
		err = addAI(typed, command.PlayerID, payload, now)
	case "match_remove_player":
		removedPlayerID, err = removePlayer(
			typed,
			command.PlayerID,
			payload,
			now,
		)
	case "room_update_settings":
		err = updateRoomSettings(typed, command.PlayerID, payload)
	case CommandSetSetupMode:
		err = setSetupMode(typed, command.PlayerID, payload, now)
	case CommandDraftUnit:
		err = draftUnit(typed, command.PlayerID, payload, now)
	case "match_begin":
		err = beginMatch(typed, command.PlayerID)
	case "game_ready":
		err = gameReady(typed, command.PlayerID, now)
	case CommandPlayAction:
		err = playAction(
			typed,
			command.PlayerID,
			payload,
			automatic,
			now,
		)
	case CommandResign:
		err = resign(typed, command.PlayerID)
	case CommandTimeout:
		if command.ConnectionRef.Valid() {
			err = errors.New("client cannot submit timeout commands")
		} else {
			err = p.applyTimeout(ctx, typed, command.PlayerID, now)
		}
	case "game_restart_game":
		err = errors.New("finished rooms must start a new match")
	default:
		err = fmt.Errorf("unknown warchest command %q", command.Type)
	}
	if err != nil {
		return game.ApplyResult{}, err
	}
	if err := validateCoinConservation(typed); err != nil {
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
	if typed.Status == StatusMatch &&
		typed.Draft != nil &&
		!typed.Draft.Complete &&
		!typed.Draft.TurnDeadline.IsZero() {
		player := typed.Roster[typed.Draft.CurrentPickerID]
		if player != nil && !player.AI {
			return []game.ScheduledCommand{{
				Key: fmt.Sprintf(
					"draft:%s:%d",
					player.PlayerID,
					typed.Draft.TurnDeadline.UnixNano(),
				),
				ExecuteAt: typed.Draft.TurnDeadline,
				PlayerID:  player.PlayerID,
				Type:      CommandTimeout,
			}}, nil
		}
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

func deadlineString(value time.Time) *string {
	if value.IsZero() {
		return nil
	}
	result := value.UTC().Format(time.RFC3339Nano)
	return &result
}
