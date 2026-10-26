package quoridor

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestOpeningMovesAndStructuralPaths(t *testing.T) {
	state := ruleTestState()
	if !reflect.DeepEqual(state.Pawns, freshPawns()) ||
		state.WallStock[SideSouth] != 10 ||
		state.WallStock[SideNorth] != 10 {
		t.Fatalf("opening = %+v", state)
	}
	moves := legalMoves(state, SideSouth)
	want := []Cell{
		{Row: 7, Column: 4},
		{Row: 8, Column: 3},
		{Row: 8, Column: 5},
	}
	if !reflect.DeepEqual(moves, want) {
		t.Fatalf("opening moves = %+v, want %+v", moves, want)
	}
	for _, side := range []Side{SideSouth, SideNorth} {
		path := shortestPath(state, side)
		if len(path) != 9 ||
			path[0] != state.Pawns[side] ||
			path[len(path)-1].Row != targetRow(side) {
			t.Fatalf("%s path = %+v", side, path)
		}
	}
}

func TestWallsBlockExactlyTwoEdges(t *testing.T) {
	horizontal := []Wall{{
		Row:         4,
		Column:      3,
		Orientation: OrientationHorizontal,
		Side:        SideSouth,
	}}
	if edgeOpen(horizontal, Cell{4, 3}, Cell{5, 3}) ||
		edgeOpen(horizontal, Cell{4, 4}, Cell{5, 4}) ||
		!edgeOpen(horizontal, Cell{4, 5}, Cell{5, 5}) {
		t.Fatal("horizontal wall blocked the wrong edges")
	}
	vertical := []Wall{{
		Row:         4,
		Column:      3,
		Orientation: OrientationVertical,
		Side:        SideSouth,
	}}
	if edgeOpen(vertical, Cell{4, 3}, Cell{4, 4}) ||
		edgeOpen(vertical, Cell{5, 3}, Cell{5, 4}) ||
		!edgeOpen(vertical, Cell{3, 3}, Cell{3, 4}) {
		t.Fatal("vertical wall blocked the wrong edges")
	}
}

func TestStraightJumpAndBlockedDiagonalDetour(t *testing.T) {
	jump := ruleTestState()
	jump.Pawns[SideSouth] = Cell{Row: 4, Column: 4}
	jump.Pawns[SideNorth] = Cell{Row: 3, Column: 4}
	if !containsCell(legalMoves(jump, SideSouth), Cell{Row: 2, Column: 4}) ||
		containsCell(legalMoves(jump, SideSouth), Cell{Row: 3, Column: 3}) ||
		containsCell(legalMoves(jump, SideSouth), Cell{Row: 3, Column: 5}) {
		t.Fatalf("straight jump moves = %+v", legalMoves(jump, SideSouth))
	}

	jump.Walls = []Wall{{
		Row:         2,
		Column:      4,
		Orientation: OrientationHorizontal,
		Side:        SideNorth,
	}}
	jump.WallStock[SideNorth]--
	moves := legalMoves(jump, SideSouth)
	if containsCell(moves, Cell{Row: 2, Column: 4}) ||
		!containsCell(moves, Cell{Row: 3, Column: 3}) ||
		!containsCell(moves, Cell{Row: 3, Column: 5}) {
		t.Fatalf("diagonal detour moves = %+v", moves)
	}

	jump.Walls = append(jump.Walls, Wall{
		Row:         3,
		Column:      3,
		Orientation: OrientationVertical,
		Side:        SideNorth,
	})
	jump.WallStock[SideNorth]--
	moves = legalMoves(jump, SideSouth)
	if containsCell(moves, Cell{Row: 3, Column: 3}) ||
		!containsCell(moves, Cell{Row: 3, Column: 5}) {
		t.Fatalf("side wall detour moves = %+v", moves)
	}
}

func TestBoardEdgeAllowsDiagonalDetour(t *testing.T) {
	state := ruleTestState()
	state.Pawns[SideSouth] = Cell{Row: 1, Column: 4}
	state.Pawns[SideNorth] = Cell{Row: 0, Column: 4}
	moves := legalMoves(state, SideSouth)
	if !containsCell(moves, Cell{Row: 0, Column: 3}) ||
		!containsCell(moves, Cell{Row: 0, Column: 5}) {
		t.Fatalf("edge detour moves = %+v", moves)
	}
}

