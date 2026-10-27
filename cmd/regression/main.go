package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

var supportedGames = []string{
	"acquire",
	"davinci",
	"splendor",
	"yinsh",
	"othello",
	"fugitive",
	"warchest",
	"quoridor",
	"hex",
	"loveletter",
	"sixnimmt",
	"forsale",
}

type config struct {
	Mode        string
	Games       []string
	RunID       string
	ReportDir   string
	HTTPBase    string
	WSBase      string
	FrontendURL string
	Origin      string
	AccessToken string
	PostgresDSN string
	NATSURL     string
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	cfg, err := parseConfig(args)
	if err != nil {
		return err
	}
	report := regressionReport{
		RunID:     cfg.RunID,
		Mode:      cfg.Mode,
		HTTPBase:  cfg.HTTPBase,
		WSBase:    cfg.WSBase,
		StartedAt: time.Now().UTC(),
	}
	api := newAPIClient(cfg)

	switch cfg.Mode {
	case "probe":
		report.Checks = api.runProbe(ctx, cfg.Games)
	case "canary":
		if cfg.AccessToken == "" {
			report.Checks = append(report.Checks, checkResult{
				Name:    "canary credentials",
				Status:  checkFailed,
				Message: "REGRESSION_ACCESS_TOKEN is required",
			})
			break
		}
		verifier, verifierErr := newStorageVerifier(ctx, cfg)
		if verifierErr != nil {
			report.Checks = append(report.Checks, checkResult{
				Name:    "storage verifier",
				Status:  checkFailed,
				Message: verifierErr.Error(),
			})
			break
		}
		defer verifier.Close()
		for _, gameType := range cfg.Games {
			report.Games = append(report.Games, runCanary(ctx, cfg, api, verifier, gameType))
		}
	default:
		return fmt.Errorf("unsupported mode %q", cfg.Mode)
	}

	report.finish()
	if err := writeReport(cfg.ReportDir, report); err != nil {
		return fmt.Errorf("write regression report: %w", err)
	}
	fmt.Printf("regression report: %s\n", filepath.Join(cfg.ReportDir, "report.md"))
	if report.failed() {
		return errors.New("regression failed")
	}
	return nil
}

func parseConfig(args []string) (config, error) {
	flags := flag.NewFlagSet("regression", flag.ContinueOnError)
	mode := flags.String("mode", "probe", "probe or canary")
	games := flags.String("games", strings.Join(supportedGames, ","), "comma-separated game types")
	reportDir := flags.String("report-dir", "", "report output directory")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	runID := time.Now().UTC().Format("20060102T150405Z") + "-" + uuid.NewString()[:8]
	cfg := config{
		Mode:        strings.TrimSpace(*mode),
		RunID:       runID,
		ReportDir:   strings.TrimSpace(*reportDir),
		HTTPBase:    envOrDefault("REGRESSION_HTTP_BASE", "https://api.gamebus.online/pam-api"),
		WSBase:      envOrDefault("REGRESSION_WS_BASE", "wss://api.gamebus.online/ws"),
		FrontendURL: envOrDefault("REGRESSION_FRONTEND_URL", "https://board.gamebus.online/"),
		Origin:      envOrDefault("REGRESSION_ORIGIN", "https://board.gamebus.online"),
		AccessToken: strings.TrimSpace(os.Getenv("REGRESSION_ACCESS_TOKEN")),
		PostgresDSN: strings.TrimSpace(os.Getenv("REGRESSION_POSTGRES_DSN")),
		NATSURL:     strings.TrimSpace(os.Getenv("REGRESSION_NATS_URL")),
	}
	if cfg.ReportDir == "" {
		cfg.ReportDir = filepath.Join(".artifacts", "regression", runID)
	}
	for _, raw := range strings.Split(*games, ",") {
		gameType := strings.TrimSpace(raw)
		if gameType == "" {
			continue
		}
		if !slices.Contains(supportedGames, gameType) {
			return config{}, fmt.Errorf("unsupported game %q", gameType)
		}
		if !slices.Contains(cfg.Games, gameType) {
			cfg.Games = append(cfg.Games, gameType)
		}
	}
	if len(cfg.Games) == 0 {
		return config{}, errors.New("at least one game is required")
	}
	for name, value := range map[string]string{
		"REGRESSION_HTTP_BASE":    cfg.HTTPBase,
		"REGRESSION_WS_BASE":      cfg.WSBase,
		"REGRESSION_FRONTEND_URL": cfg.FrontendURL,
		"REGRESSION_ORIGIN":       cfg.Origin,
	} {
		if err := validatePublicURL(name, value); err != nil {
			return config{}, err
		}
	}
	return cfg, nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return strings.TrimRight(value, "/")
	}
	return strings.TrimRight(fallback, "/")
}

func validatePublicURL(name, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s must be an absolute URL", name)
	}
	if parsed.User != nil {
		return fmt.Errorf("%s must not contain credentials", name)
	}
	return nil
}
