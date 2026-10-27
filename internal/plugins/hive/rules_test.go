package hive

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestBasePiecesAndOpeningRestrictions(t *testing.T) {
	if len(pieces) != 22 || len(piecesByID) != 22 {
		t.Fatalf("pieces = %d/%d", len(pieces), len(piecesByID))
	}
	state := ruleState()
	if options := placementOptions(state, "wQ1"); len(options) != 0 {
		t.Fatalf("queen opening options = %+v", options)
	}
	options := placementOptions(state, "wA1")
	if len(options) != 1 || options[0].To != (Hex{}) {
		t.Fatalf("opening options = %+v", options)
	}
	if err := placePiece(state, "white", "wA1", Hex{}, false); err != nil {
		t.Fatal(err)
	}
	if err := placePiece(
		state,
		"black",
		"bA1",
		Hex{Q: 1},
		false,
	); err != nil {
		t.Fatal(err)
	}
	if reason := placementReason(
		state,
		"wQ1",
		&Hex{Q: 0, R: 1},
	); reason == "" {
		t.Fatal("white placement touched black after the opening")
	}
	if reason := placementReason(
		state,
		"wQ1",
		&Hex{Q: -1, R: 0},
	); reason != "" {
		t.Fatalf("white edge placement rejected: %s", reason)
	}
}

func TestFourthTurnForcesQueenAndBlocksMovementBeforeQueen(t *testing.T) {
	state := ruleState()
	mustPlace(t, state, "white", "wA1", Hex{})
	mustPlace(t, state, "black", "bA1", Hex{Q: 1})
	mustPlace(t, state, "white", "wB1", Hex{Q: -1})
	mustPlace(t, state, "black", "bB1", Hex{Q: 2})
	mustPlace(t, state, "white", "wG1", Hex{Q: -2})
	mustPlace(t, state, "black", "bG1", Hex{Q: 3})
	if !queenDue(state, SideWhite) {
		t.Fatal("white queen is not due")
	}
	actions := legalActions(state)
	if len(actions) == 0 {
		t.Fatal("queen has no legal fourth-turn placement")
	}
	for _, action := range actions {
		if action.Kind != "place" || action.PieceID != "wQ1" {
			t.Fatalf("non-queen fourth-turn action = %+v", action)
		}
	}
	if err := movePiece(
		state,
		"white",
		"wA1",
		Hex{Q: 0, R: 1},
		false,
	); err == nil {
		t.Fatal("piece moved before queen was placed")
	}
}

func TestOneHiveGroundSlideAndCreatureMovement(t *testing.T) {
	bridge := seededState([]seedPiece{
		{"wA1", -1, 0},
		{"wQ1", 0, 0},
		{"bQ1", 1, 0},
		{"bA1", 2, 0},
	}, SideBlack)
	if moveBlock(bridge, "bQ1") == "" {
		t.Fatal("bridge queen was movable")
	}
	if len(moveOptions(bridge, "bA1")) <= 2 {
		t.Fatal("edge ant has too few moves")
	}
	if groundSlide(
		map[string][]string{
			"0,1":  {"wQ1"},
			"1,-1": {"bQ1"},
		},
		Hex{},
		Hex{Q: 1},
	) {
		t.Fatal("ground piece crossed a closed gate")
	}
	if groundSlide(map[string][]string{}, Hex{}, Hex{Q: 1}) {
		t.Fatal("ground piece moved without hive contact")
	}
	if !groundSlide(
		map[string][]string{"0,1": {"wQ1"}},
		Hex{},
		Hex{Q: 1},
	) {
		t.Fatal("ground piece failed to slide with one edge contact")
	}

	hopper := seededState([]seedPiece{
		{"wG1", -2, 0},
		{"wQ1", -1, 0},
		{"bQ1", 0, 0},
		{"bA1", 1, 0},
	}, SideWhite)
	option, ok := candidateAt(moveOptions(hopper, "wG1"), Hex{Q: 2})
	if !ok || len(option.Path) != 5 {
		t.Fatalf("grasshopper option = %+v/%v", option, ok)
	}

	spider := seededState([]seedPiece{
		{"wS1", -1, 0},
		{"wQ1", 0, 0},
		{"bQ1", 1, 0},
		{"bA1", 1, -1},
	}, SideWhite)
	options := moveOptions(spider, "wS1")
	if len(options) == 0 {
		t.Fatal("spider has no moves")
	}
	for _, option := range options {
		seen := map[Hex]bool{}
		for _, step := range option.Path {
			seen[step] = true
		}
		if len(option.Path) != 4 || len(seen) != 4 {
			t.Fatalf("invalid spider path = %+v", option.Path)
		}
	}
}

