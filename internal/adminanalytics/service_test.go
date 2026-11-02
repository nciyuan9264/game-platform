package adminanalytics

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReportingWindowUsesBeijingCalendarDays(t *testing.T) {
	location, err := time.LoadLocation(Timezone)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{
		location: location,
		now: func() time.Time {
			return time.Date(2026, 11, 1, 16, 30, 0, 0, time.UTC)
		},
	}
	window := service.reportingWindow()
	if window.StartDate() != "2026-10-04" || window.EndDate() != "2026-11-02" {
		t.Fatalf("window = %s..%s", window.StartDate(), window.EndDate())
	}
	if got := window.StartUTC.Format(time.RFC3339); got != "2026-10-03T16:00:00Z" {
		t.Fatalf("start UTC = %s", got)
	}
	if got := window.EndUTC.Format(time.RFC3339); got != "2026-11-02T16:00:00Z" {
		t.Fatalf("end UTC = %s", got)
	}
}

func TestReportingWindowAcceptsCustomDateRange(t *testing.T) {
	location, err := time.LoadLocation(Timezone)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{
		location: location,
		now: func() time.Time {
			return time.Date(2026, 11, 2, 3, 0, 0, 0, time.UTC)
		},
	}
	window, err := service.reportingWindowFor(DateRangeQuery{
		StartDate: "2026-10-27",
		EndDate:   "2026-11-02",
	})
	if err != nil {
		t.Fatal(err)
	}
	if window.Days != 7 || window.StartDate() != "2026-10-27" || window.EndDate() != "2026-11-02" {
		t.Fatalf("window = %+v", window)
	}
	if got := window.StartUTC.Format(time.RFC3339); got != "2026-10-26T16:00:00Z" {
		t.Fatalf("start UTC = %s", got)
	}
	if got := window.EndUTC.Format(time.RFC3339); got != "2026-11-02T16:00:00Z" {
		t.Fatalf("end UTC = %s", got)
	}
}

func TestReportingWindowRejectsInvalidRanges(t *testing.T) {
	location, err := time.LoadLocation(Timezone)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{
		location: location,
		now: func() time.Time {
			return time.Date(2026, 11, 2, 3, 0, 0, 0, time.UTC)
		},
	}
	tests := []DateRangeQuery{
		{StartDate: "2026-11-01"},
		{StartDate: "2026/11/01", EndDate: "2026-11-02"},
		{StartDate: "2026-11-02", EndDate: "2026-11-01"},
		{StartDate: "2026-11-01", EndDate: "2026-11-03"},
		{StartDate: "2025-11-01", EndDate: "2026-11-02"},
	}
	for _, input := range tests {
		if _, err := service.reportingWindowFor(input); !errors.Is(err, ErrInvalidDateRange) {
			t.Fatalf("reportingWindowFor(%+v) error = %v", input, err)
		}
	}
}

func TestNormalizeUserQuery(t *testing.T) {
	got := normalizeUserQuery(UserQuery{Page: -1, PageSize: 500, Query: "  player  "})
	if got.Page != 1 || got.PageSize != 100 || got.Query != "player" {
		t.Fatalf("normalized query = %+v", got)
	}
	defaults := normalizeUserQuery(UserQuery{})
	if defaults.Page != 1 || defaults.PageSize != 20 {
		t.Fatalf("default query = %+v", defaults)
	}
}

func TestCachedValueExpires(t *testing.T) {
	now := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	service := &Service{
		cache: newMemoryCache(time.Minute),
		now:   func() time.Time { return now },
	}
	loads := 0
	load := func(context.Context) (int, error) {
		loads++
		return loads, nil
	}
	first, err := cachedValue(context.Background(), service, "value", load)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cachedValue(context.Background(), service, "value", load)
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 1 || loads != 1 {
		t.Fatalf("cached values = %d, %d; loads = %d", first, second, loads)
	}
	now = now.Add(time.Minute)
	third, err := cachedValue(context.Background(), service, "value", load)
	if err != nil {
		t.Fatal(err)
	}
	if third != 2 || loads != 2 {
		t.Fatalf("expired value = %d; loads = %d", third, loads)
	}
}

func TestPercentageHandlesEmptyDenominator(t *testing.T) {
	if got := percentage(3, 0); got != 0 {
		t.Fatalf("percentage with empty denominator = %v", got)
	}
	if got := percentage(1, 4); got != 0.25 {
		t.Fatalf("percentage = %v", got)
	}
}
