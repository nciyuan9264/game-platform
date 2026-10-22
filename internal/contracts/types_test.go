package contracts

import (
	"encoding/json"
	"testing"
)

func TestOutboundMessageConnectionRefJSONCompatibility(t *testing.T) {
	msg := OutboundMessage{
		MessageID:      "m1",
		RoomID:         "r1",
		TargetPlayerID: "p1",
		TargetConn:     ConnectionRef{GatewayNode: "gateway-a", SessionID: "session-a"},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal outbound: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if raw["targetGatewayNode"] != "gateway-a" || raw["targetSessionID"] != "session-a" {
		t.Fatalf("legacy target fields missing: %s", data)
	}

	var legacy OutboundMessage
	if err := json.Unmarshal([]byte(`{"targetGatewayNode":"gateway-b","targetSessionID":"session-b"}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy outbound: %v", err)
	}
	if legacy.TargetConn.GatewayNode != "gateway-b" || legacy.TargetConn.SessionID != "session-b" {
		t.Fatalf("legacy target did not populate connection ref: %+v", legacy.TargetConn)
	}
}

func TestRoomCommandConnectionRefJSONCompatibility(t *testing.T) {
	cmd := RoomCommand{
		CommandID:     "c1",
		RoomID:        "r1",
		PlayerID:      "p1",
		ConnectionRef: ConnectionRef{GatewayNode: "gateway-a", SessionID: "session-a"},
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if raw["gatewayNode"] != "gateway-a" || raw["sessionID"] != "session-a" {
		t.Fatalf("legacy command fields missing: %s", data)
	}

	var legacy RoomCommand
	if err := json.Unmarshal([]byte(`{"gatewayNode":"gateway-b","sessionID":"session-b"}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy command: %v", err)
	}
	if legacy.ConnectionRef.GatewayNode != "gateway-b" || legacy.ConnectionRef.SessionID != "session-b" {
		t.Fatalf("legacy command did not populate connection ref: %+v", legacy.ConnectionRef)
	}
}
