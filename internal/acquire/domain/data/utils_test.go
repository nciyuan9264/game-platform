package data

import (
	"os"
	"strings"
	"testing"
)

func TestUtilsUsesMathRandV2InsteadOfExpRand(t *testing.T) {
	src, err := os.ReadFile("utils.go")
	if err != nil {
		t.Fatalf("read utils.go: %v", err)
	}
	if strings.Contains(string(src), "golang.org/x/exp/rand") {
		t.Fatal("utils.go should use math/rand/v2 instead of golang.org/x/exp/rand")
	}
}
