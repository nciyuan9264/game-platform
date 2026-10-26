package warchest

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

const (
	defaultRoomName       = "战争之匣"
	defaultTurnTimeout    = 90
	maxRoomNameCharacters = 18
	maxNoHumanChecks      = 3
)

func connect(state *State, playerID string, now time.Time) error {
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
		Roster:    []UnitType{},
		Bag:       []string{},
		Hand:      []string{},
		Discard:   []Discard{},
		Supply:    UnitCoins{},
		Lost:      UnitCoins{},
	}
	state.PlayerOrder = append(state.PlayerOrder, playerID)
	sortPlayerOrder(state)
	refreshSetup(state, now)
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

func addAI(state *State, actorID string, payload []byte, now time.Time) error {
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
		DisplayName: "渡鸦",
		Online:      true,
		Ready:       true,
		GameReady:   true,
		AI:          true,
		SeatIndex:   seatIndex,
		Roster:      []UnitType{},
		Bag:         []string{},
		Hand:        []string{},
		Discard:     []Discard{},
		Supply:      UnitCoins{},
		Lost:        UnitCoins{},
	}
	state.PlayerOrder = append(state.PlayerOrder, playerID)
	sortPlayerOrder(state)
	refreshSetup(state, now)
	return nil
}

func removePlayer(
	state *State,
	actorID string,
	payload []byte,
	now time.Time,
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
	refreshSetup(state, now)
	return value.PlayerID, nil
}

func updateRoomSettings(state *State, actorID string, payload []byte) error {
	if actorID != state.OwnerID || state.Status != StatusMatch {
		return errors.New("only the owner can update room settings")
	}
	var value struct {
		Visibility         string  `json:"visibility"`
		DisplayName        *string `json:"displayName"`
		TurnTimeoutSeconds *int    `json:"turnTimeoutSeconds"`
	}
	if err := decodePayload(payload, &value); err != nil {
		return err
	}
	if value.Visibility != contracts.VisibilityPublic &&
		value.Visibility != contracts.VisibilityPrivate {
		return errors.New("invalid visibility")
	}
	name := state.DisplayName
	if value.DisplayName != nil {
		name = strings.TrimSpace(*value.DisplayName)
		if size := len([]rune(name)); size < 1 || size > maxRoomNameCharacters {
			return errors.New("room name must contain 1 to 18 characters")
		}
	}
	if value.TurnTimeoutSeconds != nil &&
		*value.TurnTimeoutSeconds != defaultTurnTimeout {
		return errors.New("warchest turn timeout is fixed at 90 seconds")
	}
	state.Visibility = value.Visibility
	state.DisplayName = name
	state.TurnTimeoutSeconds = defaultTurnTimeout
	return nil
}

func setSetupMode(
	state *State,
	actorID string,
	payload []byte,
	now time.Time,
) error {
	if actorID != state.OwnerID || state.Status != StatusMatch {
		return errors.New("only the owner can select setup mode")
	}
	var value struct {
		Mode SetupMode `json:"mode"`
	}
	if err := decodePayload(payload, &value); err != nil {
		return err
	}
	if value.Mode != SetupRecommended &&
		value.Mode != SetupRandom &&
		value.Mode != SetupDraft {
		return errors.New("invalid setup mode")
	}
	state.SetupMode = value.Mode
	state.Draft = nil
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			continue
		}
		player.Roster = []UnitType{}
		if playerID != state.OwnerID && !player.AI {
			player.Ready = false
		}
	}
	refreshSetup(state, now)
	return nil
}

func draftUnit(
	state *State,
	playerID string,
	payload []byte,
	now time.Time,
) error {
	if state.Status != StatusMatch ||
		state.SetupMode != SetupDraft ||
		state.Draft == nil ||
		state.Draft.Complete {
		return errors.New("draft is not active")
	}
	if state.Draft.CurrentPickerID != playerID {
		return errors.New("player is not the current picker")
	}
	var value struct {
		UnitType UnitType `json:"unitType"`
	}
	if err := decodePayload(payload, &value); err != nil {
		return err
	}
	if !containsUnit(availableDraftUnits(state.Draft), value.UnitType) {
		return errors.New("draft unit is unavailable")
	}
	state.Draft.PickedUnits = append(state.Draft.PickedUnits, value.UnitType)
	state.Draft.Rosters[playerID] = append(
		state.Draft.Rosters[playerID],
		value.UnitType,
	)
	state.Draft.CompletedPickCount++
	if state.Draft.CompletedPickCount >= 8 {
		state.Draft.Complete = true
		state.Draft.CurrentPickerID = ""
		state.Draft.TurnDeadline = time.Time{}
		for _, currentID := range state.PlayerOrder {
			if player := state.Roster[currentID]; player != nil {
				player.Roster = copyUnits(state.Draft.Rosters[currentID])
			}
		}
		return nil
	}
	order := draftPickOrder(state.Draft)
	state.Draft.CurrentPickerID = order[state.Draft.CompletedPickCount]
	refreshDraftDeadline(state, now)
	return nil
}

func refreshSetup(state *State, now time.Time) {
	assignSides(state)
	if len(state.PlayerOrder) != 2 {
		state.Draft = nil
		state.InitialPlayerID = ""
		state.InitialSide = ""
		for _, player := range state.Roster {
			if player != nil {
				player.Roster = []UnitType{}
			}
		}
		return
	}
	switch state.SetupMode {
	case SetupRandom:
		setupRandomRosters(state)
	case SetupDraft:
		setupDraft(state, now)
	default:
		setupRecommendedRosters(state)
	}
}

