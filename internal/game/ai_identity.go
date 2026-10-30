package game

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

const maxAIOrdinal = 999

var (
	ErrInvalidAIPlayerID = errors.New("invalid AI player ID")
	ErrAIPlayerIDInUse   = errors.New("AI player ID is already in use")
)

var canonicalAIPlayerID = regexp.MustCompile(`^ai_([0-9]{3})$`)

type AddAIRequest struct {
	SeatIndex       *int   `json:"seatIndex"`
	RestorePlayerID string `json:"restorePlayerID,omitempty"`
}

type AIIdentity struct {
	PlayerID    string
	DisplayName string
	Ordinal     int
}

func DecodeAddAIRequest(payload []byte) (AddAIRequest, error) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 || bytes.Equal(payload, []byte("null")) {
		return AddAIRequest{}, nil
	}
	var request AddAIRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return AddAIRequest{}, err
	}
	return request, nil
}

func NextAIIdentity(inUse func(string) bool, names []string) AIIdentity {
	for ordinal := 1; ordinal <= maxAIOrdinal; ordinal++ {
		identity := newAIIdentity(ordinal, names)
		if inUse == nil || !inUse(identity.PlayerID) {
			return identity
		}
	}
	return AIIdentity{}
}

func ResolveAIIdentity(
	requestedID string,
	inUse func(string) bool,
	names []string,
) (AIIdentity, error) {
	if requestedID == "" {
		identity := NextAIIdentity(inUse, names)
		if identity.PlayerID == "" {
			return AIIdentity{}, fmt.Errorf("%w: no IDs available", ErrAIPlayerIDInUse)
		}
		return identity, nil
	}
	ordinal, ok := aiOrdinal(requestedID)
	if !ok {
		return AIIdentity{}, fmt.Errorf("%w: %q", ErrInvalidAIPlayerID, requestedID)
	}
	if inUse != nil && inUse(requestedID) {
		return AIIdentity{}, fmt.Errorf("%w: %q", ErrAIPlayerIDInUse, requestedID)
	}
	return newAIIdentity(ordinal, names), nil
}

func AIName(playerID string, names []string) (string, bool) {
	ordinal, ok := aiOrdinal(playerID)
	if !ok {
		return "", false
	}
	return aiDisplayName(ordinal, names), true
}

func newAIIdentity(ordinal int, names []string) AIIdentity {
	return AIIdentity{
		PlayerID:    fmt.Sprintf("ai_%03d", ordinal),
		DisplayName: aiDisplayName(ordinal, names),
		Ordinal:     ordinal,
	}
}

func aiDisplayName(ordinal int, names []string) string {
	if ordinal <= len(names) && names[ordinal-1] != "" {
		return names[ordinal-1]
	}
	return fmt.Sprintf("AI %03d", ordinal)
}

func aiOrdinal(playerID string) (int, bool) {
	matches := canonicalAIPlayerID.FindStringSubmatch(playerID)
	if len(matches) != 2 {
		return 0, false
	}
	ordinal, err := strconv.Atoi(matches[1])
	if err != nil || ordinal < 1 || ordinal > maxAIOrdinal {
		return 0, false
	}
	return ordinal, true
}
