package yinsh

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestBoardHasEightyFiveUniquePoints(t *testing.T) {
	points := BoardPoints()
	if len(points) != 85 {
		t.Fatalf("points = %d, want 85", len(points))
	}
	ids := map[string]bool{}
	coords := map[[2]int]bool{}
	for _, point := range points {
		if ids[point.ID] || coords[[2]int{point.Q, point.R}] {
			t.Fatalf("duplicate point: %+v", point)
		}
		ids[point.ID] = true
		coords[[2]int{point.Q, point.R}] = true
	}
}

func TestLegalMovesRespectLinesMarkersAndRings(t *testing.T) {
	state := testGameState()
	state.Rings = map[string]Side{"E4": SideWhite}
	moves := legalMoves(state, "E4")
	if !hasDestination(moves, "E8") || hasDestination(moves, "G5") {
		t.Fatalf("straight moves = %+v", moves)
	}

	state.Rings["E6"] = SideBlack
	moves = legalMoves(state, "E4")
	for _, blocked := range []string{"E6", "E7", "E8"} {
		if hasDestination(moves, blocked) {
			t.Fatalf("ring blocker allowed %s: %+v", blocked, moves)
		}
	}

	delete(state.Rings, "E6")
	state.Markers = map[string]Side{"E5": SideBlack, "E6": SideWhite}
	moves = legalMoves(state, "E4")
	if !hasDestination(moves, "E7") || hasDestination(moves, "E8") {
		t.Fatalf("marker jump destinations = %+v", moves)
	}
}

func TestMoveLeavesMarkerAndFlipsBothColors(t *testing.T) {
	state := testGameState()
	state.Rings = map[string]Side{
		"E4": SideWhite,
		"C5": SideWhite,
		"G3": SideWhite,
		"I8": SideWhite,
		"F9": SideWhite,
		"D3": SideBlack,
		"H9": SideBlack,
		"I6": SideBlack,
		"B3": SideBlack,
		"J7": SideBlack,
	}
	state.Markers = map[string]Side{"E5": SideBlack, "E6": SideWhite}
	if err := moveRing(state, "white", "E4", "E7"); err != nil {
		t.Fatal(err)
	}
	if state.Markers["E4"] != SideWhite ||
		state.Markers["E5"] != SideWhite ||
		state.Markers["E6"] != SideBlack ||
		state.Rings["E7"] != SideWhite ||
		state.Rings["E4"] != "" {
		t.Fatalf("moved state = %+v/%+v", state.Rings, state.Markers)
	}
}

func TestPlacementAlternatesAndWhiteMovesFirst(t *testing.T) {
	state := testGameState()
	state.Status = StatusPlacement
	state.CurrentPlayer = "white"
	state.Rings = map[string]Side{}
	state.PlacedRings = map[Side]int{SideWhite: 0, SideBlack: 0}
	points := []string{"E4", "D3", "C5", "F9", "G3", "H9", "I8", "I6", "F6", "D7"}
	players := []string{"white", "black"}
	for index, pointID := range points {
		if err := placeRing(state, players[index%2], pointID); err != nil {
			t.Fatalf("placement %d: %v", index, err)
		}
	}
	if state.Status != StatusMove || state.CurrentPlayer != "white" ||
		state.PlacedRings[SideWhite] != 5 || state.PlacedRings[SideBlack] != 5 {
		t.Fatalf("placement result = %+v", state)
	}
}

func TestRowsHandleExtendedAndIntersectingLines(t *testing.T) {
	state := testGameState()
	state.Rings = map[string]Side{}
	state.Markers = map[string]Side{
		"F3": SideWhite, "F4": SideWhite, "F5": SideWhite,
		"F6": SideWhite, "F7": SideWhite, "F8": SideWhite,
	}
	if got := len(rowsFor(state, SideWhite)); got != 2 {
		t.Fatalf("six marker row windows = %d, want 2", got)
	}

	state.Markers = map[string]Side{
		"F3": SideWhite, "F4": SideWhite, "F5": SideWhite,
		"F6": SideWhite, "F7": SideWhite,
		"D4": SideWhite, "E5": SideWhite, "G7": SideWhite, "H8": SideWhite,
	}
	rows := rowsFor(state, SideWhite)
	if len(rows) != 2 {
		t.Fatalf("cross rows = %+v", rows)
	}
	for _, pointID := range rows[0].Points {
		delete(state.Markers, pointID)
	}
	if remaining := rowsFor(state, SideWhite); len(remaining) != 0 {
		t.Fatalf("intersecting row remained after shared point removal: %+v", remaining)
	}
}

