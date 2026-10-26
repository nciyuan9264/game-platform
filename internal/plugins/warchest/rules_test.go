package warchest

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform/internal/game"
)

func TestBoardTopology(t *testing.T) {
	if len(boardCells) != 37 || len(locationCells) != 10 {
		t.Fatalf(
			"board has %d cells and %d locations",
			len(boardCells),
			len(locationCells),
		)
	}
	for key := range boardCells {
		for _, neighbor := range adjacent(key) {
			if distance(key, neighbor) != 1 ||
				!containsString(adjacent(neighbor), key) {
				t.Fatalf("asymmetric adjacency: %s -> %s", key, neighbor)
			}
		}
	}
}

func TestBasePoolContainsSixteenUnitsAndRoyalIsSeparate(t *testing.T) {
	if len(unitOrder) != 16 {
		t.Fatalf("unit pool size = %d", len(unitOrder))
	}
	seen := map[UnitType]bool{}
	for _, unit := range unitOrder {
		if seen[unit] || unit == UnitRoyal || unitTotals[unit] < 4 {
			t.Fatalf("invalid base unit %q", unit)
		}
		seen[unit] = true
	}
	if unitTotals[UnitRoyal] != 1 {
		t.Fatalf("royal coin total = %d", unitTotals[UnitRoyal])
	}
}

func TestRangedAndMountedTactics(t *testing.T) {
	tests := []struct {
		name       string
		attacker   UnitType
		targetCell string
		wantTo     string
	}{
		{name: "crossbow", attacker: UnitCrossbow, targetCell: "2,0"},
		{name: "archer", attacker: UnitArcher, targetCell: "1,1"},
		{
			name:       "cavalry",
			attacker:   UnitCavalry,
			targetCell: "2,0",
			wantTo:     "1,0",
		},
		{
			name:       "lancer",
			attacker:   UnitLancer,
			targetCell: "3,0",
			wantTo:     "2,0",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := actionFixture(t, test.attacker, UnitScout)
			attacker := deployFixtureUnit(
				t,
				state,
				SideOak,
				test.attacker,
				"0,0",
				1,
			)
			target := deployFixtureUnit(
				t,
				state,
				SideFalcon,
				UnitScout,
				test.targetCell,
				1,
			)
			giveHandCoin(
				t,
				state,
				state.playerForSide(SideOak),
				test.attacker,
			)
			action := findAction(
				t,
				legalActions(state, state.CurrentPlayer),
				ActionTactic,
				func(action LegalAction) bool {
					return action.ActorUnitID == attacker.UnitID &&
						action.TargetUnitID == target.UnitID &&
						(test.wantTo == "" || action.To == test.wantTo)
				},
			)
			if test.attacker == UnitLancer && len(action.Path) != 2 {
				t.Fatalf("lancer path = %v", action.Path)
			}
		})
	}

	light := actionFixture(t, UnitLight, UnitScout)
	unit := deployFixtureUnit(t, light, SideOak, UnitLight, "0,0", 1)
	giveHandCoin(t, light, light.playerForSide(SideOak), UnitLight)
	action := findAction(
		t,
		legalActions(light, light.CurrentPlayer),
		ActionTactic,
		func(action LegalAction) bool {
			return action.ActorUnitID == unit.UnitID
		},
	)
	if len(action.Path) != 2 || action.To == action.From {
		t.Fatalf("light cavalry path = %+v", action)
	}
}

