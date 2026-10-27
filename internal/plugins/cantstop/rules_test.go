package cantstop

import (
	"reflect"
	"testing"
)

func TestPairingGeneration(t *testing.T) {
	state := rulesState()
	state.Dice = []int{2, 3, 4, 5}
	state.RollNumber = 1
	options := legalPairingOptions(state)
	got := make([][]int, 0, len(options))
	for _, option := range options {
		got = append(got, option.Sums)
	}
	want := [][]int{{5, 9}, {6, 8}, {7, 7}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pairings = %#v, want %#v", got, want)
	}
}

func TestDuplicatePairingAndDoubleAdvance(t *testing.T) {
	state := rulesState()
	state.Dice = []int{3, 3, 4, 4}
	state.RollNumber = 1
	options := legalPairingOptions(state)
	if len(options) != 2 {
		t.Fatalf("options = %#v", options)
	}
	var double *PairingOption
	for index := range options {
		if reflect.DeepEqual(options[index].Sums, []int{7, 7}) {
			double = &options[index]
		}
	}
	if double == nil || double.Advances[7] != 2 ||
		double.RunnerPositions[7] != 2 {
		t.Fatalf("double seven option = %#v", double)
	}
}

func TestSingleMarkerChoiceCreatesTwoVariants(t *testing.T) {
	state := rulesState()
	state.Runners = map[int]int{6: 2, 8: 2}
	pair := pairing{index: 0, sums: []int{5, 9}, diceIndexes: []int{0, 1, 2, 3}}
	options := variantsForPairing(state, pair)
	if len(options) != 2 {
		t.Fatalf("variant count = %d: %#v", len(options), options)
	}
	if options[0].StepCount != 1 || options[1].StepCount != 1 {
		t.Fatalf("variants should advance once: %#v", options)
	}
}

func TestBustAndBank(t *testing.T) {
	state := rulesState()
	state.Runners = map[int]int{7: columnHeight(7)}
	if err := bank(state, "p1", SourceManual); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusBanked || state.ClaimedBy[7] != "p1" ||
		state.Roster["p1"].Saved[7] != columnHeight(7) {
		t.Fatalf("bank state = %+v", state)
	}

	state = rulesState()
	state.Runners = map[int]int{6: 3, 8: 2}
	if err := bust(state, "p1", SourceTimeout); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusBust || state.LostGain != 5 ||
		len(state.Runners) != 0 || state.Roster["p1"].Busts != 1 {
		t.Fatalf("bust state = %+v", state)
	}
}

func TestThirdClaimWins(t *testing.T) {
	state := rulesState()
	state.Roster["p1"].Saved[2] = columnHeight(2)
	state.Roster["p1"].Saved[3] = columnHeight(3)
	state.ClaimedBy[2] = "p1"
	state.ClaimedBy[3] = "p1"
	state.Runners = map[int]int{4: columnHeight(4)}
	if err := bank(state, "p1", SourceManual); err != nil {
		t.Fatal(err)
	}
	if state.Status != StatusEnd || state.Result == nil ||
		state.Result.WinnerID != "p1" {
		t.Fatalf("result = %+v", state.Result)
	}
}

func rulesState() *State {
	return &State{
		Status:       StatusDecide,
		MaxPlayers:   defaultMaxPlayers,
		Visibility:   "public",
		RandomSeed:   1,
		MatchRoundNo: 1,
		Roster: map[string]*Player{
			"p1": {PlayerID: "p1", SeatIndex: 0, Saved: map[int]int{}},
			"p2": {PlayerID: "p2", SeatIndex: 1, Saved: map[int]int{}},
		},
		PlayerOrder:    []string{"p1", "p2"},
		CurrentPlayer:  "p1",
		NextPlayer:     "p2",
		TurnNumber:     1,
		RollNumber:     1,
		Runners:        map[int]int{},
		ClaimedBy:      map[int]string{},
		Dice:           []int{},
		PairingOptions: []PairingOption{},
		Log:            []Action{},
	}
}