func TestResolutionPrioritizesMoverAndKeepsDisjointRows(t *testing.T) {
	state := testGameState()
	state.Rings = map[string]Side{
		"C5": SideWhite, "G3": SideWhite, "I8": SideWhite,
		"D3": SideBlack, "I6": SideBlack, "B3": SideBlack,
	}
	state.Markers = map[string]Side{
		"F3": SideWhite, "F4": SideWhite, "F5": SideWhite, "F6": SideWhite, "F7": SideWhite,
		"H5": SideWhite, "H6": SideWhite, "H7": SideWhite, "H8": SideWhite, "H9": SideWhite,
		"D5": SideBlack, "D6": SideBlack, "D7": SideBlack, "D8": SideBlack, "D9": SideBlack,
	}
	state.MoverPlayer = "white"
	state.Resolution = []string{"white", "black"}
	advanceResolution(state)
	if state.Status != StatusRemoveRow || state.ResolverPlayer != "white" {
		t.Fatalf("first resolver = %s/%s", state.Status, state.ResolverPlayer)
	}
	rows := rowsFor(state, SideWhite)
	if len(rows) < 2 {
		t.Fatalf("white disjoint rows = %+v", rows)
	}
	if err := removeRow(state, "white", rows[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := removeRing(state, "white", "C5"); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusRemoveRow || state.ResolverPlayer != "white" {
		t.Fatalf("second white row was not retained: %s/%s", state.Status, state.ResolverPlayer)
	}
}

func TestRemovingThirdRingEndsImmediately(t *testing.T) {
	state := testGameState()
	state.Status = StatusRemoveRing
	state.CurrentPlayer = "white"
	state.ResolverPlayer = "white"
	state.Resolution = []string{"white", "black"}
	state.RingsRemoved[SideWhite] = 2
	state.Rings = map[string]Side{"C5": SideWhite, "D3": SideBlack}
	if err := removeRing(state, "white", "C5"); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusEnd || state.WinnerID != "white" ||
		state.RingsRemoved[SideWhite] != 3 {
		t.Fatalf("third ring result = %+v", state)
	}
}

func TestRoundAlternatesWhitePlayer(t *testing.T) {
	for roundNo, wantWhite := range map[int]string{1: "owner", 2: "guest", 3: "owner"} {
		state := startPreparedMatch(t, roundNo)
		if got := playerForSide(state, SideWhite); got != wantWhite {
			t.Fatalf("round %d white = %s, want %s", roundNo, got, wantWhite)
		}
		if state.CurrentPlayer != wantWhite || state.Status != StatusPlacement {
			t.Fatalf("round %d start = %s/%s", roundNo, state.Status, state.CurrentPlayer)
		}
	}
}

func TestTimeoutCannotBeSubmittedByClientAndInternalTimeoutIsLegal(t *testing.T) {
	state := startPreparedMatch(t, 1)
	plugin := &Plugin{}
	_, err := plugin.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		PlayerID: "owner",
		Type:     CommandTimeout,
		ConnectionRef: contracts.ConnectionRef{
			GatewayNode: "gateway",
			SessionID:   "session",
		},
	})
	if err == nil {
		t.Fatal("client timeout was accepted")
	}
	before := cloneState(state)
	if _, err := plugin.ApplyCommand(context.Background(), state, contracts.RoomCommand{
		PlayerID: "owner",
		Type:     CommandTimeout,
		SentAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(before.Rings, state.Rings) || len(state.Rings) != 1 {
		t.Fatalf("internal timeout did not place a legal ring: %+v", state.Rings)
	}
}

func TestAIAlwaysReturnsLegalCommand(t *testing.T) {
	state := startPreparedMatch(t, 1)
	state.Roster["owner"].AI = true
	plugin := &Plugin{}
	command, err := plugin.Decide(context.Background(), game.AIRequest{
		PlayerID: "owner",
		State:    state,
	})
	if err != nil {
		t.Fatal(err)
	}
	if command.Type != CommandPlaceRing {
		t.Fatalf("AI command = %+v", command)
	}
	if _, err := plugin.ApplyCommand(context.Background(), state, command); err != nil {
		t.Fatalf("AI returned illegal command: %v", err)
	}
}

func testGameState() *State {
	return &State{
		RoomID:      "yinsh-test",
		OwnerID:     "white",
		Status:      StatusMove,
		MaxPlayers:  2,
		Visibility:  contracts.VisibilityPrivate,
		RandomSeed:  1,
		RoundNo:     1,
		PlayerOrder: []string{"white", "black"},
		Roster: map[string]*Player{
			"white": {PlayerID: "white", Side: SideWhite, SeatIndex: 0, Online: true, Ready: true},
			"black": {PlayerID: "black", Side: SideBlack, SeatIndex: 1, Online: true, Ready: true},
		},
		Rings:         map[string]Side{},
		Markers:       map[string]Side{},
		PlacedRings:   map[Side]int{SideWhite: 5, SideBlack: 5},
		RingsRemoved:  map[Side]int{SideWhite: 0, SideBlack: 0},
		CurrentPlayer: "white",
		Log:           []Action{},
	}
}

func startPreparedMatch(t *testing.T, roundNo int) *State {
	t.Helper()
	plugin := &Plugin{}
	raw, err := plugin.InitRoom(context.Background(), game.RoomConfig{
		RoomID:        "yinsh-room",
		OwnerPlayerID: "owner",
		MaxPlayers:    2,
		RandomSeed:    42,
		RoundNo:       roundNo,
	})
	if err != nil {
		t.Fatal(err)
	}
	state := raw.(*State)
	commands := []contracts.RoomCommand{
		{PlayerID: "owner", Type: "connect"},
		{PlayerID: "guest", Type: "connect"},
		{PlayerID: "guest", Type: "match_ready", Payload: map[string]any{"ready": true}},
		{PlayerID: "owner", Type: "match_begin"},
		{PlayerID: "owner", Type: "game_ready", SentAt: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)},
		{PlayerID: "guest", Type: "game_ready", SentAt: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)},
	}
	for _, command := range commands {
		if _, err := plugin.ApplyCommand(context.Background(), state, command); err != nil {
			t.Fatalf("%s: %v", command.Type, err)
		}
	}
	return state
}

func hasDestination(moves []MoveOption, pointID string) bool {
	for _, move := range moves {
		if move.To == pointID {
			return true
		}
	}
	return false
}