func TestScoutAndSupportTactics(t *testing.T) {
	scout := actionFixture(t, UnitScout, UnitKnight)
	ally := deployFixtureUnit(t, scout, SideOak, UnitSword, "0,0", 1)
	_ = ally
	giveHandCoin(t, scout, scout.playerForSide(SideOak), UnitScout)
	deploy := findAction(
		t,
		legalActions(scout, scout.CurrentPlayer),
		ActionDeploy,
		func(action LegalAction) bool {
			return containsString(adjacent("0,0"), action.To) &&
				scout.Controls[action.To] != SideOak
		},
	)
	if deploy.To == "" {
		t.Fatal("scout had no frontline deployment")
	}

	ensign := actionFixture(t, UnitEnsign, UnitKnight)
	source := deployFixtureUnit(t, ensign, SideOak, UnitEnsign, "0,0", 1)
	recipient := deployFixtureUnit(t, ensign, SideOak, UnitSword, "-1,1", 1)
	giveHandCoin(t, ensign, ensign.playerForSide(SideOak), UnitEnsign)
	order := findAction(
		t,
		legalActions(ensign, ensign.CurrentPlayer),
		ActionTactic,
		func(action LegalAction) bool {
			return action.SourceUnitID == source.UnitID &&
				action.ActorUnitID == recipient.UnitID &&
				action.Maneuver == ActionMove
		},
	)
	if distance(order.From, order.To) != 1 ||
		distance(source.Cell, order.To) > 2 {
		t.Fatalf("invalid ensign order: %+v", order)
	}

	marshall := actionFixture(t, UnitMarshall, UnitScout)
	marshalUnit := deployFixtureUnit(
		t,
		marshall,
		SideOak,
		UnitMarshall,
		"0,0",
		1,
	)
	sword := deployFixtureUnit(
		t,
		marshall,
		SideOak,
		UnitSword,
		"-1,1",
		1,
	)
	target := deployFixtureUnit(
		t,
		marshall,
		SideFalcon,
		UnitScout,
		"0,1",
		1,
	)
	giveHandCoin(
		t,
		marshall,
		marshall.playerForSide(SideOak),
		UnitMarshall,
	)
	findAction(
		t,
		legalActions(marshall, marshall.CurrentPlayer),
		ActionTactic,
		func(action LegalAction) bool {
			return action.SourceUnitID == marshalUnit.UnitID &&
				action.ActorUnitID == sword.UnitID &&
				action.TargetUnitID == target.UnitID &&
				action.Maneuver == ActionAttack
		},
	)
}

func TestMercenaryRecruitAndRoyalGuardMovement(t *testing.T) {
	mercenary := actionFixture(t, UnitMercenary, UnitScout)
	unit := deployFixtureUnit(
		t,
		mercenary,
		SideOak,
		UnitMercenary,
		"-1,1",
		1,
	)
	giveHandCoin(
		t,
		mercenary,
		mercenary.playerForSide(SideOak),
		UnitRoyal,
	)
	recruit := findAction(
		t,
		legalActions(mercenary, mercenary.CurrentPlayer),
		ActionRecruit,
		func(action LegalAction) bool {
			return action.RecruitUnit == UnitMercenary
		},
	)
	applyAction(t, mercenary, mercenary.CurrentPlayer, recruit)
	if mercenary.Pending == nil ||
		mercenary.Pending.Kind != "mercenary" ||
		mercenary.Pending.ActorUnitID != unit.UnitID {
		t.Fatalf("mercenary continuation = %+v", mercenary.Pending)
	}

	guard := actionFixture(t, UnitGuard, UnitScout)
	guardUnit := deployFixtureUnit(
		t,
		guard,
		SideOak,
		UnitGuard,
		"0,0",
		1,
	)
	guard.Controls["2,0"] = SideOak
	giveHandCoin(t, guard, guard.playerForSide(SideOak), UnitRoyal)
	move := findAction(
		t,
		legalActions(guard, guard.CurrentPlayer),
		ActionTactic,
		func(action LegalAction) bool {
			return action.ActorUnitID == guardUnit.UnitID &&
				action.To == "2,0"
		},
	)
	if move.UnitType != UnitRoyal || len(move.Path) != 2 {
		t.Fatalf("royal guard move = %+v", move)
	}
}