func TestBeetleStackAndGateHeight(t *testing.T) {
	state := seededState([]seedPiece{
		{"wQ1", -1, 1},
		{"wB1", -1, 0},
		{"bQ1", 0, 0},
		{"bA1", 1, 0},
	}, SideWhite)
	if _, ok := candidateAt(moveOptions(state, "wB1"), Hex{}); !ok {
		t.Fatal("beetle cannot climb onto the queen")
	}
	if err := movePiece(state, "white", "wB1", Hex{}, false); err != nil {
		t.Fatal(err)
	}
	if stack := state.Board["0,0"]; !reflect.DeepEqual(
		stack,
		[]string{"bQ1", "wB1"},
	) {
		t.Fatalf("stack = %+v", stack)
	}
	if moveBlock(state, "bQ1") == "" {
		t.Fatal("covered queen can move")
	}
	if reason := placementReason(
		state,
		"bA2",
		&Hex{Q: 0, R: -1},
	); reason == "" {
		t.Fatal("placement ignored top beetle color")
	}
	if !beetleStep(
		map[string][]string{
			"1,0":  {"wA1"},
			"0,1":  {"wQ1"},
			"1,-1": {"bQ1"},
		},
		Hex{},
		Hex{Q: 1},
		1,
	) {
		t.Fatal("beetle was blocked below travel height")
	}
	if beetleStep(
		map[string][]string{
			"1,0":  {"wA1"},
			"0,1":  {"wQ1", "wB1"},
			"1,-1": {"bQ1", "bB1"},
		},
		Hex{},
		Hex{Q: 1},
		1,
	) {
		t.Fatal("beetle crossed an equal-height closed gate")
	}
}

func TestSurroundOutcomeDrawAndForcedPass(t *testing.T) {
	finish := seededState([]seedPiece{
		{"bQ1", 0, 0},
		{"wQ1", -1, 0},
		{"wA1", 1, 0},
		{"bA1", 0, 1},
		{"bB1", -1, 1},
		{"bS1", 0, -1},
		{"wG1", -2, 2},
	}, SideWhite)
	if err := movePiece(
		finish,
		"white",
		"wG1",
		Hex{Q: 1, R: -1},
		false,
	); err != nil {
		t.Fatal(err)
	}
	if finish.Status != StatusEnd ||
		finish.WinnerSide != SideWhite ||
		finish.Draw {
		t.Fatalf("finish = %+v", finish)
	}

	draw := seededState([]seedPiece{
		{"wQ1", 0, 0},
		{"bQ1", 1, 0},
		{"wA1", -1, 1},
		{"bA1", -1, 0},
		{"wB1", 0, -1},
		{"bA2", 1, -1},
		{"bB1", 2, 0},
		{"wS1", 1, 1},
		{"bG1", 2, -1},
		{"wG1", 0, -2},
	}, SideWhite)
	if err := movePiece(
		draw,
		"white",
		"wG1",
		Hex{Q: 0, R: 1},
		false,
	); err != nil {
		t.Fatal(err)
	}
	if draw.Status != StatusEnd || !draw.Draw ||
		draw.WinnerID != "" {
		t.Fatalf("draw = %+v", draw)
	}

	pass := passState()
	if len(legalActions(pass)) != 0 {
		t.Fatal("pass state has legal actions")
	}
	if err := forcePass(pass, "white"); err != nil {
		t.Fatal(err)
	}
	if pass.CurrentSide != SideBlack ||
		pass.ConsecutivePasses != 1 ||
		pass.LastAction.Kind != "pass" {
		t.Fatalf("first pass = %+v", pass)
	}
	pass.CurrentSide = SideWhite
	pass.CurrentPlayer = "white"
	pass.ConsecutivePasses = 1
	if err := forcePass(pass, "white"); err != nil {
		t.Fatal(err)
	}
	if pass.Status != StatusEnd || !pass.Draw {
		t.Fatalf("double pass = %+v", pass)
	}
}

