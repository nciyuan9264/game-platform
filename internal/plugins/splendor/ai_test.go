package splendor

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/plugins/splendor/domain"
	"github.com/nciyuan9264/game-platform/internal/plugins/splendor/entities"
	"github.com/nciyuan9264/game-platform/internal/plugins/splendor/rules"
)

func TestSplendorAIStrategy(t *testing.T) {
	tests := []struct {
		name   string
		state  *domain.GameState
		assert func(*testing.T, map[string]any)
	}{
		{
			name: "buys immediate winning card",
			state: aiTestState(
				&domain.PlayerState{Score: 14, Gem: map[string]int{"Red": 1}},
				entities.NormalCard{
					ID: 0, Level: 1, Bonus: "Black", Points: 1,
					Cost: map[string]int{"Red": 1},
				},
				entities.NormalCard{
					ID: 1, Level: 3, Bonus: "Blue", Points: 5,
					Cost: map[string]int{"Blue": 7},
				},
			),
			assert: assertAICommand("game_buy_card", 0),
		},
		{
			name: "buys affordable reserved card",
			state: func() *domain.GameState {
				state := aiTestState(
					&domain.PlayerState{
						Gem: map[string]int{"Red": 1},
						ReserveCard: []entities.NormalCard{{
							ID: 12, Level: 1, Bonus: "Blue",
							Cost: map[string]int{"Red": 1},
						}},
					},
					entities.NormalCard{
						ID: 70, Level: 3, Bonus: "Black", Points: 3,
						Cost: map[string]int{"Blue": 7},
					},
				)
				return state
			}(),
			assert: assertAICommand("game_buy_card", 12),
		},
		{
			name: "prefers purchase that earns noble",
			state: func() *domain.GameState {
				player := &domain.PlayerState{
					Gem: map[string]int{"Red": 1, "Blue": 1},
					NormalCard: []entities.NormalCard{
						{ID: 20, Bonus: "Red"},
						{ID: 21, Bonus: "Red"},
						{ID: 22, Bonus: "Red"},
					},
				}
				state := aiTestState(
					player,
					entities.NormalCard{
						ID: 30, Level: 1, Bonus: "Red", Points: 1,
						Cost: map[string]int{"Red": 1},
					},
					entities.NormalCard{
						ID: 31, Level: 1, Bonus: "Blue", Points: 2,
						Cost: map[string]int{"Blue": 1},
					},
				)
				state.NobleCards["N1"] = &entities.NobleCard{
					ID: "N1", Points: 3, State: entities.CardStateRevealed,
					Cost: map[string]int{"Red": 4},
				}
				return state
			}(),
			assert: assertAICommand("game_buy_card", 30),
		},
		{
			name: "takes two useful gems",
			state: aiTestState(
				&domain.PlayerState{Gem: map[string]int{}},
				entities.NormalCard{
					ID: 71, Level: 3, Bonus: "Black", Points: 4,
					Cost: map[string]int{"Red": 3},
				},
			),
			assert: func(t *testing.T, command map[string]any) {
				t.Helper()
				if command["type"] != "game_get_gem" {
					t.Fatalf("command = %+v", command)
				}
				payload := command["payload"].(map[string]any)
				take := payload["take"].(map[string]int)
				if !reflect.DeepEqual(take, map[string]int{"Red": 2}) {
					t.Fatalf("take = %+v, want two Red", take)
				}
			},
		},
		{
			name: "returns low value gems at token limit",
			state: aiTestState(
				&domain.PlayerState{Gem: map[string]int{"Red": 8, "Blue": 2}},
				entities.NormalCard{
					ID: 73, Level: 3, Bonus: "Black", Points: 5,
					Cost: map[string]int{"Red": 11},
				},
			),
			assert: func(t *testing.T, command map[string]any) {
				t.Helper()
				if command["type"] != "game_get_gem" {
					t.Fatalf("command = %+v", command)
				}
				payload := command["payload"].(map[string]any)
				take := payload["take"].(map[string]int)
				returns := payload["returns"].(map[string]int)
				if !reflect.DeepEqual(take, map[string]int{"Red": 2}) ||
					!reflect.DeepEqual(returns, map[string]int{"Blue": 2}) {
					t.Fatalf("payload = %+v", payload)
				}
			},
		},
		{
			name: "reserves opponent winning card",
			state: func() *domain.GameState {
				state := aiTestState(
					&domain.PlayerState{Gem: map[string]int{}},
					entities.NormalCard{
						ID: 50, Level: 2, Bonus: "Blue", Points: 2,
						Cost: map[string]int{"Blue": 2},
					},
				)
				state.Players["opponent"] = &domain.PlayerState{
					Score: 13,
					Gem:   map[string]int{"Blue": 2},
				}
				state.Roster["opponent"] = &domain.Player{PlayerID: "opponent"}
				state.PlayerOrder = append(state.PlayerOrder, "opponent")
				return state
			}(),
			assert: assertAICommand("game_preserve_card", 50),
		},
		{
			name: "passes only without another action",
			state: func() *domain.GameState {
				state := aiTestState(&domain.PlayerState{Gem: map[string]int{}})
				for _, color := range orderedGemColors {
					state.Gems[color] = 0
				}
				return state
			}(),
			assert: func(t *testing.T, command map[string]any) {
				t.Helper()
				if command["type"] != "game_get_gem" {
					t.Fatalf("command = %+v", command)
				}
				payload := command["payload"].(map[string]any)
				if len(payload["take"].(map[string]int)) != 0 {
					t.Fatalf("pass payload = %+v", payload)
				}
			},
		},
	}

	plugin := &Plugin{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, err := plugin.Decide(context.Background(), game.AIRequest{
				PlayerID: "ai",
				State:    test.state,
			})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(command.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if actionErr := rules.ValidateAction(domain.NewRoom(test.state), domain.Command{
				Type: command.Type, PlayerID: command.PlayerID, Payload: payload,
			}); actionErr != nil {
				t.Fatalf("AI returned illegal command %+v: %v", command, actionErr)
			}
			test.assert(t, map[string]any{
				"type":    command.Type,
				"payload": command.Payload,
			})
		})
	}
}