func TestBasicActionsAndCoinConservation(t *testing.T) {
	state := actionFixture(t, UnitSword, UnitScout)
	player := state.Roster[state.CurrentPlayer]
	giveHandCoin(t, state, player, UnitSword)
	deploy := findAction(
		t,
		legalActions(state, player.PlayerID),
		ActionDeploy,
		func(LegalAction) bool { return true },
	)
	applyAction(t, state, player.PlayerID, deploy)
	if state.Board[deploy.To] == nil ||
		len(player.Discard) != 0 {
		t.Fatalf("deployment settled incorrectly")
	}

	giveHandCoin(t, state, player, UnitSword)
	state.CurrentPlayer = player.PlayerID
	bolster := findAction(
		t,
		legalActions(state, player.PlayerID),
		ActionBolster,
		func(action LegalAction) bool {
			return action.ActorUnitID == state.Board[deploy.To].UnitID
		},
	)
	applyAction(t, state, player.PlayerID, bolster)
	if len(state.Board[deploy.To].CoinIDs) != 2 {
		t.Fatalf("bolster did not add a coin")
	}
	if err := validateCoinConservation(state); err != nil {
		t.Fatal(err)
	}
}

func TestPikemanRetaliatesAndKnightRequiresBolsteredAttacker(t *testing.T) {
	state := actionFixture(t, UnitSword, UnitPike)
	attacker := deployFixtureUnit(t, state, SideOak, UnitSword, "0,0", 1)
	defender := deployFixtureUnit(t, state, SideFalcon, UnitPike, "1,0", 1)
	giveHandCoin(t, state, state.playerForSide(SideOak), UnitSword)
	state.CurrentPlayer = state.playerIDForSide(SideOak)
	attack := findAction(
		t,
		legalActions(state, state.CurrentPlayer),
		ActionAttack,
		func(action LegalAction) bool {
			return action.TargetUnitID == defender.UnitID
		},
	)
	applyAction(t, state, state.CurrentPlayer, attack)
	if findUnitByID(state, attacker.UnitID) != nil ||
		findUnitByID(state, defender.UnitID) != nil {
		t.Fatal("pikeman retaliation did not remove both units")
	}

	knightState := actionFixture(t, UnitSword, UnitKnight)
	sword := deployFixtureUnit(
		t,
		knightState,
		SideOak,
		UnitSword,
		"0,0",
		1,
	)
	knight := deployFixtureUnit(
		t,
		knightState,
		SideFalcon,
		UnitKnight,
		"1,0",
		1,
	)
	giveHandCoin(
		t,
		knightState,
		knightState.playerForSide(SideOak),
		UnitSword,
	)
	if hasTarget(
		legalActions(knightState, knightState.CurrentPlayer),
		knight.UnitID,
	) {
		t.Fatal("unbolstered swordsman could attack a knight")
	}
	addBoardCoin(t, knightState, sword)
	if !hasTarget(
		legalActions(knightState, knightState.CurrentPlayer),
		knight.UnitID,
	) {
		t.Fatal("bolstered swordsman could not attack a knight")
	}
}

func TestRoyalGuardDefenseIsTransactional(t *testing.T) {
	state := actionFixture(t, UnitCavalry, UnitGuard)
	attacker := deployFixtureUnit(
		t,
		state,
		SideOak,
		UnitCavalry,
		"0,0",
		1,
	)
	defender := deployFixtureUnit(
		t,
		state,
		SideFalcon,
		UnitGuard,
		"1,0",
		1,
	)
	giveHandCoin(t, state, state.playerForSide(SideOak), UnitCavalry)
	beforeHand := append([]string(nil), state.playerForSide(SideOak).Hand...)
	paidCoinID := beforeHand[0]
	attack := findAction(
		t,
		legalActions(state, state.CurrentPlayer),
		ActionAttack,
		func(action LegalAction) bool {
			return action.TargetUnitID == defender.UnitID
		},
	)
	applyAction(t, state, state.CurrentPlayer, attack)
	if state.Pending == nil ||
		state.Pending.Kind != "guard" ||
		!reflect.DeepEqual(
			state.playerForSide(SideOak).Hand,
			beforeHand,
		) ||
		findUnitByID(state, attacker.UnitID) == nil ||
		findUnitByID(state, defender.UnitID) == nil {
		t.Fatalf("guard prompt mutated attack too early: %+v", state.Pending)
	}
	defense := findAction(
		t,
		legalActions(state, state.CurrentPlayer),
		ActionDefend,
		func(action LegalAction) bool { return action.Defense == "supply" },
	)
	defenderPlayer := state.playerForSide(SideFalcon)
	supplyBefore := len(defenderPlayer.Supply[UnitGuard])
	applyAction(t, state, state.CurrentPlayer, defense)
	if findUnitByID(state, defender.UnitID) == nil ||
		len(defenderPlayer.Supply[UnitGuard]) != supplyBefore-1 ||
		len(defenderPlayer.Lost[UnitGuard]) != 1 ||
		state.ActionCounter != 1 ||
		state.Coins[paidCoinID] != UnitCavalry {
		t.Fatalf(
			"guard supply defense settled incorrectly: defender=%+v attacker=%+v pending=%+v",
			defenderPlayer,
			state.playerForSide(SideOak),
			state.Pending,
		)
	}
	if err := validateCoinConservation(state); err != nil {
		t.Fatal(err)
	}
}

