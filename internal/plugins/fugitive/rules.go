package fugitive

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

const (
	defaultRoomName    = "午夜追缉"
	defaultTurnTimeout = 120
	maxNoHumanChecks   = 3
)

func ensureState(state *State) {
	if state.Roster == nil {
		state.Roster = map[string]*Player{}
	}
	if state.PlayerOrder == nil {
		state.PlayerOrder = []string{}
	}
	if state.Decks == nil {
		state.Decks = [][]Card{}
	}
	if state.Route == nil {
		state.Route = []Hideout{}
	}
	if state.Log == nil {
		state.Log = []Action{}
	}
	if state.OwnerRole != RoleMarshal {
		state.OwnerRole = RoleFugitive
	}
	state.DisplayName = defaultRoomName
	state.TurnTimeoutSeconds = defaultTurnTimeout
	for _, player := range state.Roster {
		if player != nil && player.Hand == nil {
			player.Hand = []Card{}
		}
	}
	assignRoles(state)
}

func connect(state *State, playerID string) error {
	if player := state.Roster[playerID]; player != nil {
		player.Online = true
		return nil
	}
	if state.Status != StatusMatch {
		return errors.New("game already started")
	}
	if len(state.Roster) >= state.MaxPlayers {
		return errors.New("room is full")
	}
	state.Roster[playerID] = &Player{
		UserID:    playerID,
		PlayerID:  playerID,
		Online:    true,
		Ready:     playerID == state.OwnerID,
		SeatIndex: firstFreeSeat(state),
		Hand:      []Card{},
	}
	state.PlayerOrder = append(state.PlayerOrder, playerID)
	sortPlayerOrder(state)
	assignRoles(state)
	return nil
}

func setReady(state *State, playerID string, payload []byte) error {
	if state.Status != StatusMatch {
		return errors.New("preparation phase has ended")
	}
	player := state.Roster[playerID]
	if player == nil {
		return errors.New("player not found")
	}
	var value struct {
		Ready *bool `json:"ready"`
	}
	if err := decodePayload(payload, &value); err != nil || value.Ready == nil {
		return errors.New("invalid ready payload")
	}
	if playerID == state.OwnerID && !*value.Ready {
		return errors.New("owner is always ready")
	}
	player.Ready = *value.Ready
	return nil
}

func addAI(state *State, actorID string, payload []byte) error {
	if actorID != state.OwnerID || state.Status != StatusMatch {
		return errors.New("only the owner can add AI during preparation")
	}
	if len(state.Roster) >= state.MaxPlayers {
		return errors.New("room is full")
	}
	seatIndex := firstFreeSeat(state)
	var value struct {
		SeatIndex *int `json:"seatIndex"`
	}
	if len(payload) > 0 && string(payload) != "null" {
		if err := json.Unmarshal(payload, &value); err != nil {
			return errors.New("invalid seat")
		}
	}
	if value.SeatIndex != nil {
		seatIndex = *value.SeatIndex
		if seatIndex < 0 ||
			seatIndex >= state.MaxPlayers ||
			seatOccupied(state, seatIndex) {
			return errors.New("seat is unavailable")
		}
	}
	playerID := fmt.Sprintf("ai_%d", seatIndex+1)
	for state.Roster[playerID] != nil {
		playerID += "_"
	}
	state.Roster[playerID] = &Player{
		PlayerID:    playerID,
		DisplayName: "灰狐",
		Online:      true,
		Ready:       true,
		GameReady:   true,
		AI:          true,
		SeatIndex:   seatIndex,
		Hand:        []Card{},
	}
	state.PlayerOrder = append(state.PlayerOrder, playerID)
	sortPlayerOrder(state)
	assignRoles(state)
	return nil
}

func removePlayer(
	state *State,
	actorID string,
	payload []byte,
) (string, error) {
	if actorID != state.OwnerID || state.Status != StatusMatch {
		return "", errors.New(
			"only the owner can remove players during preparation",
		)
	}
	var value struct {
		PlayerID string `json:"playerID"`
	}
	if err := decodePayload(payload, &value); err != nil ||
		value.PlayerID == "" ||
		value.PlayerID == state.OwnerID {
		return "", errors.New("invalid player")
	}
	if state.Roster[value.PlayerID] == nil {
		return "", errors.New("player not found")
	}
	delete(state.Roster, value.PlayerID)
	state.PlayerOrder = removeString(state.PlayerOrder, value.PlayerID)
	assignRoles(state)
	return value.PlayerID, nil
}

