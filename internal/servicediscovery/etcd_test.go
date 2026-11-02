package servicediscovery

import "testing"

func TestServiceNameUsesEnvironmentOverride(t *testing.T) {
	t.Setenv("AUTH_RPC_PSM", "pam.identity.rpc")
	got, err := ServiceName("AUTH_RPC_PSM", "pam.auth.rpc")
	if err != nil {
		t.Fatal(err)
	}
	if got != "pam.identity.rpc" {
		t.Fatalf("ServiceName() = %q, want pam.identity.rpc", got)
	}
}

func TestServiceNameUsesFallback(t *testing.T) {
	t.Setenv("AUTH_RPC_PSM", "")
	got, err := ServiceName("AUTH_RPC_PSM", "pam.auth.rpc")
	if err != nil {
		t.Fatal(err)
	}
	if got != "pam.auth.rpc" {
		t.Fatalf("ServiceName() = %q, want pam.auth.rpc", got)
	}
}

func TestServerOptionsUsesGameAdminPSMOverride(t *testing.T) {
	t.Setenv("GAME_ADMIN_RPC_PSM", "game.operations.rpc")
	t.Setenv("CONTROL_RPC_PSM", "game.platform.control")
	t.Setenv("ETCD_ENDPOINTS", "")
	_, got, err := ServerOptions("game.admin.rpc")
	if err != nil {
		t.Fatal(err)
	}
	if got != "game.operations.rpc" {
		t.Fatalf("ServerOptions() PSM = %q, want game.operations.rpc", got)
	}
}

func TestServiceNameRejectsNonThreeSegmentPSM(t *testing.T) {
	t.Setenv("AUTH_RPC_PSM", "pam.auth")
	if _, err := ServiceName("AUTH_RPC_PSM", "pam.auth.rpc"); err == nil {
		t.Fatal("ServiceName() succeeded for a two-segment PSM")
	}
}
