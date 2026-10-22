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

func TestServiceNameRejectsNonThreeSegmentPSM(t *testing.T) {
	t.Setenv("AUTH_RPC_PSM", "pam.auth")
	if _, err := ServiceName("AUTH_RPC_PSM", "pam.auth.rpc"); err == nil {
		t.Fatal("ServiceName() succeeded for a two-segment PSM")
	}
}