func updateRoomSettings(
	state *State,
	actorID string,
	payload []byte,
) error {
	if actorID != state.OwnerID || state.Status != StatusMatch {
		return errors.New("only the owner can update room settings")
	}
	var value struct {
		Visibility string `json:"visibility"`
	}
	if err := decodePayload(payload, &value); err != nil {
		return err
	}
	if value.Visibility != contracts.VisibilityPublic &&
		value.Visibility != contracts.VisibilityPrivate {
		return errors.New("invalid visibility")
	}
	state.Visibility = value.Visibility
	state.DisplayName = defaultRoomName
	state.TurnTimeoutSeconds = defaultTurnTimeout
	return nil
}

func selectRole(state *State, actorID string, payload []byte) error {
	if actorID != state.OwnerID || state.Status != StatusMatch {
		return errors.New("only the owner can select roles during preparation")
	}
	var value struct {
		Role Role `json:"role"`
	}
	if err := decodePayload(payload, &value); err != nil {
		return err
	}
	if value.Role != RoleFugitive && value.Role != RoleMarshal {
		return errors.New("invalid role")
	}
	state.OwnerRole = value.Role
	assignRoles(state)
	return nil
}

func beginMatch(state *State, actorID string) error {
	if actorID != state.OwnerID || state.Status != StatusMatch {
		return errors.New("only the owner can start the match")
	}
	if len(state.Roster) != 2 {
		return errors.New("fugitive requires two players")
	}
	for _, player := range state.Roster {
		if player == nil || !player.Ready || !player.Online {
			return errors.New("all players must be ready and online")
		}
		player.Ready = player.AI
		player.GameReady = player.AI
		player.Hand = []Card{}
	}
	assignRoles(state)
	if state.FugitivePlayerID == "" || state.MarshalPlayerID == "" {
		return errors.New("both roles are required")
	}
	setupGame(state)
	state.Status = StatusWaiting
	return nil
}

func gameReady(state *State, playerID string, sentAt time.Time) error {
	if state.Status != StatusWaiting {
		return nil
	}
	player := state.Roster[playerID]
	if player == nil {
		return errors.New("player not found")
	}
	player.Ready = true
	player.GameReady = true
	for _, current := range state.Roster {
		if current == nil || !current.GameReady {
			return nil
		}
	}
	if sentAt.IsZero() {
		sentAt = time.Now().UTC()
	}
	state.GameStartTime = sentAt.UTC()
	state.RoundNo = 1
	state.CurrentPlayer = state.FugitivePlayerID
	state.Status = StatusOpening
	startDeadline(state, sentAt)
	return nil
}

func setupGame(state *State) {
	state.RandomCounter = 0
	state.Decks = freshDecks(state)
	state.Route = []Hideout{{
		Card:     card(0),
		Revealed: true,
		Sprints:  []Card{},
	}}
	state.Log = []Action{}
	state.LastAction = nil
	state.Searches = 0
	state.WinnerID = ""
	state.WinnerRole = ""
	state.EndReason = ""
	state.CurrentPlayer = ""
	state.DrawsRemaining = 0
	state.OpeningHideouts = 0
	state.GameStartTime = time.Time{}
	state.TurnStartedAt = time.Time{}
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	fugitive := state.Roster[state.FugitivePlayerID]
	marshal := state.Roster[state.MarshalPlayerID]
	fugitive.Hand = []Card{card(1), card(2), card(3), card(42)}
	for index, count := range []int{3, 2} {
		for draw := 0; draw < count; draw++ {
			value, _ := drawFromDeck(state, index)
			fugitive.Hand = append(fugitive.Hand, value)
		}
	}
	sortHand(fugitive.Hand)
	marshal.Hand = []Card{}
}

func drawCard(
	state *State,
	playerID string,
	deckIndex int,
	automatic bool,
) error {
	if state.Status != StatusDraw || state.CurrentPlayer != playerID {
		return errors.New("not in the active draw phase")
	}
	player := state.Roster[playerID]
	if player == nil {
		return errors.New("player not found")
	}
	value, err := drawFromDeck(state, deckIndex)
	if err != nil {
		return err
	}
	player.Hand = append(player.Hand, value)
	sortHand(player.Hand)
	state.DrawsRemaining--
	deck := deckIndex
	numbers := []int{value.Number}
	appendAction(state, Action{
		RoundNo:         state.RoundNo,
		Kind:            "draw",
		PlayerID:        playerID,
		Role:            player.Role,
		DeckIndex:       &deck,
		Numbers:         numbers,
		Automatic:       automatic,
		PrivatePlayerID: playerID,
	})
	if state.DrawsRemaining <= 0 || !decksHaveCards(state) {
		enterActionPhase(state)
	}
	return nil
}

