package platformapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoadtestAuthorizationRequiresExplicitStrongToken(t *testing.T) {
	req := httptest.NewRequest("GET", "/platform/__loadtest/acquire/stats", nil)

	t.Setenv("ACQUIRE_LOADTEST_ENABLED", "false")
	t.Setenv("ACQUIRE_LOADTEST_TOKEN", strings.Repeat("a", minLoadtestTokenLength))
	req.Header.Set("X-Loadtest-Token", strings.Repeat("a", minLoadtestTokenLength))
	if loadtestAuthorized(req) {
		t.Fatal("disabled loadtest endpoint accepted a token")
	}

	t.Setenv("ACQUIRE_LOADTEST_ENABLED", "true")
	t.Setenv("ACQUIRE_LOADTEST_TOKEN", "local-test-token")
	req.Header.Set("X-Loadtest-Token", "local-test-token")
	if loadtestAuthorized(req) {
		t.Fatal("weak loadtest token was accepted")
	}

	strongToken := strings.Repeat("b", minLoadtestTokenLength)
	t.Setenv("ACQUIRE_LOADTEST_TOKEN", strongToken)
	req.Header.Set("X-Loadtest-Token", strongToken)
	if !loadtestAuthorized(req) {
		t.Fatal("explicit strong loadtest token was rejected")
	}
}
