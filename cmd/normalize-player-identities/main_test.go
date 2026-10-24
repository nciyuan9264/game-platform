package main

import "testing"

func TestParseOptions(t *testing.T) {
	options, err := parseOptions([]string{
		"--dry-run",
		"--game-type=acquire",
		"--after-id=42",
		"--batch-size=200",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !options.DryRun ||
		options.VerifyOnly ||
		options.GameType != "acquire" ||
		options.AfterID != 42 ||
		options.BatchSize != 200 {
		t.Fatalf("options = %+v", options)
	}
}

func TestParseOptionsRejectsConflictsAndInvalidRanges(t *testing.T) {
	for _, args := range [][]string{
		{"--dry-run", "--verify-only"},
		{"--after-id=-1"},
		{"--batch-size=0"},
		{"--batch-size=1001"},
		{"--game-type=unknown"},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Fatalf("parseOptions(%v) succeeded", args)
		}
	}
}
