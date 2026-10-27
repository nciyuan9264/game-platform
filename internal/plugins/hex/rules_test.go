package hex

import (
	"reflect"
	"testing"
)

func TestNeighborsAndConnections(t *testing.T) {
	center := Cell{Row: 5, Column: 5}
	wantNeighbors := []Cell{
		{4, 5}, {4, 6}, {5, 4}, {5, 6}, {6, 4}, {6, 5},
	}
	if got := neighbors(center); !reflect.DeepEqual(got, wantNeighbors) {
		t.Fatalf("neighbors = %+v, want %+v", got, wantNeighbors)
	}

	red := freshBoard()
	for row := 0; row < boardSize; row++ {
		red[position(Cell{Row: row, Column: 5})] = StoneRed
	}
	if path := connection(red, SideRed); len(path) != boardSize {
		t.Fatalf("red path = %+v", path)
	}
	if path := connection(red, SideTeal); len(path) != 0 {
		t.Fatalf("unexpected teal path = %+v", path)
	}

	teal := freshBoard()
	for column := 0; column < boardSize; column++ {
		teal[position(Cell{Row: 5, Column: column})] = StoneTeal
	}
	if path := connection(teal, SideTeal); len(path) != boardSize {
		t.Fatalf("teal path = %+v", path)
	}

	cornersOnly := freshBoard()
	for row := 0; row < boardSize; row++ {
		cornersOnly[position(Cell{Row: row, Column: row})] = StoneRed
	}
	if path := connection(cornersOnly, SideRed); len(path) != 0 {
		t.Fatalf("corner contacts connected: %+v", path)
	}
}

func TestPlaceSwapAndWin(t *testing.T) {
	state := ruleTestState()
	center := Cell{Row: 5, Column: 5}
	if err := placeStone(state, "red", center, false); err != nil {
		t.Fatal(err)
	}
	if !canSwap(state, "teal") {
		t.Fatal("swap was not offered after the opening move")
	}
	if err := swapSides(state, "teal", false); err != nil {
		t.Fatal(err)
	}
	if state.Board[position(center)] != StoneRed ||
		state.RedPlayerID != "teal" ||
		state.TealPlayerID != "red" ||
		state.CurrentPlayer != "red" ||
		state.Roster["teal"].Side != SideRed {
		t.Fatalf("swapped state = %+v", state)
	}
	if canSwap(state, "red") {
		t.Fatal("swap remained available")
	}

	win := ruleTestState()
	for row := 0; row < boardSize-1; row++ {
		win.Board[position(Cell{Row: row, Column: 3})] = StoneRed
		cell := Cell{Row: row, Column: 3}
		win.Log = append(win.Log, Action{
			ID: row + 1, Kind: "place", PlayerID: "red",
			Side: SideRed, Cell: &cell,
		})
	}
	last := cloneAction(win.Log[len(win.Log)-1])
	win.LastAction = &last
	if err := placeStone(
		win,
		"red",
		Cell{Row: boardSize - 1, Column: 3},
		false,
	); err != nil {
		t.Fatal(err)
	}
	if win.Status != StatusEnd || win.WinnerID != "red" ||
		len(win.WinningPath) != boardSize {
		t.Fatalf("win = %+v", win)
	}
	if err := placeStone(win, "teal", Cell{Row: 0, Column: 0}, false); err == nil {
		t.Fatal("move accepted after the game ended")
	}
}

func TestSequentialFillAlwaysFindsAWinner(t *testing.T) {
	state := ruleTestState()
	for index := range state.Board {
		if state.Status == StatusEnd {
			break
		}
		if err := placeStone(
			state,
			state.CurrentPlayer,
			cellAt(index),
			false,
		); err != nil {
			t.Fatal(err)
		}
	}
	if state.WinnerID == "" {
		t.Fatal("filled board did not produce a winner")
	}
}

func ruleTestState() *State {
	return &State{
		RoomID:        "hex-rules",
		OwnerID:       "red",
		Status:        StatusPlaying,
		MaxPlayers:    2,
		Visibility:    "public",
		RandomSeed:    40,
		RoundNo:       1,
		PlayerOrder:   []string{"red", "teal"},
		RedPlayerID:   "red",
		TealPlayerID:  "teal",
		FirstPlayerID: "red",
		CurrentPlayer: "red",
		Roster: map[string]*Player{
			"red": {
				UserID: "1", PlayerID: "red", Online: true,
				Ready: true, GameReady: true, SeatIndex: 0, Side: SideRed,
			},
			"teal": {
				UserID: "2", PlayerID: "teal", Online: true,
				Ready: true, GameReady: true, SeatIndex: 1, Side: SideTeal,
			},
		},
		Board:       freshBoard(),
		Log:         []Action{},
		WinningPath: []Cell{},
	}
}