func TestBerserkerContinuationUsesBoardReinforcement(t *testing.T) {
	state := actionFixture(t, UnitBerserker, UnitScout)
	berserker := deployFixtureUnit(
		t,
		state,
		SideOak,
		UnitBerserker,
		"-1,1",
		3,
	)
	state.Controls["-1,1"] = SideFalcon
	giveHandCoin(t, state, state.playerForSide(SideOak), UnitBerserker)
	control := findAction(
		t,
		legalActions(state, state.CurrentPlayer),
		ActionControl,
		func(LegalAction) bool { return true },
	)
	applyAction(t, state, state.CurrentPlayer, control)
	if state.Pending == nil || state.Pending.Kind != "berserker" {
		t.Fatal("berserker continuation was not created")
	}
	hand := append([]string(nil), state.playerForSide(SideOak).Hand...)
	move := findAction(
		t,
		legalActions(state, state.CurrentPlayer),
		ActionMove,
		func(LegalAction) bool { return true },
	)
	applyAction(t, state, state.CurrentPlayer, move)
	moved := findUnitByID(state, berserker.UnitID)
	if moved == nil ||
		len(moved.CoinIDs) != 2 ||
		len(state.playerForSide(SideOak).Hand) != len(hand) ||
		len(state.playerForSide(SideOak).Discard) != 2 {
		t.Fatalf(
			"berserker continuation paid incorrectly: moved=%+v player=%+v pending=%+v",
			moved,
			state.playerForSide(SideOak),
			state.Pending,
		)
	}
}

func TestFootmanTacticRequiresSecondUnit(t *testing.T) {
	state := actionFixture(t, UnitFootman, UnitScout)
	first := deployFixtureUnit(
		t,
		state,
		SideOak,
		UnitFootman,
		"-1,1",
		1,
	)
	second := deployFixtureUnit(
		t,
		state,
		SideOak,
		UnitFootman,
		"1,1",
		1,
	)
	state.Controls["-1,1"] = SideFalcon
	giveHandCoin(t, state, state.playerForSide(SideOak), UnitFootman)
	tactic := findAction(
		t,
		legalActions(state, state.CurrentPlayer),
		ActionTactic,
		func(action LegalAction) bool {
			return action.ActorUnitID == first.UnitID &&
				action.Maneuver == ActionControl
		},
	)
	applyAction(t, state, state.CurrentPlayer, tactic)
	if state.Pending == nil ||
		state.Pending.Kind != "footman" ||
		!reflect.DeepEqual(
			state.Pending.RemainingUnitIDs,
			[]string{second.UnitID},
		) {
		t.Fatalf("invalid footman continuation: %+v", state.Pending)
	}
	for _, action := range legalActions(state, state.CurrentPlayer) {
		if action.ActorUnitID != second.UnitID || !action.Free {
			t.Fatalf("invalid second footman action: %+v", action)
		}
	}
}