func TestBasicAIProducesDeterministicLegalPlay(t *testing.T) {
	left := ruleState()
	right := cloneState(left)
	for step := 0; step < 24 && left.Status == StatusPlaying; step++ {
		leftOption, ok := bestAction(context.Background(), left)
		if !ok || !applyCandidate(left, leftOption, true) {
			t.Fatalf("left AI failed at step %d", step)
		}
		rightOption, rightOK := bestAction(context.Background(), right)
		if !rightOK || !reflect.DeepEqual(leftOption, rightOption) ||
			!applyCandidate(right, rightOption, true) {
			t.Fatalf("AI is not deterministic at step %d", step)
		}
		if err := validateState(left); err != nil {
			t.Fatalf("left invalid at step %d: %v", step, err)
		}
	}
}

type seedPiece struct {
	id string
	q  int
	r  int
}

func ruleState() *State {
	start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	return &State{
		RoomID:        "hive-rules",
		OwnerID:       "white",
		Status:        StatusPlaying,
		MaxPlayers:    2,
		Visibility:    "public",
		RandomSeed:    40,
		RoundNo:       1,
		Roster:        testRoster(),
		PlayerOrder:   []string{"white", "black"},
		Board:         map[string][]string{},
		Turns:         map[Side]int{SideWhite: 0, SideBlack: 0},
		CurrentSide:   SideWhite,
		CurrentPlayer: "white",
		WhitePlayerID: "white",
		BlackPlayerID: "black",
		FirstPlayerID: "white",
		Log:           []Action{},
		GameStartTime: start,
		TurnDeadline:  start.Add(turnTimeout),
		TurnTimeout:   turnTimeout,
	}
}

func testRoster() map[string]*Player {
	return map[string]*Player{
		"white": {
			UserID:    "100",
			PlayerID:  "white",
			Online:    true,
			Ready:     true,
			GameReady: true,
			SeatIndex: 0,
			Side:      SideWhite,
		},
		"black": {
			PlayerID:  "black",
			Online:    true,
			Ready:     true,
			GameReady: true,
			SeatIndex: 1,
			Side:      SideBlack,
		},
	}
}

func seededState(values []seedPiece, side Side) *State {
	state := ruleState()
	state.Board = map[string][]string{}
	for _, value := range values {
		position := key(Hex{Q: value.q, R: value.r})
		state.Board[position] = append(state.Board[position], value.id)
	}
	state.Turns = map[Side]int{SideWhite: 4, SideBlack: 4}
	state.CurrentSide = side
	state.CurrentPlayer = playerForSide(state, side)
	return state
}

func passState() *State {
	values := make([]seedPiece, 0, 14)
	for index, piece := range pieces {
		if piece.Side == SideWhite {
			values = append(values, seedPiece{piece.PieceID, index, 0})
		}
	}
	values = append(values,
		seedPiece{"bQ1", -2, 0},
		seedPiece{"bA1", -1, 0},
		seedPiece{"bA2", 11, 0},
	)
	state := seededState(values, SideWhite)
	state.Turns = map[Side]int{SideWhite: 11, SideBlack: 8}
	return state
}

func mustPlace(
	t *testing.T,
	state *State,
	playerID,
	pieceID string,
	target Hex,
) {
	t.Helper()
	if err := placePiece(
		state,
		playerID,
		pieceID,
		target,
		false,
	); err != nil {
		t.Fatal(err)
	}
}

func candidateAt(options []candidate, target Hex) (candidate, bool) {
	for _, option := range options {
		if option.To == target {
			return option, true
		}
	}
	return candidate{}, false
}
