package adminanalytics

import (
	"testing"
	"time"
)

func TestSortUsersBeforePagination(t *testing.T) {
	users := []User{
		{ID: 1, GamesPlayed: 8},
		{ID: 2, GamesPlayed: 0},
		{ID: 3, GamesPlayed: 4},
	}
	query := normalizeUserQuery(UserQuery{
		Page: 2, PageSize: 2, SortBy: "games_played", SortOrder: "asc",
	})

	sortUsers(users, query)
	firstPage := paginateUsers(users, 1, query.PageSize)
	secondPage := paginateUsers(users, query.Page, query.PageSize)

	if firstPage[0].ID != 2 || firstPage[1].ID != 3 {
		t.Fatalf("first page IDs = %d, %d", firstPage[0].ID, firstPage[1].ID)
	}
	if len(secondPage) != 1 || secondPage[0].ID != 1 {
		t.Fatalf("second page = %+v", secondPage)
	}
}

func TestSortUsersKeepsMissingValuesLast(t *testing.T) {
	recent := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	older := recent.Add(-time.Hour)
	users := []User{
		{ID: 1},
		{ID: 2, LastActiveAt: &older},
		{ID: 3, LastActiveAt: &recent},
	}

	sortUsers(users, normalizeUserQuery(UserQuery{
		SortBy: "last_active_at", SortOrder: "desc",
	}))

	if users[0].ID != 3 || users[1].ID != 2 || users[2].ID != 1 {
		t.Fatalf("sorted IDs = %d, %d, %d", users[0].ID, users[1].ID, users[2].ID)
	}
}
