package history

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const MaxReplayFrameBytes = 8 << 20

var (
	ErrReplayMaterializationMissing = errors.New("replay materialization is missing")
	ErrReplayPayloadCorrupt         = errors.New("replay payload is corrupt")
	ErrReplayPayloadTooLarge        = errors.New("replay payload exceeds the decoded size limit")
	ErrReplaySchemaUnsupported      = errors.New("replay schema is unsupported")
)

type ReplayFrame struct {
	RoomData    json.RawMessage `json:"roomData"`
	PlayersData json.RawMessage `json:"playersData"`
	PrivateData json.RawMessage `json:"privateData,omitempty"`
	Deck        json.RawMessage `json:"deck,omitempty"`
	Reserve     json.RawMessage `json:"reserve,omitempty"`
	Result      json.RawMessage `json:"result"`
}

func EncodeReplayFrame(schemaVersion int, frame any) ([]byte, error) {
	if schemaVersion <= 0 {
		return nil, fmt.Errorf("%w: %d", ErrReplaySchemaUnsupported, schemaVersion)
	}
	encoded, err := json.Marshal(frame)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxReplayFrameBytes {
		return nil, ErrReplayPayloadTooLarge
	}
	if !json.Valid(encoded) {
		return nil, ErrReplayPayloadCorrupt
	}
	var validated ReplayFrame
	if err := json.Unmarshal(encoded, &validated); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrReplayPayloadCorrupt, err)
	}
	if len(validated.RoomData) == 0 ||
		len(validated.PlayersData) == 0 ||
		len(validated.Result) == 0 {
		return nil, ErrReplayPayloadCorrupt
	}

	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	if _, err := writer.Write(encoded); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func DecodeReplayFrame(
	schemaVersion int,
	payload []byte,
) (ReplayFrame, error) {
	if len(payload) == 0 {
		return ReplayFrame{}, ErrReplayMaterializationMissing
	}
	if schemaVersion != 1 && schemaVersion != 2 {
		return ReplayFrame{}, fmt.Errorf(
			"%w: %d",
			ErrReplaySchemaUnsupported,
			schemaVersion,
		)
	}
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return ReplayFrame{}, fmt.Errorf("%w: %v", ErrReplayPayloadCorrupt, err)
	}
	defer reader.Close()

	decoded, err := io.ReadAll(io.LimitReader(reader, MaxReplayFrameBytes+1))
	if err != nil {
		return ReplayFrame{}, fmt.Errorf("%w: %v", ErrReplayPayloadCorrupt, err)
	}
	if len(decoded) > MaxReplayFrameBytes {
		return ReplayFrame{}, ErrReplayPayloadTooLarge
	}
	if !json.Valid(decoded) {
		return ReplayFrame{}, ErrReplayPayloadCorrupt
	}
	var frame ReplayFrame
	if err := json.Unmarshal(decoded, &frame); err != nil {
		return ReplayFrame{}, fmt.Errorf("%w: %v", ErrReplayPayloadCorrupt, err)
	}
	if len(frame.RoomData) == 0 ||
		len(frame.PlayersData) == 0 ||
		len(frame.Result) == 0 {
		return ReplayFrame{}, ErrReplayPayloadCorrupt
	}
	return frame, nil
}

func MaterializedReplayJSON(
	schemaVersion int,
	payload []byte,
	seq,
	totalEvents int,
	event *Event,
	players []Player,
) ([]byte, error) {
	frame, err := DecodeReplayFrame(schemaVersion, payload)
	if err != nil {
		return nil, err
	}
	roomData, err := replayRoomDataWithProfiles(frame.RoomData, players)
	if err != nil {
		return nil, err
	}
	envelope := map[string]any{
		"seq":         seq,
		"totalEvents": totalEvents,
		"roomData":    roomData,
		"playersData": frame.PlayersData,
		"result":      frame.Result,
	}
	if len(frame.PrivateData) > 0 {
		envelope["privateData"] = frame.PrivateData
	}
	if len(frame.Deck) > 0 {
		envelope["deck"] = frame.Deck
	}
	if len(frame.Reserve) > 0 {
		envelope["reserve"] = frame.Reserve
	}
	if event != nil {
		currentEvent := map[string]any{
			"seq":      event.Seq,
			"playerID": event.PlayerID,
			"cmdType":  event.CommandType,
		}
		if event.DisplayName != "" {
			currentEvent["displayName"] = event.DisplayName
		}
		if len(event.Payload) > 0 {
			currentEvent["payloadJSON"] = string(event.Payload)
		}
		envelope["currentEvent"] = currentEvent
	}
	return json.Marshal(envelope)
}

func replayRoomDataWithProfiles(raw json.RawMessage, players []Player) (json.RawMessage, error) {
	if len(players) == 0 {
		return raw, nil
	}
	var roomData map[string]any
	if err := json.Unmarshal(raw, &roomData); err != nil {
		return nil, fmt.Errorf("%w: room data: %v", ErrReplayPayloadCorrupt, err)
	}
	replayPlayers, _ := roomData["players"].(map[string]any)
	if replayPlayers == nil {
		replayPlayers = make(map[string]any, len(players))
	}
	for _, player := range players {
		sourcePlayerID := player.SourcePlayerID
		if sourcePlayerID == "" {
			sourcePlayerID = player.PlayerID
		}
		value := replayPlayers[sourcePlayerID]
		if value == nil {
			value = replayPlayers[player.PlayerID]
		}
		profile, _ := value.(map[string]any)
		if profile == nil {
			profile = map[string]any{}
		}
		if sourcePlayerID != player.PlayerID {
			delete(replayPlayers, sourcePlayerID)
		}
		profile["playerID"] = player.PlayerID
		profile["displayName"] = player.DisplayName
		if player.UserID != nil {
			profile["userID"] = fmt.Sprintf("%d", *player.UserID)
		} else {
			delete(profile, "userID")
		}
		replayPlayers[player.PlayerID] = profile
	}
	roomData["players"] = replayPlayers
	encoded, err := json.Marshal(roomData)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}