func TestSplendorAIDecisionIsDeterministic(t *testing.T) {
	state := aiTestState(
		&domain.PlayerState{Gem: map[string]int{"White": 1}},
		entities.NormalCard{
			ID: 44, Level: 2, Bonus: "Black", Points: 2,
			Cost: map[string]int{"White": 5},
		},
		entities.NormalCard{
			ID: 68, Level: 2, Bonus: "Red", Points: 2,
			Cost: map[string]int{"Black": 5},
		},
	)
	plugin := &Plugin{}
	first, err := plugin.Decide(context.Background(), game.AIRequest{
		PlayerID: "ai",
		State:    state,
	})
	if err != nil {
		t.Fatal(err)
	}
	for iteration := 0; iteration < 20; iteration++ {
		next, err := plugin.Decide(context.Background(), game.AIRequest{
			PlayerID: "ai",
			State:    state,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(next, first) {
			t.Fatalf("decision %d = %+v, want %+v", iteration, next, first)
		}
	}
}

func aiTestState(player *domain.PlayerState, cards ...entities.NormalCard) *domain.GameState {
	if player.NormalCard == nil {
		player.NormalCard = []entities.NormalCard{}
	}
	if player.NobleCard == nil {
		player.NobleCard = []entities.NobleCard{}
	}
	if player.Gem == nil {
		player.Gem = map[string]int{}
	}
	if player.ReserveCard == nil {
		player.ReserveCard = []entities.NormalCard{}
	}
	state := &domain.GameState{
		RoomID:        "ai-test",
		RoomStatus:    domain.RoomStatusPlaying,
		CurrentPlayer: "ai",
		FirstPlayer:   "ai",
		MaxPlayers:    2,
		Roster: map[string]*domain.Player{
			"ai": {PlayerID: "ai", AI: true},
		},
		Players:     map[string]*domain.PlayerState{"ai": player},
		PlayerOrder: []string{"ai"},
		NormalCards: map[string]*entities.NormalCard{},
		NobleCards:  map[string]*entities.NobleCard{},
		Gems: map[string]int{
			"White": 4, "Blue": 4, "Green": 4,
			"Red": 4, "Black": 4, "Gold": 5,
		},
	}
	for index := range cards {
		card := cloneNormalCard(cards[index])
		card.State = entities.CardStateRevealed
		state.NormalCards[fmtInt(card.ID)] = &card
	}
	return state
}

func assertAICommand(commandType string, cardID int) func(*testing.T, map[string]any) {
	return func(t *testing.T, command map[string]any) {
		t.Helper()
		if command["type"] != commandType {
			t.Fatalf("command = %+v", command)
		}
		switch payload := command["payload"].(type) {
		case int:
			if payload != cardID {
				t.Fatalf("card ID = %d, want %d", payload, cardID)
			}
		case map[string]any:
			if payload["cardID"] != cardID {
				t.Fatalf("card ID = %v, want %d", payload["cardID"], cardID)
			}
		default:
			t.Fatalf("payload = %#v", command["payload"])
		}
	}
}

func fmtInt(value int) string {
	return fmt.Sprintf("%d", value)
}
