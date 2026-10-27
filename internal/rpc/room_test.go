package rpc

import (
	"errors"
	"testing"

	platform "github.com/nciyuan9264/game-platform-client/kitex_gen/pam/game/platform/v1"
	"github.com/nciyuan9264/game-platform/internal/contracts"
)

func TestCreateRoomTurnTimeoutSecondsAllowsFixedFugitiveTimeout(t *testing.T) {
	timeout := int32(120)
	got, err := createRoomTurnTimeoutSeconds(contracts.GameFugitive, &platform.CreateRoomReq{
		TurnTimeoutSeconds: &timeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != 120 {
		t.Fatalf("timeout = %d, want 120", got)
	}
}

func TestCreateRoomTurnTimeoutSecondsDefaultsFugitiveTimeout(t *testing.T) {
	got, err := createRoomTurnTimeoutSeconds(contracts.GameFugitive, &platform.CreateRoomReq{})
	if err != nil {
		t.Fatal(err)
	}
	if got != 120 {
		t.Fatalf("timeout = %d, want 120", got)
	}
}

func TestCreateRoomTurnTimeoutSecondsRejectsWrongFugitiveTimeout(t *testing.T) {
	timeout := int32(60)
	_, err := createRoomTurnTimeoutSeconds(contracts.GameFugitive, &platform.CreateRoomReq{
		TurnTimeoutSeconds: &timeout,
	})
	var platformErr interface{ GetCode() string }
	if !errors.As(err, &platformErr) || platformErr.GetCode() != "invalid_request" {
		t.Fatalf("error = %v, want invalid_request", err)
	}
}

func TestCreateRoomTurnTimeoutSecondsKeepsGenericTimeoutLimit(t *testing.T) {
	timeout := int32(120)
	_, err := createRoomTurnTimeoutSeconds(contracts.GameAcquire, &platform.CreateRoomReq{
		TurnTimeoutSeconds: &timeout,
	})
	var platformErr interface{ GetCode() string }
	if !errors.As(err, &platformErr) || platformErr.GetCode() != "invalid_request" {
		t.Fatalf("error = %v, want invalid_request", err)
	}
}
