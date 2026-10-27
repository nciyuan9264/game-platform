package onitama

import (
	"encoding/json"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

func TestOpeningMovesAllowJumpingButRejectFriendlyLanding(t *testing.T) {
	state := ruleTestState()
	moves := legalMoves(state, SideBlue)
	if !containsMove(moves, "tiger", "blue-master", Cell{2, 2}) {
		t.Fatalf("opening moves = %+v", moves)
	}
	if containsMove(moves, "crab", "blue-master", Cell{4, 0}) {
		t.Fatal("friendly occupied landing was legal")
	}
	state.Pieces["red-student-1"].Cell = Cell{3, 2}
	if !containsMove(
		legalMoves(state, SideBlue),
		"tiger",
		"blue-master",
		Cell{2, 2},
	) {
		t.Fatal("intermediate piece incorrectly blocked a move")
	}
}

func TestCaptureAndBothWinningPathsDoNotExchangeCards(t *testing.T) {
	stone := ruleTestState()
	stone.Pieces["blue-master"].Cell = Cell{2, 2}
	stone.Pieces["red-master"].Cell = Cell{0, 2}
	transit := stone.TransitCardID
	if err := movePiece(
		stone,
		"blue",
		"tiger",
		"blue-master",
		Cell{0, 2},
		false,
	); err != nil {
		t.Fatal(err)
	}
	if stone.Status != StatusEnd ||
		stone.EndReason != "stone" ||
		stone.TransitCardID != transit ||
		stone.LastAction.ReceivedCardID != "" ||
		stone.LastAction.CapturedKind != PieceMaster {
		t.Fatalf("stone result = %+v", stone)
	}

	stream := ruleTestState()
	stream.Hands[SideBlue] = []string{"crane", "crab"}
	stream.Pieces["blue-master"].Cell = Cell{1, 2}
	stream.Pieces["red-master"].Cell = Cell{0, 4}
	transit = stream.TransitCardID
	if err := movePiece(
		stream,
		"blue",
		"crane",
		"blue-master",
		Cell{0, 2},
		false,
	); err != nil {
		t.Fatal(err)
	}
	if stream.Status != StatusEnd ||
		stream.EndReason != "stream" ||
		stream.TransitCardID != transit ||
		stream.LastAction.ReceivedCardID != "" {
		t.Fatalf("stream result = %+v", stream)
	}
}

func TestMoveExchangesCardAndPassRequiresNoMoves(t *testing.T) {
	state := ruleTestState()
	received := state.TransitCardID
	if err := movePiece(
		state,
		"blue",
		"tiger",
		"blue-master",
		Cell{2, 2},
		false,
	); err != nil {
		t.Fatal(err)
	}
	if state.TransitCardID != "tiger" ||
		state.Hands[SideBlue][0] != received ||
		state.CurrentPlayer != "red" ||
		state.LastAction.ReceivedCardID != received {
		t.Fatalf("exchange = %+v", state)
	}
	if err := passTurn(
		ruleTestState(),
		"blue",
		"tiger",
		false,
	); err == nil {
		t.Fatal("pass accepted while a move existed")
	}

	blocked := ruleTestState()
	for id, piece := range blocked.Pieces {
		if piece.Side == SideBlue {
			delete(blocked.Pieces, id)
		}
	}
	received = blocked.TransitCardID
	if err := passTurn(
		blocked,
		"blue",
		blocked.Hands[SideBlue][0],
		true,
	); err != nil {
		t.Fatal(err)
	}
	if blocked.CurrentPlayer != "red" ||
		blocked.LastAction.Kind != "pass" ||
		!blocked.LastAction.Automatic ||
		blocked.LastAction.ReceivedCardID != received {
		t.Fatalf("pass result = %+v", blocked)
	}
}

func TestInvalidMoveDoesNotMutateRuleState(t *testing.T) {
	state := ruleTestState()
	before, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		cardID string
		piece  string
		to     Cell
	}{
		{"cobra", "blue-master", Cell{3, 2}},
		{"tiger", "red-master", Cell{2, 2}},
		{"tiger", "blue-master", Cell{-1, 2}},
		{"tiger", "blue-master", Cell{4, 0}},
	} {
		if err := movePiece(
			state,
			"blue",
			test.cardID,
			test.piece,
			test.to,
			false,
		); err == nil {
			t.Fatalf("move accepted: %+v", test)
		}
		after, marshalErr := json.Marshal(state)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("state changed:\n%s\n%s", before, after)
		}
	}
}

func TestRandomLegalPlayPreservesCardAndPieceInvariants(t *testing.T) {
	random := rand.New(rand.NewSource(9264))
	for gameIndex := 0; gameIndex < 20; gameIndex++ {
		state := ruleTestState()
		for turn := 0; turn < 120 && state.Status == StatusPlaying; turn++ {
			side := currentSide(state)
			actions := allCandidates(state, side)
			if len(actions) == 0 {
				t.Fatal("active player has no move or pass")
			}
			action := actions[random.Intn(len(actions))]
			if !applyCandidate(state, side, action, false) {
				t.Fatalf("failed legal action %+v", action)
			}
			if err := validateState(state); err != nil {
				t.Fatalf("turn %d: %v\n%+v", turn, err, state)
			}
		}
	}
}

func ruleTestState() *State {
	return &State{
		RoomID:        "onitama-rules",
		OwnerID:       "blue",
		Status:        StatusPlaying,
		MaxPlayers:    2,
		Visibility:    "public",
		RandomSeed:    9264,
		RoundNo:       1,
		PlayerOrder:   []string{"blue", "red"},
		BluePlayerID:  "blue",
		RedPlayerID:   "red",
		FirstPlayerID: "blue",
		CurrentPlayer: "blue",
		Roster: map[string]*Player{
			"blue": {
				UserID:    "blue",
				PlayerID:  "blue",
				Online:    true,
				Ready:     true,
				GameReady: true,
				SeatIndex: 0,
				Side:      SideBlue,
			},
			"red": {
				UserID:    "red",
				PlayerID:  "red",
				Online:    true,
				Ready:     true,
				GameReady: true,
				SeatIndex: 1,
				Side:      SideRed,
			},
		},
		Pieces: freshPieces(),
		Hands: map[Side][]string{
			SideBlue: {"tiger", "crab"},
			SideRed:  {"dragon", "elephant"},
		},
		DrawnCardIDs: []string{
			"tiger",
			"crab",
			"dragon",
			"elephant",
			"crane",
		},
		TransitCardID: "crane",
		GameStartTime: time.Date(
			2026,
			10,
			27,
			8,
			0,
			0,
			0,
			time.UTC,
		),
		Log: []Action{},
	}
}

func containsMove(
	values []LegalMove,
	cardID,
	pieceID string,
	to Cell,
) bool {
	for _, move := range values {
		if move.CardID == cardID &&
			move.PieceID == pieceID &&
			move.To == to {
			return true
		}
	}
	return false
}