func playHideout(
	state *State,
	playerID string,
	hideoutNumber int,
	sprintNumbers []int,
	automatic bool,
) error {
	if (state.Status != StatusOpening &&
		state.Status != StatusFugitiveAction) ||
		state.CurrentPlayer != playerID ||
		playerID != state.FugitivePlayerID {
		return errors.New("not in the fugitive action phase")
	}
	player := state.Roster[playerID]
	hideout, sprints, err := validateMove(
		state,
		player,
		hideoutNumber,
		sprintNumbers,
	)
	if err != nil {
		return err
	}
	player.Hand = removeCards(
		player.Hand,
		append([]int{hideoutNumber}, sprintNumbers...),
	)
	revealed := hideoutNumber == 42
	state.Route = append(state.Route, Hideout{
		Card:     hideout,
		Revealed: revealed,
		Sprints:  sprints,
	})
	destination := hideoutNumber
	appendAction(state, Action{
		RoundNo:         state.RoundNo,
		Kind:            "playHideout",
		PlayerID:        playerID,
		Role:            RoleFugitive,
		Destination:     &destination,
		SprintCount:     len(sprints),
		Automatic:       automatic,
		PrivatePlayerID: playerID,
	})
	if state.Status == StatusOpening {
		state.OpeningHideouts++
		if state.OpeningHideouts >= 2 {
			startTurn(state, RoleMarshal, 2, time.Now().UTC())
		}
		return nil
	}
	if hideoutNumber == 42 {
		resolveEscape(state)
		return nil
	}
	startTurn(state, RoleMarshal, 1, time.Now().UTC())
	return nil
}

func finishOpening(
	state *State,
	playerID string,
	automatic bool,
) error {
	if state.Status != StatusOpening ||
		state.CurrentPlayer != playerID ||
		playerID != state.FugitivePlayerID ||
		state.OpeningHideouts < 1 {
		return errors.New("opening cannot finish")
	}
	appendAction(state, Action{
		RoundNo:   state.RoundNo,
		Kind:      "finishOpening",
		PlayerID:  playerID,
		Role:      RoleFugitive,
		Automatic: automatic,
	})
	startTurn(state, RoleMarshal, 2, time.Now().UTC())
	return nil
}

func layLow(state *State, playerID string, automatic bool) error {
	if state.Status != StatusFugitiveAction ||
		state.CurrentPlayer != playerID ||
		playerID != state.FugitivePlayerID {
		return errors.New("not in the fugitive action phase")
	}
	deckIndex := lowestNonEmptyDeck(state)
	var numbers []int
	var deck *int
	if deckIndex >= 0 {
		value, err := drawFromDeck(state, deckIndex)
		if err != nil {
			return err
		}
		player := state.Roster[playerID]
		player.Hand = append(player.Hand, value)
		sortHand(player.Hand)
		numbers = []int{value.Number}
		index := deckIndex
		deck = &index
	}
	appendAction(state, Action{
		RoundNo:         state.RoundNo,
		Kind:            "layLow",
		PlayerID:        playerID,
		Role:            RoleFugitive,
		DeckIndex:       deck,
		Numbers:         numbers,
		Automatic:       automatic,
		PrivatePlayerID: playerID,
	})
	startTurn(state, RoleMarshal, 1, time.Now().UTC())
	return nil
}