func assignSides(state *State) {
	sortPlayerOrder(state)
	for index, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player == nil {
			continue
		}
		sideIndex := index
		if state.MatchRoundNo%2 == 0 {
			sideIndex = 1 - index
		}
		if sideIndex == 0 {
			player.Side = SideOak
		} else {
			player.Side = SideFalcon
		}
	}
}

func setupRecommendedRosters(state *State) {
	state.Draft = nil
	for _, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player != nil {
			player.Roster = copyUnits(recommendedRosters[player.Side])
		}
	}
	state.InitialSide = SideOak
	state.InitialPlayerID = state.playerIDForSide(SideOak)
}

func setupRandomRosters(state *State) {
	state.Draft = nil
	pool := shuffledUnits(unitOrder, setupSeed(state, 0x72616e646f6d))
	for index, playerID := range state.PlayerOrder {
		player := state.Roster[playerID]
		if player != nil {
			player.Roster = copyUnits(pool[index*4 : index*4+4])
		}
	}
	if setupSeed(state, 0x696e6974696174)&1 == 0 {
		state.InitialPlayerID = state.PlayerOrder[0]
	} else {
		state.InitialPlayerID = state.PlayerOrder[1]
	}
	state.InitialSide = state.sideForPlayer(state.InitialPlayerID)
}

func setupDraft(state *State, now time.Time) {
	if state.Draft == nil {
		pool := shuffledUnits(unitOrder, setupSeed(state, 0x6472616674))[:8]
		first := state.PlayerOrder[int(setupSeed(state, 0x7069636b6572)&1)]
		state.Draft = &DraftState{
			Pool:            copyUnits(pool),
			PickedUnits:     []UnitType{},
			Rosters:         map[string][]UnitType{},
			FirstPickerID:   first,
			CurrentPickerID: first,
		}
		for _, playerID := range state.PlayerOrder {
			state.Draft.Rosters[playerID] = []UnitType{}
			state.Roster[playerID].Roster = []UnitType{}
		}
	}
	order := draftPickOrder(state.Draft)
	if state.Draft.CompletedPickCount < len(order) {
		state.Draft.CurrentPickerID = order[state.Draft.CompletedPickCount]
	}
	second := otherPlayerID(state, state.Draft.FirstPickerID)
	state.InitialPlayerID = second
	state.InitialSide = state.sideForPlayer(second)
	refreshDraftDeadline(state, now)
}

func refreshDraftDeadline(state *State, now time.Time) {
	if state.Draft == nil ||
		state.Draft.Complete ||
		state.Draft.CurrentPickerID == "" {
		return
	}
	player := state.Roster[state.Draft.CurrentPickerID]
	if player == nil || player.AI {
		state.Draft.TurnDeadline = time.Time{}
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	state.Draft.TurnDeadline = now.UTC().Add(
		time.Duration(defaultTurnTimeout) * time.Second,
	)
}

func draftPickOrder(draft *DraftState) []string {
	if draft == nil {
		return nil
	}
	first := draft.FirstPickerID
	second := ""
	for playerID := range draft.Rosters {
		if playerID != first {
			second = playerID
			break
		}
	}
	return []string{
		first,
		second,
		second,
		first,
		first,
		second,
		second,
		first,
	}
}

func currentDraftPickCount(draft *DraftState) int {
	if draft == nil || draft.Complete {
		return 0
	}
	order := draftPickOrder(draft)
	index := draft.CompletedPickCount
	if index >= len(order) {
		return 0
	}
	count := 0
	for index < len(order) && order[index] == order[draft.CompletedPickCount] {
		count++
		index++
	}
	return count
}

func availableDraftUnits(draft *DraftState) []UnitType {
	if draft == nil {
		return []UnitType{}
	}
	picked := map[UnitType]bool{}
	for _, unit := range draft.PickedUnits {
		picked[unit] = true
	}
	result := make([]UnitType, 0, len(draft.Pool)-len(picked))
	for _, unit := range draft.Pool {
		if !picked[unit] {
			result = append(result, unit)
		}
	}
	return result
}

func setupComplete(state *State) bool {
	if len(state.PlayerOrder) != 2 {
		return false
	}
	first := state.Roster[state.PlayerOrder[0]]
	second := state.Roster[state.PlayerOrder[1]]
	if first == nil || second == nil {
		return false
	}
	if state.SetupMode == SetupDraft &&
		(state.Draft == nil || !state.Draft.Complete) {
		return false
	}
	return validRosters(first.Roster, second.Roster) &&
		state.InitialPlayerID != "" &&
		state.InitialSide != ""
}

func setupSeed(state *State, salt uint64) uint64 {
	value := state.RandomSeed ^
		uint64(state.MatchRoundNo)*0x9e3779b97f4a7c15 ^
		salt
	if value == 0 {
		value = 1
	}
	return value
}

func shuffledUnits(values []UnitType, seed uint64) []UnitType {
	result := copyUnits(values)
	random := seed
	for index := len(result) - 1; index > 0; index-- {
		random = random*1664525 + 1013904223
		target := int(random % uint64(index+1))
		result[index], result[target] = result[target], result[index]
	}
	return result
}

func otherPlayerID(state *State, playerID string) string {
	for _, currentID := range state.PlayerOrder {
		if currentID != playerID {
			return currentID
		}
	}
	return ""
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
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func decodePayload(data []byte, target any) error {
	if len(data) == 0 || string(data) == "null" {
		data = []byte("{}")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return errors.New("invalid command payload")
	}
	return nil
}
