package main

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
)

func TestRunCLITuneOutputsTuningSummary(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runCLI([]string{
		"-tune",
		"-candidates=1",
		"-players=4",
		"-games=1",
		"-seed=7",
		"-depth=1",
		"-beam=2",
		"-max-turns=1",
		"-json",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runCLI returned error: %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"baselineName": "online"`) {
		t.Fatalf("stdout missing tuning baseline: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"best"`) {
		t.Fatalf("stdout missing best result: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"players": 4`) {
		t.Fatalf("stdout missing tuning player count: %s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "tuning progress: 1/1") {
		t.Fatalf("stderr missing tuning progress: %s", stderr.String())
	}
	if strings.Contains(stdout.String(), "tuning progress") {
		t.Fatalf("stdout should not contain tuning progress: %s", stdout.String())
	}
}

func TestRunCLITuneSuppressesStructuredSimulationLogs(t *testing.T) {
	var stdout, stderr, processLog bytes.Buffer
	log.SetOutput(&processLog)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	err := runCLI([]string{
		"-tune",
		"-candidates=1",
		"-games=1",
		"-seed=7",
		"-depth=1",
		"-beam=2",
		"-max-turns=2",
		"-json",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runCLI returned error: %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "tuning progress: 1/1") {
		t.Fatalf("stderr missing tuning progress: %s", stderr.String())
	}
	if processLog.Len() != 0 {
		t.Fatalf("simulation logs should be suppressed, got: %s", processLog.String())
	}
}

func TestRunCLIArenaSelectsNamedWeights(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runCLI([]string{
		"-candidate=controlFocused",
		"-baseline=online",
		"-players=4",
		"-games=1",
		"-seed=7",
		"-depth=1",
		"-beam=2",
		"-max-turns=1",
		"-json",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runCLI returned error: %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"games": 1`) {
		t.Fatalf("stdout missing arena result: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"players": 4`) {
		t.Fatalf("stdout missing player count: %s", stdout.String())
	}
}

func TestRunCLITuneSupportsGridCandidates(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runCLI([]string{
		"-tune",
		"-grid",
		"-grid-limit=2",
		"-players=3",
		"-games=1",
		"-seed=7",
		"-depth=1",
		"-beam=2",
		"-max-turns=1",
		"-json",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runCLI returned error: %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "tuning progress: 2/2") {
		t.Fatalf("stderr missing grid progress total: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), `"name": "grid-balanced-`) {
		t.Fatalf("stdout missing grid candidate name: %s", stdout.String())
	}
}