func searchHideouts(
	state *State,
	playerID string,
	numbers []int,
	automatic bool,
) error {
	manhunt := state.Status == StatusManhunt
	if (state.Status != StatusMarshalSearch && !manhunt) ||
		state.CurrentPlayer != playerID ||
		playerID != state.MarshalPlayerID {
		return errors.New("not in the marshal search phase")
	}
	if len(numbers) == 0 || manhunt && len(numbers) != 1 {
		return errors.New("invalid search count")
	}
	known := knownNumbers(state, playerID)
	selected := make(map[int]bool, len(numbers))
	for _, number := range numbers {
		if number <= 0 || number >= 42 ||
			selected[number] ||
			known[number] {
			return errors.New("invalid search number")
		}
		selected[number] = true
	}
	hidden := make(map[int]int)
	for index, hideout := range state.Route {
		if !hideout.Revealed {
			hidden[hideout.Card.Number] = index
		}
	}
	success := true
	for _, number := range numbers {
		if _, ok := hidden[number]; !ok {
			success = false
			break
		}
	}
	if success {
		for _, number := range numbers {
			index := hidden[number]
			state.Route[index].Revealed = true
		}
	}
	state.Searches++
	result := success
	appendAction(state, Action{
		RoundNo:   state.RoundNo,
		Kind:      "search",
		PlayerID:  playerID,
		Role:      RoleMarshal,
		Numbers:   append([]int(nil), numbers...),
		Success:   &result,
		Automatic: automatic,
	})
	if success && hiddenHideoutCount(state) == 0 {
		finishGame(
			state,
			state.MarshalPlayerID,
			RoleMarshal,
			"all_hideouts_revealed",
		)
		return nil
	}
	if manhunt {
		if !success {
			finishGame(
				state,
				state.FugitivePlayerID,
				RoleFugitive,
				"manhunt_missed",
			)
		} else {
			startDeadline(state, time.Now().UTC())
		}
		return nil
	}
	state.RoundNo++
	startTurn(state, RoleFugitive, 1, time.Now().UTC())
	return nil
}

func validateMove(
	state *State,
	player *Player,
	hideoutNumber int,
	sprintNumbers []int,
) (Card, []Card, error) {
	if player == nil {
		return Card{}, nil, errors.New("player not found")
	}
	last := lastHideoutNumber(state)
	if hideoutNumber <= last {
		return Card{}, nil, errors.New("hideout must increase")
	}
	hand := make(map[int]Card, len(player.Hand))
	for _, value := range player.Hand {
		hand[value.Number] = value
	}
	hideout, ok := hand[hideoutNumber]
	if !ok {
		return Card{}, nil, errors.New("hideout is not in hand")
	}
	seen := map[int]bool{hideoutNumber: true}
	sprints := make([]Card, 0, len(sprintNumbers))
	reach := 3
	for _, number := range sprintNumbers {
		value, exists := hand[number]
		if !exists || seen[number] || number == 42 {
			return Card{}, nil, errors.New("invalid sprint card")
		}
		seen[number] = true
		reach += value.Sprint
		sprints = append(sprints, value)
	}
	if hideoutNumber-last > reach {
		return Card{}, nil, errors.New("sprint value is insufficient")
	}
	sort.Slice(sprints, func(i, j int) bool {
		return sprints[i].Number < sprints[j].Number
	})
	return hideout, sprints, nil
}

func resolveEscape(state *State) {
	if highestRevealed(state) >= 30 {
		finishGame(
			state,
			state.FugitivePlayerID,
			RoleFugitive,
			"escaped",
		)
		return
	}
	state.Status = StatusManhunt
	state.CurrentPlayer = state.MarshalPlayerID
	state.DrawsRemaining = 0
	startDeadline(state, time.Now().UTC())
}

func startTurn(
	state *State,
	role Role,
	draws int,
	now time.Time,
) {
	state.CurrentPlayer = playerForRole(state, role)
	state.DrawsRemaining = draws
	state.TurnStartedAt = now.UTC()
	if state.DrawsRemaining > 0 && decksHaveCards(state) {
		state.Status = StatusDraw
	} else {
		enterActionPhase(state)
	}
	startDeadline(state, now)
}

func enterActionPhase(state *State) {
	state.DrawsRemaining = 0
	if roleForPlayer(state, state.CurrentPlayer) == RoleMarshal {
		state.Status = StatusMarshalSearch
	} else {
		state.Status = StatusFugitiveAction
	}
}

