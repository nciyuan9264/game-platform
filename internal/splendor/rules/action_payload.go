package rules

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// GemTakePayload is the structured take-and-return command payload.
// Legacy payloads encode Take directly as a color-to-count object.
type GemTakePayload struct {
	Take    map[string]int `json:"take"`
	Returns map[string]int `json:"returns,omitempty"`
	Legacy  bool           `json:"-"`
}

// ReserveCardPayload is the structured public-reserve command payload.
// Legacy payloads encode only the numeric card ID.
type ReserveCardPayload struct {
	CardID  int            `json:"cardID"`
	Returns map[string]int `json:"returns,omitempty"`
	Legacy  bool           `json:"-"`
}

func parseGemTakePayload(data json.RawMessage) (GemTakePayload, *ActionError) {
	var fields map[string]json.RawMessage
	if err := decodeJSON(data, &fields, false); err != nil {
		return GemTakePayload{}, actionError("invalid_payload", "宝石选择格式无效")
	}

	if _, structured := fields["take"]; structured || fields["returns"] != nil {
		var payload GemTakePayload
		if err := decodeJSON(data, &payload, true); err != nil || payload.Take == nil {
			return GemTakePayload{}, actionError("invalid_payload", "宝石选择格式无效")
		}
		return payload, nil
	}

	var take map[string]int
	if err := decodeJSON(data, &take, false); err != nil || take == nil {
		return GemTakePayload{}, actionError("invalid_payload", "宝石选择格式无效")
	}
	return GemTakePayload{Take: take, Legacy: true}, nil
}

func parseReserveCardPayload(data json.RawMessage) (ReserveCardPayload, *ActionError) {
	var legacyCardID int
	if err := decodeJSON(data, &legacyCardID, false); err == nil {
		if legacyCardID <= 0 {
			return ReserveCardPayload{}, actionError("invalid_payload", "发展卡编号无效")
		}
		return ReserveCardPayload{CardID: legacyCardID, Legacy: true}, nil
	}

	var payload ReserveCardPayload
	if err := decodeJSON(data, &payload, true); err != nil || payload.CardID <= 0 {
		return ReserveCardPayload{}, actionError("invalid_payload", "发展卡编号无效")
	}
	return payload, nil
}

func decodeJSON(data []byte, target interface{}, disallowUnknown bool) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if disallowUnknown {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
