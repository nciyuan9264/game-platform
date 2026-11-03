package adminanalytics

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

var (
	ErrInvalidDateRange = errors.New("invalid admin analytics date range")
	ErrUserNotFound     = errors.New("admin analytics user not found")
)

type Config struct {
	GamePostgresDSN string
	AuthPostgresDSN string
	CacheTTL        time.Duration
}

type Service struct {
	gameDB   *sql.DB
	authDB   *sql.DB
	location *time.Location
	cache    *memoryCache
	now      func() time.Time
}

func Open(ctx context.Context, cfg Config) (*Service, error) {
	if strings.TrimSpace(cfg.GamePostgresDSN) == "" {
		return nil, errors.New("game PostgreSQL DSN is required")
	}
	if strings.TrimSpace(cfg.AuthPostgresDSN) == "" {
		return nil, errors.New("auth PostgreSQL DSN is required")
	}
	gameDB, err := openDatabase(ctx, cfg.GamePostgresDSN)
	if err != nil {
		return nil, fmt.Errorf("open game analytics database: %w", err)
	}
	authDB, err := openDatabase(ctx, cfg.AuthPostgresDSN)
	if err != nil {
		_ = gameDB.Close()
		return nil, fmt.Errorf("open auth analytics database: %w", err)
	}
	service, err := New(gameDB, authDB, cfg.CacheTTL)
	if err != nil {
		_ = errors.Join(gameDB.Close(), authDB.Close())
		return nil, err
	}
	return service, nil
}

func New(gameDB, authDB *sql.DB, cacheTTL time.Duration) (*Service, error) {
	if gameDB == nil || authDB == nil {
		return nil, errors.New("game and auth databases are required")
	}
	location, err := time.LoadLocation(Timezone)
	if err != nil {
		return nil, fmt.Errorf("load analytics timezone: %w", err)
	}
	if cacheTTL <= 0 {
		cacheTTL = time.Minute
	}
	return &Service{
		gameDB:   gameDB,
		authDB:   authDB,
		location: location,
		cache:    newMemoryCache(cacheTTL),
		now:      time.Now,
	}, nil
}

func openDatabase(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func (s *Service) Close() error {
	return errors.Join(s.gameDB.Close(), s.authDB.Close())
}

func (s *Service) PingGame(ctx context.Context) error { return s.gameDB.PingContext(ctx) }
func (s *Service) PingAuth(ctx context.Context) error { return s.authDB.PingContext(ctx) }

func (s *Service) reportingWindow() Window {
	window, _ := s.reportingWindowFor(DateRangeQuery{})
	return window
}

func (s *Service) reportingWindowForGames(query GamesQuery) (Window, error) {
	if !query.AllTime {
		return s.reportingWindowFor(query.DateRange)
	}
	generatedAt := s.now()
	localNow := generatedAt.In(s.location)
	end := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, s.location)
	start := time.Date(1970, time.January, 1, 0, 0, 0, 0, s.location)
	return Window{
		Start:       start,
		End:         end,
		StartUTC:    start.UTC(),
		EndUTC:      end.AddDate(0, 0, 1).UTC(),
		Days:        int(end.Sub(start).Hours()/24) + 1,
		GeneratedAt: generatedAt.UTC(),
	}, nil
}

func (s *Service) reportingWindowFor(query DateRangeQuery) (Window, error) {
	generatedAt := s.now()
	localNow := generatedAt.In(s.location)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, s.location)
	startDate := strings.TrimSpace(query.StartDate)
	endDate := strings.TrimSpace(query.EndDate)
	if startDate == "" && endDate == "" {
		endDate = today.Format(time.DateOnly)
		startDate = today.AddDate(0, 0, -(DefaultWindowDays - 1)).Format(time.DateOnly)
	}
	if startDate == "" || endDate == "" {
		return Window{}, fmt.Errorf("%w: start_date and end_date must be provided together", ErrInvalidDateRange)
	}
	start, err := time.ParseInLocation(time.DateOnly, startDate, s.location)
	if err != nil {
		return Window{}, fmt.Errorf("%w: start_date must use YYYY-MM-DD", ErrInvalidDateRange)
	}
	end, err := time.ParseInLocation(time.DateOnly, endDate, s.location)
	if err != nil {
		return Window{}, fmt.Errorf("%w: end_date must use YYYY-MM-DD", ErrInvalidDateRange)
	}
	if start.After(end) {
		return Window{}, fmt.Errorf("%w: start_date must not be after end_date", ErrInvalidDateRange)
	}
	if end.After(today) {
		return Window{}, fmt.Errorf("%w: end_date must not be in the future", ErrInvalidDateRange)
	}
	days := int(end.Sub(start).Hours()/24) + 1
	if days > MaxWindowDays {
		return Window{}, fmt.Errorf("%w: date range must not exceed %d days", ErrInvalidDateRange, MaxWindowDays)
	}
	return Window{
		Start:       start,
		End:         end,
		StartUTC:    start.UTC(),
		EndUTC:      end.AddDate(0, 0, 1).UTC(),
		Days:        days,
		GeneratedAt: generatedAt.UTC(),
	}, nil
}

func normalizeUserQuery(input UserQuery) UserQuery {
	if input.Page < 1 {
		input.Page = 1
	}
	if input.PageSize < 1 {
		input.PageSize = 20
	}
	if input.PageSize > 100 {
		input.PageSize = 100
	}
	input.Query = strings.TrimSpace(input.Query)
	switch strings.ToLower(strings.TrimSpace(input.SortBy)) {
	case "user", "created_at", "last_active_at", "active_days", "games_played", "player_duration", "favorite_game":
		input.SortBy = strings.ToLower(strings.TrimSpace(input.SortBy))
	default:
		input.SortBy = "created_at"
	}
	if strings.EqualFold(strings.TrimSpace(input.SortOrder), "asc") {
		input.SortOrder = "asc"
	} else {
		input.SortOrder = "desc"
	}
	return input
}

func percentage(numerator, denominator int64) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

type cacheEntry struct {
	expiresAt time.Time
	value     any
}

type memoryCache struct {
	mu      sync.RWMutex
	ttl     time.Duration
	entries map[string]cacheEntry
}

func newMemoryCache(ttl time.Duration) *memoryCache {
	return &memoryCache{ttl: ttl, entries: make(map[string]cacheEntry)}
}

func cachedValue[T any](ctx context.Context, service *Service, key string, load func(context.Context) (T, error)) (T, error) {
	now := service.now()
	service.cache.mu.RLock()
	entry, ok := service.cache.entries[key]
	service.cache.mu.RUnlock()
	if ok && now.Before(entry.expiresAt) {
		if value, valid := entry.value.(T); valid {
			return value, nil
		}
	}
	value, err := load(ctx)
	if err != nil {
		var zero T
		return zero, err
	}
	service.cache.mu.Lock()
	service.cache.entries[key] = cacheEntry{expiresAt: now.Add(service.cache.ttl), value: value}
	service.cache.mu.Unlock()
	return value, nil
}
