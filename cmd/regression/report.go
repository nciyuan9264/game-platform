package main

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	checkPassed  = "passed"
	checkFailed  = "failed"
	checkSkipped = "skipped"
)

type checkResult struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"durationMs"`
	Message    string `json:"message,omitempty"`
}

type gameResult struct {
	GameType string        `json:"gameType"`
	Request  string        `json:"requestID"`
	RoomID   string        `json:"roomID,omitempty"`
	MatchID  string        `json:"matchID,omitempty"`
	Checks   []checkResult `json:"checks"`
}

type regressionReport struct {
	RunID      string        `json:"runID"`
	Mode       string        `json:"mode"`
	HTTPBase   string        `json:"httpBase"`
	WSBase     string        `json:"wsBase,omitempty"`
	StartedAt  time.Time     `json:"startedAt"`
	FinishedAt time.Time     `json:"finishedAt"`
	Status     string        `json:"status"`
	Checks     []checkResult `json:"checks"`
	Games      []gameResult  `json:"games,omitempty"`
}

func (r *regressionReport) finish() {
	r.FinishedAt = time.Now().UTC()
	r.Status = checkPassed
	for _, check := range r.Checks {
		if check.Status == checkFailed {
			r.Status = checkFailed
		}
	}
	for _, game := range r.Games {
		for _, check := range game.Checks {
			if check.Status == checkFailed {
				r.Status = checkFailed
			}
		}
	}
}

func (r regressionReport) failed() bool {
	return r.Status == checkFailed
}

func writeReport(dir string, report regressionReport) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "report.md"), []byte(markdownReport(report)), 0o644)
}

func markdownReport(report regressionReport) string {
	var output strings.Builder
	fmt.Fprintf(&output, "# Game Platform Regression %s\n\n", html.EscapeString(report.RunID))
	fmt.Fprintf(&output, "- Mode: `%s`\n", report.Mode)
	fmt.Fprintf(&output, "- Status: `%s`\n", report.Status)
	fmt.Fprintf(&output, "- Started: `%s`\n", report.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(&output, "- Finished: `%s`\n\n", report.FinishedAt.Format(time.RFC3339))
	writeMarkdownChecks(&output, "Global Checks", report.Checks)
	for _, game := range report.Games {
		title := fmt.Sprintf("%s (%s)", game.GameType, game.RoomID)
		writeMarkdownChecks(&output, title, game.Checks)
	}
	return output.String()
}

func writeMarkdownChecks(output *strings.Builder, title string, checks []checkResult) {
	fmt.Fprintf(output, "## %s\n\n", html.EscapeString(title))
	output.WriteString("| Check | Status | Duration | Message |\n")
	output.WriteString("| --- | --- | ---: | --- |\n")
	for _, check := range checks {
		message := strings.ReplaceAll(check.Message, "|", "\\|")
		fmt.Fprintf(
			output,
			"| %s | `%s` | %d ms | %s |\n",
			html.EscapeString(check.Name),
			check.Status,
			check.DurationMS,
			html.EscapeString(message),
		)
	}
	output.WriteString("\n")
}

func runCheck(name string, fn func() error) checkResult {
	started := time.Now()
	err := fn()
	result := checkResult{Name: name, Status: checkPassed, DurationMS: time.Since(started).Milliseconds()}
	if err != nil {
		result.Status = checkFailed
		result.Message = err.Error()
	}
	return result
}

func skippedCheck(name, message string) checkResult {
	return checkResult{Name: name, Status: checkSkipped, Message: message}
}