func startDeadline(state *State, now time.Time) {
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
	if state.Status == StatusMatch ||
		state.Status == StatusWaiting ||
		state.Status == StatusEnd ||
		state.CurrentPlayer == "" {
		return
	}
	player := state.Roster[state.CurrentPlayer]
	if player == nil || player.AI || state.TurnTimeoutSeconds == 0 {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	state.TurnStartedAt = now.UTC()
	state.TurnTimeout = time.Duration(state.TurnTimeoutSeconds) * time.Second
	state.TurnDeadline = state.TurnStartedAt.Add(state.TurnTimeout)
}

func finishGame(
	state *State,
	winnerID string,
	winnerRole Role,
	reason string,
) {
	state.Status = StatusEnd
	state.WinnerID = winnerID
	state.WinnerRole = winnerRole
	state.EndReason = reason
	state.CurrentPlayer = ""
	state.DrawsRemaining = 0
	state.TurnStartedAt = time.Time{}
	state.TurnDeadline = time.Time{}
	state.TurnTimeout = 0
}

func appendAction(state *State, action Action) {
	action.ID = len(state.Log) + 1
	copyAction := cloneAction(action)
	state.Log = append(state.Log, copyAction)
	state.LastAction = &copyAction
}

func assignRoles(state *State) {
	state.FugitivePlayerID = ""
	state.MarshalPlayerID = ""
	for _, player := range state.Roster {
		if player != nil {
			player.Role = ""
		}
	}
	owner := state.Roster[state.OwnerID]
	if owner == nil {
		return
	}
	owner.Role = state.OwnerRole
	if owner.Role == RoleFugitive {
		state.FugitivePlayerID = owner.PlayerID
	} else {
		state.MarshalPlayerID = owner.PlayerID
	}
	for _, playerID := range state.PlayerOrder {
		if playerID == state.OwnerID {
			continue
		}
		player := state.Roster[playerID]
		if player == nil {
			continue
		}
		player.Role = otherRole(state.OwnerRole)
		if player.Role == RoleFugitive {
			state.FugitivePlayerID = playerID
		} else {
			state.MarshalPlayerID = playerID
		}
	}
}

func knownNumbers(state *State, playerID string) map[int]bool {
	result := map[int]bool{0: true, 42: true}
	if player := state.Roster[playerID]; player != nil {
		for _, value := range player.Hand {
			result[value.Number] = true
		}
	}
	for _, hideout := range state.Route {
		if !hideout.Revealed {
			continue
		}
		result[hideout.Card.Number] = true
		for _, sprint := range hideout.Sprints {
			result[sprint.Number] = true
		}
	}
	return result
}

func highestRevealed(state *State) int {
	result := 0
	for _, hideout := range state.Route {
		if hideout.Revealed &&
			hideout.Card.Number != 42 &&
			hideout.Card.Number > result {
			result = hideout.Card.Number
		}
	}
	return result
}

func hiddenHideoutCount(state *State) int {
	result := 0
	for _, hideout := range state.Route {
		if !hideout.Revealed {
			result++
		}
	}
	return result
}

func lastHideoutNumber(state *State) int {
	if len(state.Route) == 0 {
		return 0
	}
	return state.Route[len(state.Route)-1].Card.Number
}

func playerForRole(state *State, role Role) string {
	if role == RoleMarshal {
		return state.MarshalPlayerID
	}
	return state.FugitivePlayerID
}

func roleForPlayer(state *State, playerID string) Role {
	if player := state.Roster[playerID]; player != nil {
		return player.Role
	}
	return ""
}

func decksHaveCards(state *State) bool {
	return lowestNonEmptyDeck(state) >= 0
}

func removeCards(hand []Card, numbers []int) []Card {
	remove := make(map[int]bool, len(numbers))
	for _, number := range numbers {
		remove[number] = true
	}
	result := make([]Card, 0, len(hand)-len(numbers))
	for _, value := range hand {
		if !remove[value.Number] {
			result = append(result, value)
		}
	}
	return result
}

func firstFreeSeat(state *State) int {
	for seat := 0; seat < state.MaxPlayers; seat++ {
		if !seatOccupied(state, seat) {
			return seat
		}
	}
	return len(state.Roster)
}

func seatOccupied(state *State, seat int) bool {
	for _, player := range state.Roster {
		if player != nil && player.SeatIndex == seat {
			return true
		}
	}
	return false
}

func sortPlayerOrder(state *State) {
	sort.SliceStable(state.PlayerOrder, func(i, j int) bool {
		left := state.Roster[state.PlayerOrder[i]]
		right := state.Roster[state.PlayerOrder[j]]
		if left == nil || right == nil {
			return state.PlayerOrder[i] < state.PlayerOrder[j]
		}
		if left.SeatIndex != right.SeatIndex {
			return left.SeatIndex < right.SeatIndex
		}
		return left.PlayerID < right.PlayerID
	})
}

func removeString(values []string, target string) []string {
	result := values[:0]
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func decodePayload(payload []byte, target any) error {
	if len(payload) == 0 || string(payload) == "null" {
		payload = []byte("{}")
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return errors.New("invalid command payload")
	}
	return nil
}
