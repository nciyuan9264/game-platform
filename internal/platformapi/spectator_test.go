package platformapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nciyuan9264/game-platform-server/internal/authapi"
	"github.com/nciyuan9264/game-platform-server/internal/contracts"
	"github.com/nciyuan9264/game-platform-server/internal/store"
)

type fakeSpectatorStore struct {
	createErr error
	created   contracts.SpectatorSession
	session   contracts.SpectatorSession
	room      contracts.RoomRecord
	match     contracts.MatchRecord
	snapshot  contracts.SpectatorSnapshotRecord
	next      time.Time
}

func (s *fakeSpectatorStore) CreateSpectatorSession(_ context.Context, session contracts.SpectatorSession) error {
	s.created = session
	return s.createErr
}

func (s *fakeSpectatorStore) GetSpectatorSession(context.Context, string, string) (contracts.SpectatorSession, error) {
	if s.session.SessionID == "" {
		return contracts.SpectatorSession{}, sql.ErrNoRows
	}
	return s.session, nil
}

func (s *fakeSpectatorStore) GetRoom(context.Context, string) (contracts.RoomRecord, error) {
	if s.room.RoomID == "" {
		return contracts.RoomRecord{}, sql.ErrNoRows
	}
	return s.room, nil
}

func (s *fakeSpectatorStore) GetMatch(context.Context, string) (contracts.MatchRecord, error) {
	if s.match.MatchID == "" {
		return contracts.MatchRecord{}, sql.ErrNoRows
	}
	return s.match, nil
}

func (s *fakeSpectatorStore) LatestAvailableSpectatorSnapshot(context.Context, string) (contracts.SpectatorSnapshotRecord, error) {
	if s.snapshot.RoomID == "" {
		return contracts.SpectatorSnapshotRecord{}, sql.ErrNoRows
	}
	return s.snapshot, nil
}

func (s *fakeSpectatorStore) NextSpectatorSnapshotAvailability(context.Context, string) (time.Time, error) {
	if s.next.IsZero() {
		return time.Time{}, sql.ErrNoRows
	}
	return s.next, nil
}

func newSpectatorTestServer(st spectatorStore) *Server {
	auth := roleAuthClient{
		valid:    true,
		identity: contracts.UserIdentity{UserID: "9", Roles: []string{"player"}},
	}
	server := New(nil, auth, nil, authapi.Config{})
	server.spectators = st
	return server
}

func TestCreateSpectatorSession(t *testing.T) {
	st := &fakeSpectatorStore{room: contracts.RoomRecord{RoomID: "room-1", CurrentMatchID: "room-1-r1"}}
	server := newSpectatorTestServer(st)
	req := httptest.NewRequest(http.MethodPost, "/platform/spectator/rooms/room-1/sessions", nil)
	req.Header.Set("Authorization", "Bearer token")
	res := httptest.NewRecorder()
	server.Routes().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
	if st.created.RoomID != "room-1" || st.created.UserID != "9" || st.created.SessionID == "" {
		t.Fatalf("created session = %+v", st.created)
	}
	if st.created.ExpiresAt.Sub(st.created.CreatedAt) != spectatorSessionTTL {
		t.Fatalf("session ttl = %s", st.created.ExpiresAt.Sub(st.created.CreatedAt))
	}
}

func TestCreateSpectatorSessionRejectsParticipant(t *testing.T) {
	server := newSpectatorTestServer(&fakeSpectatorStore{
		createErr: store.ErrSpectatorParticipant,
		room:      contracts.RoomRecord{RoomID: "room-1", CurrentMatchID: "room-1-r1"},
	})
	req := httptest.NewRequest(http.MethodPost, "/platform/spectator/rooms/room-1/sessions", nil)
	req.Header.Set("Authorization", "Bearer token")
	res := httptest.NewRecorder()
	server.Routes().ServeHTTP(res, req)
	if res.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
	}
}

func TestSpectatorFrameOnlyReturnsNewVisibleSnapshot(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeSpectatorStore{
		session: contracts.SpectatorSession{SessionID: "session-1", RoomID: "room-1", MatchID: "room-1-r1", UserID: "9"},
		room: contracts.RoomRecord{
			RoomID:         "room-1",
			GameType:       contracts.GameAcquire,
			Status:         contracts.RoomStatusPlaying,
			LastAppliedSeq: 12,
		},
		match: contracts.MatchRecord{MatchID: "room-1-r1", LastAppliedSeq: 12},
		snapshot: contracts.SpectatorSnapshotRecord{
			RoomID:      "room-1",
			MatchID:     "room-1-r1",
			GameType:    contracts.GameAcquire,
			Seq:         7,
			Payload:     map[string]any{"seq": float64(7)},
			CapturedAt:  now.Add(-contracts.SpectatorDelay),
			AvailableAt: now,
		},
	}
	server := newSpectatorTestServer(st)

	for _, test := range []struct {
		name         string
		afterSeq     string
		wantSnapshot bool
	}{
		{name: "new snapshot", afterSeq: "6", wantSnapshot: true},
		{name: "already seen", afterSeq: "7"},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/platform/spectator/sessions/session-1?afterSeq="+test.afterSeq, nil)
			req.Header.Set("Authorization", "Bearer token")
			res := httptest.NewRecorder()
			server.Routes().ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s", res.Code, res.Body.String())
			}
			var body struct {
				Data struct {
					VisibleSeq int64           `json:"visibleSeq"`
					LiveSeq    int64           `json:"liveSeq"`
					Snapshot   json.RawMessage `json:"snapshot"`
				} `json:"data"`
			}
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			hasSnapshot := string(body.Data.Snapshot) != "null"
			if body.Data.VisibleSeq != 7 || body.Data.LiveSeq != 12 || hasSnapshot != test.wantSnapshot {
				t.Fatalf("response = %+v snapshot = %s", body.Data, body.Data.Snapshot)
			}
		})
	}
}