func TestWarriorPriestDrawIsTheOnlyUsableCoin(t *testing.T) {
	state := actionFixture(t, UnitPriest, UnitScout)
	priest := deployFixtureUnit(
		t,
		state,
		SideOak,
		UnitPriest,
		"-1,1",
		1,
	)
	state.Controls["-1,1"] = SideFalcon
	player := state.playerForSide(SideOak)
	giveHandCoin(t, state, player, UnitPriest)
	giveHandCoin(t, state, player, UnitPriest)
	control := findAction(
		t,
		legalActions(state, state.CurrentPlayer),
		ActionControl,
		func(action LegalAction) bool {
			return action.ActorUnitID == priest.UnitID
		},
	)
	applyAction(t, state, state.CurrentPlayer, control)
	if state.Pending == nil || state.Pending.Kind != "priest" {
		t.Fatalf("priest draw is missing: %+v", state.Pending)
	}
	drawn := state.Pending.CoinID
	for _, action := range legalActions(state, state.CurrentPlayer) {
		if action.CoinID != drawn {
			t.Fatalf("original hand coin remained usable: %+v", action)
		}
	}
}

func TestAIIsIndependentOfOpponentHiddenCoins(t *testing.T) {
	state := startedTestState(t)
	actions := legalActions(state, state.CurrentPlayer)
	first := bestAction(
		context.Background(),
		state,
		state.CurrentPlayer,
		actions,
	)
	opponent := state.Roster[otherPlayerID(state, state.CurrentPlayer)]
	opponent.Hand, opponent.Bag = opponent.Bag[:3], append(
		append([]string(nil), opponent.Hand...),
		opponent.Bag[3:]...,
	)
	second := bestAction(
		context.Background(),
		state,
		state.CurrentPlayer,
		legalActions(state, state.CurrentPlayer),
	)
	if first.ActionID != second.ActionID {
		t.Fatalf("AI decision changed with opponent hidden coins")
	}
}

func TestSeededGamesConserveEveryCoin(t *testing.T) {
	wins := 0
	for seed := uint64(1); seed <= 24; seed++ {
		state := startedStateWithSeed(t, seed*733)
		for step := 0; step < 500 && state.Status != StatusEnd; step++ {
			actions := legalActions(state, state.CurrentPlayer)
			if len(actions) == 0 {
				t.Fatalf("seed %d step %d has no action", seed, step)
			}
			action := bestAction(
				context.Background(),
				state,
				state.CurrentPlayer,
				actions,
			)
			if err := resolveAction(
				state,
				state.CurrentPlayer,
				action,
				true,
				"",
				time.Now().UTC(),
			); err != nil {
				t.Fatalf("seed %d step %d: %v", seed, step, err)
			}
			if err := validateCoinConservation(state); err != nil {
				t.Fatalf("seed %d step %d: %v", seed, step, err)
			}
		}
		if state.Status == StatusEnd {
			wins++
		}
	}
	if wins == 0 {
		t.Fatal("no seeded game reached a victory")
	}
}

func actionFixture(
	t *testing.T,
	attacker,
	defender UnitType,
) *State {
	t.Helper()
	state := lobbyTestState(t, 1)
	firstRoster, secondRoster := disjointRosters(attacker, defender)
	state.Roster[state.PlayerOrder[0]].Roster = firstRoster
	state.Roster[state.PlayerOrder[1]].Roster = secondRoster
	state.InitialPlayerID = state.PlayerOrder[0]
	state.InitialSide = state.Roster[state.PlayerOrder[0]].Side
	state.Status = StatusWaiting
	for _, playerID := range state.PlayerOrder {
		state.Roster[playerID].GameReady = true
	}
	setupGame(
		state,
		time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
	)
	normalizeFixtureInventory(state)
	state.CurrentPlayer = state.playerIDForSide(SideOak)
	state.InitiativePlayer = state.CurrentPlayer
	state.Status = StatusPlaying
	state.TurnDeadline = time.Time{}
	return state
}

