package rules

import (
	"encoding/json"
	"testing"

	"github.com/nciyuan9264/game-platform/internal/splendor/domain"
	"github.com/nciyuan9264/game-platform/internal/splendor/entities"
)

func TestCardIDZeroIsValidForBuyAndReserve(t *testing.T) {
	newRoom := func() *domain.Room {
		return domain.NewRoom(&domain.GameState{
			RoomStatus:    domain.RoomStatusPlaying,
			CurrentPlayer: "player",
			Players: map[string]*domain.PlayerState{
				"player": {
					Gem:         map[string]int{"Red": 1},
					ReserveCard: []entities.NormalCard{},
				},
			},
			NormalCards: map[string]*entities.NormalCard{
				"0": {
					ID: 0, State: entities.CardStateRevealed,
					Cost: map[string]int{"Red": 1},
				},
			},
			Gems: map[string]int{"Gold": 5},
		})
	}

	for _, test := range []struct {
		name    string
		command domain.Command
	}{
		{
			name: "buy",
			command: domain.Command{
				Type: "game_buy_card", PlayerID: "player", Payload: json.RawMessage(`0`),
			},
		},
		{
			name: "legacy reserve",
			command: domain.Command{
				Type: "game_preserve_card", PlayerID: "player", Payload: json.RawMessage(`0`),
			},
		},
		{
			name: "structured reserve",
			command: domain.Command{
				Type: "game_preserve_card", PlayerID: "player",
				Payload: json.RawMessage(`{"cardID":0}`),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateAction(newRoom(), test.command); err != nil {
				t.Fatalf("cardID=0 rejected: code=%s error=%v", err.Code, err)
			}
		})
	}
}

func TestBuyCardPayloadValidation(t *testing.T) {
	tests := []struct {
		payload string
		valid   bool
	}{
		{payload: `0`, valid: true},
		{payload: `42`, valid: true},
		{payload: `null`},
		{payload: `-1`},
		{payload: `"0"`},
		{payload: `1.5`},
		{payload: `{}`},
		{payload: ``},
	}
	for _, test := range tests {
		t.Run(test.payload, func(t *testing.T) {
			_, err := decodeCardID(json.RawMessage(test.payload))
			if (err == nil) != test.valid {
				t.Fatalf("decodeCardID(%q) error = %v, valid=%v", test.payload, err, test.valid)
			}
		})
	}
}

func TestReserveCardPayloadValidation(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		valid   bool
		cardID  int
	}{
		{name: "legacy zero", payload: `0`, valid: true},
		{name: "legacy positive", payload: `42`, valid: true, cardID: 42},
		{name: "structured zero", payload: `{"cardID":0}`, valid: true},
		{
			name:    "structured positive with returns",
			payload: `{"cardID":42,"returns":{"Red":1}}`,
			valid:   true,
			cardID:  42,
		},
		{name: "missing card ID", payload: `{}`},
		{name: "null card ID", payload: `{"cardID":null}`},
		{name: "negative card ID", payload: `{"cardID":-1}`},
		{name: "wrong card ID type", payload: `{"cardID":"0"}`},
		{name: "unknown field", payload: `{"cardID":0,"extra":true}`},
		{name: "legacy null", payload: `null`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := parseReserveCardPayload(json.RawMessage(test.payload))
			if (err == nil) != test.valid {
				t.Fatalf("parseReserveCardPayload(%s) error = %v, valid=%v", test.payload, err, test.valid)
			}
			if err == nil && payload.CardID != test.cardID {
				t.Fatalf("card ID = %d, want %d", payload.CardID, test.cardID)
			}
		})
	}
}