func TestWallOverlapCrossingAndPathValidation(t *testing.T) {
	state := ruleTestState()
	if err := placeWall(state, "south", WallSpec{
		Row:         3,
		Column:      3,
		Orientation: OrientationHorizontal,
	}, false); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		wall   WallSpec
		reason string
	}{
		{
			WallSpec{3, 3, OrientationHorizontal},
			wallReasonOverlap,
		},
		{
			WallSpec{3, 4, OrientationHorizontal},
			wallReasonOverlap,
		},
		{
			WallSpec{3, 3, OrientationVertical},
			wallReasonCrossing,
		},
	} {
		legal, reason := wallCheck(state, SideNorth, test.wall)
		if legal || reason != test.reason {
			t.Fatalf("wall %+v = %v/%s", test.wall, legal, reason)
		}
	}
	if legal, reason := wallCheck(
		state,
		SideNorth,
		WallSpec{3, 5, OrientationHorizontal},
	); !legal || reason != "" {
		t.Fatalf("end-to-end wall = %v/%s", legal, reason)
	}

	path := ruleTestState()
	path.Pawns[SideSouth] = Cell{Row: 8, Column: 0}
	path.Walls = []Wall{{
		Row:         7,
		Column:      0,
		Orientation: OrientationVertical,
		Side:        SideNorth,
	}}
	path.WallStock[SideNorth]--
	if legal, reason := wallCheck(
		path,
		SideSouth,
		WallSpec{6, 0, OrientationHorizontal},
	); legal || reason != wallReasonBlocksPath {
		t.Fatalf("blocking wall = %v/%s", legal, reason)
	}
	if legal, reason := wallCheck(
		path,
		SideSouth,
		WallSpec{6, 1, OrientationHorizontal},
	); !legal || reason != "" {
		t.Fatalf("non-blocking wall = %v/%s", legal, reason)
	}
}

func TestMoveWinWallConservationAndInvalidActions(t *testing.T) {
	state := ruleTestState()
	state.Pawns[SideSouth] = Cell{Row: 1, Column: 3}
	state.Pawns[SideNorth] = Cell{Row: 6, Column: 4}
	before := cloneState(state)
	if err := movePawn(state, "north", Cell{0, 3}, false); err == nil ||
		!reflect.DeepEqual(state, before) {
		t.Fatal("inactive player changed state")
	}
	if err := movePawn(state, "south", Cell{-1, 3}, false); err == nil ||
		!reflect.DeepEqual(state, before) {
		t.Fatal("out-of-range move changed state")
	}
	if err := movePawn(state, "south", Cell{0, 3}, false); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusEnd ||
		state.WinnerID != "south" ||
		state.WinnerSide != SideSouth {
		t.Fatalf("finish = %+v", state)
	}
	if err := placeWall(state, "north", WallSpec{
		Row:         0,
		Column:      0,
		Orientation: OrientationHorizontal,
	}, false); err == nil {
		t.Fatal("wall accepted after finish")
	}
}

func TestRandomLegalPlayPreservesPathsAndWallConservation(t *testing.T) {
	random := rand.New(rand.NewSource(9264))
	state := ruleTestState()
	for step := 0; step < 160 && state.Status == StatusPlaying; step++ {
		side := sideForPlayer(state, state.CurrentPlayer)
		actions := allCandidates(state, side)
		if len(actions) == 0 {
			t.Fatal("no legal action")
		}
		action := actions[random.Intn(len(actions))]
		if !applyCandidate(state, side, action) {
			t.Fatalf("failed to apply legal action %+v", action)
		}
		if len(shortestPath(state, SideSouth)) == 0 ||
			len(shortestPath(state, SideNorth)) == 0 {
			t.Fatal("legal action removed a path")
		}
		if len(state.Walls)+
			state.WallStock[SideSouth]+
			state.WallStock[SideNorth] != 20 {
			t.Fatal("wall conservation failed")
		}
	}
}

func ruleTestState() *State {
	return &State{
		RoomID:        "quoridor-rules",
		OwnerID:       "south",
		Status:        StatusPlaying,
		MaxPlayers:    2,
		Visibility:    "public",
		RandomSeed:    40,
		RoundNo:       1,
		PlayerOrder:   []string{"south", "north"},
		SouthPlayerID: "south",
		NorthPlayerID: "north",
		FirstPlayerID: "south",
		CurrentPlayer: "south",
		Roster: map[string]*Player{
			"south": {
				PlayerID:  "south",
				Online:    true,
				Ready:     true,
				GameReady: true,
				SeatIndex: 0,
				Side:      SideSouth,
			},
			"north": {
				PlayerID:  "north",
				Online:    true,
				Ready:     true,
				GameReady: true,
				SeatIndex: 1,
				Side:      SideNorth,
			},
		},
		Pawns:     freshPawns(),
		Walls:     []Wall{},
		WallStock: freshWallStock(),
		Log:       []Action{},
	}
}

func containsCell(values []Cell, target Cell) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