func startedStateWithSeed(t *testing.T, seed uint64) *State {
	t.Helper()
	value, err := (&Plugin{}).InitRoom(
		context.Background(),
		game.RoomConfig{
			RoomID:        "simulation",
			OwnerPlayerID: "alice",
			MaxPlayers:    2,
			RandomSeed:    seed,
			RoundNo:       1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	state := value.(*State)
	applyTestCommand(t, state, "alice", "connect", nil)
	applyTestCommand(t, state, "bob", "connect", nil)
	applyTestCommand(t, state, "bob", "match_ready", map[string]any{
		"ready": true,
	})
	applyTestCommand(t, state, "alice", CommandSetSetupMode, map[string]any{
		"mode": SetupRandom,
	})
	applyTestCommand(t, state, "bob", "match_ready", map[string]any{
		"ready": true,
	})
	applyTestCommand(t, state, "alice", "match_begin", nil)
	applyTestCommand(t, state, "alice", "game_ready", nil)
	applyTestCommand(t, state, "bob", "game_ready", nil)
	return state
}

func normalizeFixtureInventory(state *State) {
	state.Board = map[string]*UnitStack{}
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		player.Bag = []string{}
		player.Hand = []string{}
		player.Discard = []Discard{}
		player.Supply = UnitCoins{}
		player.Lost = UnitCoins{}
		for coinID, unit := range state.Coins {
			if coinOwnerSeat(coinID) != player.SeatIndex {
				continue
			}
			if unit == UnitRoyal {
				player.Bag = append(player.Bag, coinID)
			} else {
				player.Supply[unit] = append(player.Supply[unit], coinID)
			}
		}
	}
}

func disjointRosters(first, second UnitType) ([]UnitType, []UnitType) {
	firstRoster := []UnitType{first}
	secondRoster := []UnitType{second}
	used := map[UnitType]bool{first: true, second: true}
	for _, unit := range unitOrder {
		if used[unit] {
			continue
		}
		if len(firstRoster) < 4 {
			firstRoster = append(firstRoster, unit)
			used[unit] = true
			continue
		}
		if len(secondRoster) < 4 {
			secondRoster = append(secondRoster, unit)
			used[unit] = true
		}
	}
	return firstRoster, secondRoster
}

func deployFixtureUnit(
	t *testing.T,
	state *State,
	side Side,
	unitType UnitType,
	cell string,
	count int,
) *UnitStack {
	t.Helper()
	player := state.playerForSide(side)
	state.UnitSerial++
	unit := &UnitStack{
		UnitID:   intString(state.UnitSerial) + "-" + string(unitType),
		UnitType: unitType,
		Side:     side,
		Cell:     cell,
	}
	for index := 0; index < count; index++ {
		coinID := removeUnitCoin(player.Supply, unitType)
		if coinID == "" {
			t.Fatalf("no %s supply coin", unitType)
		}
		unit.CoinIDs = append(unit.CoinIDs, coinID)
	}
	state.Board[cell] = unit
	return unit
}

func addBoardCoin(t *testing.T, state *State, unit *UnitStack) {
	t.Helper()
	player := state.playerForSide(unit.Side)
	coinID := removeUnitCoin(player.Supply, unit.UnitType)
	if coinID == "" {
		t.Fatalf("no %s supply coin", unit.UnitType)
	}
	unit.CoinIDs = append(unit.CoinIDs, coinID)
}

func giveHandCoin(
	t *testing.T,
	state *State,
	player *Player,
	unitType UnitType,
) {
	t.Helper()
	coinID := removeUnitCoin(player.Supply, unitType)
	if unitType == UnitRoyal {
		if len(player.Bag) == 0 {
			t.Fatal("royal coin is unavailable")
		}
		coinID = player.Bag[0]
		player.Bag = player.Bag[1:]
	}
	if coinID == "" {
		t.Fatalf("no %s coin for hand", unitType)
	}
	player.Hand = append(player.Hand, coinID)
}

func hasTarget(actions []LegalAction, unitID string) bool {
	for _, action := range actions {
		if action.TargetUnitID == unitID {
			return true
		}
	}
	return false
}

func coinOwnerSeat(coinID string) int {
	if len(coinID) >= 2 && coinID[0] == 's' {
		return int(coinID[1] - '0')
	}
	return -1
}
