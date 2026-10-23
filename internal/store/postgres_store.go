package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/nciyuan9264/game-platform/internal/contracts"
)

type PostgresStore struct {
	db *sql.DB
}

var (
	ErrStaleRoomWrite        = errors.New("stale room write rejected")
	ErrStaleOwnerWrite       = errors.New("stale owner write rejected")
	ErrRoomQuotaExceeded     = errors.New("active room quota exceeded")
	ErrIdempotencyConflict   = errors.New("idempotency key was reused with a different request")
	ErrCommandIntentConflict = errors.New("command id was reused with a different intent")
	ErrMatchActive           = errors.New("room already has a non-terminal match")
	ErrRoomNotOpen           = errors.New("room is not open")
	ErrSpectatorUnavailable  = errors.New("room is not available for spectating")
	ErrSpectatorParticipant  = errors.New("room participants must use the live game connection")
)

func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	db, err := sql.Open("postgres", withBinaryParameters(dsn))
	if err != nil {
		return nil, err
	}
	// Four long-running services share the same managed PostgreSQL instance.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)

	store := &PostgresStore{db: db}
	if err := store.waitReady(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func withBinaryParameters(dsn string) string {
	if strings.Contains(strings.ToLower(dsn), "binary_parameters=") {
		return dsn
	}
	parsed, err := url.Parse(dsn)
	if err == nil && parsed.Scheme != "" {
		query := parsed.Query()
		query.Set("binary_parameters", "yes")
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return strings.TrimSpace(dsn) + " binary_parameters=yes"
}

func (s *PostgresStore) Close() error {
	return s.db.Close()
}

func (s *PostgresStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *PostgresStore) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := s.db.PingContext(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return lastErr
}

func (s *PostgresStore) Migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(9102609264)`); err != nil {
		return err
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS rooms (
			room_id TEXT PRIMARY KEY,
			game_type TEXT NOT NULL,
			owner_user_id TEXT NOT NULL,
			owner_player_id TEXT NOT NULL,
			owner_node TEXT NOT NULL,
			epoch BIGINT NOT NULL,
			status TEXT NOT NULL,
			max_players INTEGER NOT NULL,
			player_count INTEGER NOT NULL,
				progress_current INTEGER NOT NULL DEFAULT 0,
				progress_total INTEGER NOT NULL DEFAULT 0,
			last_applied_seq BIGINT NOT NULL DEFAULT 0,
			engine_version TEXT NOT NULL,
			current_match_id TEXT NOT NULL DEFAULT '',
			next_round_no INTEGER NOT NULL DEFAULT 1,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL,
			lease_expires_at TIMESTAMPTZ
		)`,
		`ALTER TABLE rooms ADD COLUMN IF NOT EXISTS progress_current INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE rooms ADD COLUMN IF NOT EXISTS progress_total INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE rooms ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'public'`,
		`ALTER TABLE rooms ADD COLUMN IF NOT EXISTS last_applied_seq BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE rooms ADD COLUMN IF NOT EXISTS current_match_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE rooms ADD COLUMN IF NOT EXISTS next_round_no INTEGER NOT NULL DEFAULT 1`,
		`UPDATE rooms SET status = CASE
			WHEN status IN ('ended', 'interrupted', 'closed') THEN 'closed'
			WHEN status = 'creating' THEN 'creating'
			WHEN status = 'closing' THEN 'closing'
			ELSE 'open'
		END`,
		`CREATE INDEX IF NOT EXISTS idx_rooms_game_type ON rooms (game_type)`,
		`CREATE INDEX IF NOT EXISTS idx_rooms_active_visibility ON rooms (game_type, visibility, created_at DESC) WHERE status <> 'closed'`,
		`CREATE INDEX IF NOT EXISTS idx_rooms_owner_node ON rooms (owner_node)`,
		`CREATE INDEX IF NOT EXISTS idx_rooms_lease_expires_at ON rooms (lease_expires_at)`,
		`CREATE TABLE IF NOT EXISTS room_owner_generations (
			room_id TEXT PRIMARY KEY REFERENCES rooms(room_id) ON DELETE CASCADE,
			epoch BIGINT NOT NULL,
			owner_node TEXT NOT NULL,
			state TEXT NOT NULL CHECK (state IN ('recovering', 'ready')),
			recovery_deadline TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		)`,
		`INSERT INTO room_owner_generations (
			room_id, epoch, owner_node, state, recovery_deadline, updated_at
		)
		SELECT room_id, epoch, owner_node, 'ready',
			COALESCE(lease_expires_at, clock_timestamp()), updated_at
		FROM rooms
		ON CONFLICT (room_id) DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS matches (
			match_id TEXT PRIMARY KEY,
			runtime_room_id TEXT NOT NULL,
			game_type TEXT NOT NULL,
			status TEXT NOT NULL,
			owner_user_id TEXT NOT NULL,
			owner_player_id TEXT NOT NULL,
			runtime_owner_node TEXT NOT NULL,
			epoch BIGINT NOT NULL,
			max_players INTEGER NOT NULL,
			player_count INTEGER NOT NULL,
			engine_version TEXT NOT NULL,
			round_no INTEGER NOT NULL DEFAULT 1,
			last_applied_seq BIGINT NOT NULL DEFAULT 0,
			random_seed TEXT NOT NULL DEFAULT '0',
			started_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL,
			ended_at TIMESTAMPTZ,
			duration_seconds BIGINT NOT NULL DEFAULT 0,
			winner_user_id TEXT NOT NULL DEFAULT '',
			winner_player_id TEXT NOT NULL DEFAULT '',
			end_reason TEXT NOT NULL DEFAULT ''
		)`,
		`ALTER TABLE matches DROP CONSTRAINT IF EXISTS matches_runtime_room_id_key`,
		`ALTER TABLE matches ADD COLUMN IF NOT EXISTS round_no INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE matches ADD COLUMN IF NOT EXISTS last_applied_seq BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE matches ADD COLUMN IF NOT EXISTS random_seed TEXT NOT NULL DEFAULT '0'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_matches_room_round ON matches (runtime_room_id, round_no)`,
		`CREATE INDEX IF NOT EXISTS idx_matches_game_type_started ON matches (game_type, started_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_matches_status ON matches (status)`,
		`CREATE INDEX IF NOT EXISTS idx_matches_owner_user_id ON matches (owner_user_id)`,
		`CREATE TABLE IF NOT EXISTS match_players (
			match_id TEXT NOT NULL REFERENCES matches(match_id) ON DELETE CASCADE,
			user_id TEXT NOT NULL,
			player_id TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			avatar TEXT NOT NULL DEFAULT '',
			gateway_node TEXT NOT NULL DEFAULT '',
			online BOOLEAN NOT NULL DEFAULT false,
				is_ai BOOLEAN NOT NULL DEFAULT false,
			joined_at TIMESTAMPTZ,
			last_seen TIMESTAMPTZ,
			PRIMARY KEY (match_id, player_id)
		)`,
		`ALTER TABLE match_players ADD COLUMN IF NOT EXISTS is_ai BOOLEAN NOT NULL DEFAULT false`,
		`ALTER TABLE match_players ADD COLUMN IF NOT EXISTS seat_index INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE match_players ADD COLUMN IF NOT EXISTS ready BOOLEAN NOT NULL DEFAULT false`,
		`CREATE INDEX IF NOT EXISTS idx_match_players_user_id ON match_players (user_id)`,
		`CREATE TABLE IF NOT EXISTS games (
			id BIGSERIAL PRIMARY KEY,
			room_id TEXT NOT NULL,
			game_type TEXT NOT NULL,
			started_at TIMESTAMPTZ NOT NULL,
			ended_at TIMESTAMPTZ,
			duration_seconds INTEGER NOT NULL DEFAULT 0,
			winner_user_id BIGINT,
			winner_player_id TEXT NOT NULL DEFAULT '',
			end_reason TEXT NOT NULL DEFAULT '',
			max_players INTEGER NOT NULL,
			initial_state JSONB,
			final_result JSONB,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		)`,
		`UPDATE games SET winner_player_id = '' WHERE winner_player_id IS NULL`,
		`ALTER TABLE games ALTER COLUMN winner_player_id SET DEFAULT ''`,
		`ALTER TABLE games ALTER COLUMN winner_player_id SET NOT NULL`,
		`UPDATE games SET end_reason = '' WHERE end_reason IS NULL`,
		`ALTER TABLE games ALTER COLUMN end_reason SET DEFAULT ''`,
		`ALTER TABLE games ALTER COLUMN end_reason SET NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_games_type_started ON games (game_type, started_at DESC)`,
		`ALTER TABLE games ADD COLUMN IF NOT EXISTS legacy_source TEXT`,
		`ALTER TABLE games ADD COLUMN IF NOT EXISTS legacy_game_id BIGINT`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_games_legacy_identity ON games (legacy_source, legacy_game_id) WHERE legacy_source IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_games_room_open ON games (room_id, game_type, ended_at)`,
		`CREATE TABLE IF NOT EXISTS game_players (
			id BIGSERIAL PRIMARY KEY,
			game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
			user_id BIGINT,
			player_id TEXT NOT NULL,
			seat_index INTEGER NOT NULL,
			is_ai BOOLEAN NOT NULL,
			final_score INTEGER,
			final_money INTEGER,
			final_stocks INTEGER NOT NULL DEFAULT 0,
			final_rank INTEGER,
			is_winner BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMPTZ NOT NULL
		)`,
		`UPDATE game_players
		SET user_id = CASE
			WHEN player_id ~ '^[0-9]+$' THEN player_id::BIGINT
			ELSE substring(player_id FROM '([0-9]+)$')::BIGINT
		END
		WHERE user_id IS NULL
			AND NOT is_ai
			AND (player_id ~ '^[0-9]+$' OR player_id ~ '-[0-9]+$')`,
		`UPDATE games AS game
		SET winner_user_id = player.user_id
		FROM game_players AS player
		WHERE game.winner_user_id IS NULL
			AND player.game_id = game.id
			AND player.is_winner
			AND player.user_id IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_game_players_game ON game_players (game_id)`,
		`CREATE INDEX IF NOT EXISTS idx_game_players_user ON game_players (user_id, game_id)`,
		`CREATE TABLE IF NOT EXISTS game_events (
			id BIGSERIAL PRIMARY KEY,
			game_id BIGINT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
			seq INTEGER NOT NULL,
			occurred_at TIMESTAMPTZ NOT NULL,
			player_id TEXT NOT NULL,
			cmd_type TEXT NOT NULL,
			payload JSONB NOT NULL,
			state_snapshot JSONB,
			state_blob BYTEA,
			UNIQUE (game_id, seq)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_game_events_game ON game_events (game_id, seq)`,
		`CREATE TABLE IF NOT EXISTS game_operation_events (
			event_id TEXT PRIMARY KEY,
			room_id TEXT NOT NULL,
			match_id TEXT NOT NULL DEFAULT '',
			game_type TEXT NOT NULL,
			seq BIGINT NOT NULL,
			command_id TEXT NOT NULL,
			player_id TEXT NOT NULL DEFAULT '',
			visibility TEXT NOT NULL,
			event_type TEXT NOT NULL,
			payload JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL
		)`,
		`ALTER TABLE game_operation_events ADD COLUMN IF NOT EXISTS match_id TEXT NOT NULL DEFAULT ''`,
		`UPDATE game_operation_events SET match_id = room_id WHERE match_id = ''`,
		`CREATE INDEX IF NOT EXISTS idx_game_operation_events_room_seq ON game_operation_events (room_id, seq, event_id)`,
		`INSERT INTO matches (
			match_id, runtime_room_id, game_type, status, owner_user_id, owner_player_id,
			runtime_owner_node, epoch, max_players, player_count, engine_version,
			started_at, updated_at, ended_at, duration_seconds, end_reason
		)
		SELECT
				room_id, room_id, game_type,
				CASE
					WHEN status = 'closed' THEN 'ended'
					WHEN last_applied_seq > 0 THEN 'running'
					ELSE 'preparing'
				END,
				owner_user_id, owner_player_id,
			owner_node, epoch, max_players, player_count, engine_version,
			created_at, updated_at,
				CASE WHEN status = 'closed' THEN updated_at ELSE NULL END,
			GREATEST(0, FLOOR(EXTRACT(EPOCH FROM (updated_at - created_at)))::BIGINT),
				CASE WHEN status = 'closed' THEN 'legacy_room_closed' ELSE '' END
			FROM rooms r
			WHERE NOT EXISTS (
				SELECT 1 FROM matches existing
				WHERE existing.runtime_room_id = r.room_id
			)
		ON CONFLICT (match_id) DO NOTHING`,
		`UPDATE matches SET status = CASE
			WHEN status IN ('ended', 'aborted') THEN status
			WHEN status IN ('interrupted', 'closed') THEN 'aborted'
			WHEN status IN ('playing', 'running') THEN 'running'
			ELSE 'preparing'
		END`,
		`UPDATE rooms r
		SET current_match_id = r.room_id,
			next_round_no = GREATEST(r.next_round_no, 2)
		WHERE r.status = 'open'
			AND r.current_match_id = ''
			AND EXISTS (SELECT 1 FROM matches m WHERE m.match_id = r.room_id)`,
		`CREATE TABLE IF NOT EXISTS room_snapshots (
			room_id TEXT NOT NULL,
			match_id TEXT NOT NULL,
			seq BIGINT NOT NULL,
			epoch BIGINT NOT NULL,
			engine_version TEXT NOT NULL,
			state BYTEA NOT NULL,
			created_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (match_id, seq)
		)`,
		`ALTER TABLE room_snapshots ADD COLUMN IF NOT EXISTS match_id TEXT`,
		`UPDATE room_snapshots SET match_id = room_id WHERE match_id IS NULL OR match_id = ''`,
		`ALTER TABLE room_snapshots ALTER COLUMN match_id SET NOT NULL`,
		`ALTER TABLE room_snapshots DROP CONSTRAINT IF EXISTS room_snapshots_pkey`,
		`ALTER TABLE room_snapshots ADD PRIMARY KEY (match_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_room_snapshots_latest ON room_snapshots (room_id, seq DESC)`,
		`CREATE TABLE IF NOT EXISTS spectator_snapshots (
			room_id TEXT NOT NULL,
			match_id TEXT NOT NULL,
			game_type TEXT NOT NULL,
			seq BIGINT NOT NULL,
			epoch BIGINT NOT NULL,
			owner_node TEXT NOT NULL,
			payload JSONB NOT NULL,
			captured_at TIMESTAMPTZ NOT NULL,
			available_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (match_id, seq)
		)`,
		`ALTER TABLE spectator_snapshots ADD COLUMN IF NOT EXISTS match_id TEXT`,
		`ALTER TABLE spectator_snapshots ADD COLUMN IF NOT EXISTS epoch BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE spectator_snapshots ADD COLUMN IF NOT EXISTS owner_node TEXT NOT NULL DEFAULT ''`,
		`UPDATE spectator_snapshots SET match_id = room_id WHERE match_id IS NULL OR match_id = ''`,
		`ALTER TABLE spectator_snapshots ALTER COLUMN match_id SET NOT NULL`,
		`ALTER TABLE spectator_snapshots DROP CONSTRAINT IF EXISTS spectator_snapshots_pkey`,
		`ALTER TABLE spectator_snapshots ADD PRIMARY KEY (match_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_spectator_snapshots_available ON spectator_snapshots (room_id, available_at DESC, seq DESC)`,
		`CREATE TABLE IF NOT EXISTS spectator_sessions (
			session_id TEXT PRIMARY KEY,
			room_id TEXT NOT NULL,
			match_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL,
			expires_at TIMESTAMPTZ NOT NULL
		)`,
		`ALTER TABLE spectator_sessions ADD COLUMN IF NOT EXISTS match_id TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_spectator_sessions_room_user ON spectator_sessions (room_id, user_id, expires_at DESC)`,
		`CREATE TABLE IF NOT EXISTS outbound_messages (
			message_id TEXT PRIMARY KEY,
			room_id TEXT NOT NULL,
			match_id TEXT NOT NULL,
			seq BIGINT NOT NULL,
			epoch BIGINT NOT NULL,
			owner_node TEXT NOT NULL,
			target_user_id TEXT NOT NULL,
			target_player_id TEXT NOT NULL,
			target_session_id TEXT NOT NULL,
			target_gateway_node TEXT NOT NULL,
			trace_id TEXT NOT NULL,
			reliable BOOLEAN NOT NULL,
			close_after BOOLEAN NOT NULL DEFAULT false,
			payload JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL,
			delivered_at TIMESTAMPTZ,
			ack_attempts INTEGER NOT NULL DEFAULT 0
		)`,
		`ALTER TABLE outbound_messages ADD COLUMN IF NOT EXISTS match_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE outbound_messages ADD COLUMN IF NOT EXISTS epoch BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE outbound_messages ADD COLUMN IF NOT EXISTS owner_node TEXT NOT NULL DEFAULT ''`,
		`UPDATE outbound_messages SET match_id = room_id WHERE match_id = ''`,
		`ALTER TABLE outbound_messages ADD COLUMN IF NOT EXISTS close_after BOOLEAN NOT NULL DEFAULT false`,
		`CREATE INDEX IF NOT EXISTS idx_outbound_backfill_player ON outbound_messages (room_id, target_player_id, seq)`,
		`CREATE INDEX IF NOT EXISTS idx_outbound_backfill_session ON outbound_messages (room_id, target_session_id, seq)`,
		`CREATE TABLE IF NOT EXISTS room_command_acks (
			room_id TEXT NOT NULL,
			match_id TEXT NOT NULL,
			command_id TEXT NOT NULL,
			epoch BIGINT NOT NULL,
			owner_node TEXT NOT NULL,
			intent_hash TEXT NOT NULL,
			accepted BOOLEAN NOT NULL,
			seq BIGINT NOT NULL,
			error_code TEXT NOT NULL,
			message TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (match_id, command_id)
		)`,
		`ALTER TABLE room_command_acks ADD COLUMN IF NOT EXISTS match_id TEXT`,
		`ALTER TABLE room_command_acks ADD COLUMN IF NOT EXISTS epoch BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE room_command_acks ADD COLUMN IF NOT EXISTS owner_node TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE room_command_acks ADD COLUMN IF NOT EXISTS intent_hash TEXT NOT NULL DEFAULT ''`,
		`UPDATE room_command_acks SET match_id = room_id WHERE match_id IS NULL OR match_id = ''`,
		`ALTER TABLE room_command_acks ALTER COLUMN match_id SET NOT NULL`,
		`ALTER TABLE room_command_acks DROP CONSTRAINT IF EXISTS room_command_acks_pkey`,
		`ALTER TABLE room_command_acks ADD PRIMARY KEY (match_id, command_id)`,
		`CREATE TABLE IF NOT EXISTS match_start_requests (
			room_id TEXT NOT NULL,
			request_id TEXT NOT NULL,
			match_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (room_id, request_id)
		)`,
		`CREATE TABLE IF NOT EXISTS room_create_requests (
			owner_user_id TEXT NOT NULL,
			request_id TEXT NOT NULL,
			request_hash TEXT NOT NULL,
			room_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (owner_user_id, request_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_room_create_requests_room_id ON room_create_requests (room_id)`,
		`CREATE TABLE IF NOT EXISTS runtime_events (
			id BIGSERIAL PRIMARY KEY,
			room_id TEXT NOT NULL,
			match_id TEXT NOT NULL DEFAULT '',
			trace_id TEXT NOT NULL,
			node_id TEXT NOT NULL,
			component TEXT NOT NULL,
			event TEXT NOT NULL,
			message TEXT NOT NULL,
			payload JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL
		)`,
		`ALTER TABLE runtime_events ADD COLUMN IF NOT EXISTS match_id TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_runtime_events_room_id ON runtime_events (room_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_runtime_events_trace_id ON runtime_events (trace_id, id)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) SetRoom(ctx context.Context, r contracts.RoomRecord) error {
	visibility, err := normalizeRoomVisibility(r.Visibility)
	if err != nil {
		return err
	}
	r.Visibility = visibility
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Existing visibility is updated only through the owner-fenced projection.
	result, err := tx.ExecContext(ctx, `
		INSERT INTO rooms (
			room_id, game_type, visibility, owner_user_id, owner_player_id, owner_node,
				epoch, status, max_players, player_count, progress_current, progress_total, last_applied_seq, engine_version,
			current_match_id, next_round_no, created_at, updated_at, lease_expires_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		ON CONFLICT (room_id) DO UPDATE SET
			game_type = EXCLUDED.game_type,
			owner_user_id = EXCLUDED.owner_user_id,
			owner_player_id = EXCLUDED.owner_player_id,
			owner_node = EXCLUDED.owner_node,
			epoch = EXCLUDED.epoch,
			status = EXCLUDED.status,
			max_players = EXCLUDED.max_players,
			player_count = EXCLUDED.player_count,
				progress_current = EXCLUDED.progress_current,
				progress_total = EXCLUDED.progress_total,
			last_applied_seq = CASE
				WHEN rooms.current_match_id <> EXCLUDED.current_match_id THEN EXCLUDED.last_applied_seq
				ELSE GREATEST(rooms.last_applied_seq, EXCLUDED.last_applied_seq)
			END,
			engine_version = EXCLUDED.engine_version,
			current_match_id = EXCLUDED.current_match_id,
			next_round_no = GREATEST(rooms.next_round_no, EXCLUDED.next_round_no),
			updated_at = EXCLUDED.updated_at,
			lease_expires_at = EXCLUDED.lease_expires_at
		WHERE rooms.epoch <= EXCLUDED.epoch
			AND (
				rooms.status <> 'closed'
				OR EXCLUDED.status = 'closed'
			)`,
		r.RoomID, r.GameType, r.Visibility, r.OwnerUserID, r.OwnerPlayerID, r.OwnerNode,
		r.Epoch, r.Status, r.MaxPlayers, r.PlayerCount, r.ProgressCurrent, r.ProgressTotal, r.LastAppliedSeq, r.EngineVersion,
		r.CurrentMatchID, r.NextRoundNo, r.CreatedAt, r.UpdatedAt, nullableTime(r.LeaseExpiresAt),
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrStaleRoomWrite
	}
	if r.OwnerNode != "" {
		deadline := r.LeaseExpiresAt
		if deadline.IsZero() {
			deadline = now
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE room_owner_generations
			SET owner_node = $3, recovery_deadline = $4, updated_at = $5
			WHERE room_id = $1
			  AND epoch = $2
			  AND state = 'recovering'
			  AND owner_node IN ('', $3)`,
			r.RoomID, r.Epoch, r.OwnerNode, deadline, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) ReserveRoomCreation(ctx context.Context, rec contracts.RoomRecord, requestID, requestHash string, maxOwnedRooms int) (contracts.RoomRecord, bool, error) {
	visibility, err := normalizeRoomVisibility(rec.Visibility)
	if err != nil {
		return contracts.RoomRecord{}, false, err
	}
	rec.Visibility = visibility
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.RoomRecord{}, false, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, rec.OwnerUserID); err != nil {
		return contracts.RoomRecord{}, false, err
	}

	var existingRoomID, existingHash string
	err = tx.QueryRowContext(ctx, `
		SELECT room_id, request_hash
		FROM room_create_requests
		WHERE owner_user_id = $1 AND request_id = $2`,
		rec.OwnerUserID, requestID).Scan(&existingRoomID, &existingHash)
	if err == nil {
		if existingHash != requestHash {
			return contracts.RoomRecord{}, false, ErrIdempotencyConflict
		}
		row := tx.QueryRowContext(ctx, `
			SELECT room_id, game_type, visibility, owner_user_id, owner_player_id, owner_node,
				epoch, status, max_players, player_count, progress_current, progress_total, last_applied_seq, engine_version,
				current_match_id, next_round_no, created_at, updated_at, lease_expires_at
			FROM rooms
			WHERE room_id = $1`, existingRoomID)
		existing, err := scanRoom(row)
		return existing, true, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return contracts.RoomRecord{}, false, err
	}

	var activeRooms int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM rooms
		WHERE owner_user_id = $1
			AND status <> 'closed'`, rec.OwnerUserID).Scan(&activeRooms); err != nil {
		return contracts.RoomRecord{}, false, err
	}
	if maxOwnedRooms > 0 && activeRooms >= maxOwnedRooms {
		return contracts.RoomRecord{}, false, ErrRoomQuotaExceeded
	}

	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO rooms (
			room_id, game_type, visibility, owner_user_id, owner_player_id, owner_node,
			epoch, status, max_players, player_count, progress_current, progress_total,
				last_applied_seq, engine_version, current_match_id, next_round_no,
				created_at, updated_at, lease_expires_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`,
		rec.RoomID, rec.GameType, rec.Visibility, rec.OwnerUserID, rec.OwnerPlayerID, rec.OwnerNode,
		rec.Epoch, rec.Status, rec.MaxPlayers, rec.PlayerCount, rec.ProgressCurrent, rec.ProgressTotal,
		rec.LastAppliedSeq, rec.EngineVersion, rec.CurrentMatchID, rec.NextRoundNo,
		rec.CreatedAt, rec.UpdatedAt, nullableTime(rec.LeaseExpiresAt)); err != nil {
		return contracts.RoomRecord{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO room_owner_generations (
			room_id, epoch, owner_node, state, recovery_deadline, updated_at
		) VALUES ($1,$2,'','recovering',$3,$3)`,
		rec.RoomID, rec.Epoch, now); err != nil {
		return contracts.RoomRecord{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO room_create_requests (
			owner_user_id, request_id, request_hash, room_id, created_at
		) VALUES ($1,$2,$3,$4,$5)`,
		rec.OwnerUserID, requestID, requestHash, rec.RoomID, now); err != nil {
		return contracts.RoomRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return contracts.RoomRecord{}, false, err
	}
	return rec, false, nil
}

func (s *PostgresStore) DeleteRoomCreation(ctx context.Context, roomID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	statements := []string{
		`DELETE FROM room_create_requests WHERE room_id = $1`,
		`DELETE FROM match_start_requests WHERE room_id = $1`,
		`DELETE FROM game_operation_events WHERE room_id = $1`,
		`DELETE FROM spectator_sessions WHERE room_id = $1`,
		`DELETE FROM spectator_snapshots WHERE room_id = $1`,
		`DELETE FROM room_snapshots WHERE room_id = $1`,
		`DELETE FROM outbound_messages WHERE room_id = $1`,
		`DELETE FROM room_command_acks WHERE room_id = $1`,
		`DELETE FROM runtime_events WHERE room_id = $1`,
		`DELETE FROM games WHERE room_id = $1`,
		`DELETE FROM matches WHERE runtime_room_id = $1 OR match_id = $1`,
		`DELETE FROM rooms WHERE room_id = $1`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement, roomID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) GetRoom(ctx context.Context, roomID string) (contracts.RoomRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT room_id, game_type, visibility, owner_user_id, owner_player_id, owner_node,
			epoch, status, max_players, player_count, progress_current, progress_total, last_applied_seq, engine_version,
			current_match_id, next_round_no, created_at, updated_at, lease_expires_at
		FROM rooms
		WHERE room_id = $1`, roomID)
	return scanRoom(row)
}

func (s *PostgresStore) SetRoomVisibility(ctx context.Context, roomID, visibility string) error {
	visibility, err := normalizeRoomVisibility(visibility)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE rooms
		SET visibility = $2, updated_at = clock_timestamp()
		WHERE room_id = $1`, roomID, visibility)
	return err
}

func (s *PostgresStore) SetRoomVisibilityIfOwner(
	ctx context.Context,
	roomID,
	visibility,
	ownerNode string,
	epoch int64,
) error {
	visibility, err := requireRoomVisibility(visibility)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE rooms AS r
		SET visibility = $2, updated_at = clock_timestamp()
		WHERE room_id = $1
		  AND owner_node = $3
		  AND epoch = $4
		  AND EXISTS (
			SELECT 1 FROM room_owner_generations g
			WHERE g.room_id = r.room_id
			  AND g.owner_node = $3
			  AND g.epoch = $4
			  AND g.state = 'ready'
		  )`,
		roomID, visibility, ownerNode, epoch)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrStaleOwnerWrite
	}
	return nil
}

func normalizeRoomVisibility(visibility string) (string, error) {
	if visibility == "" {
		return contracts.VisibilityPublic, nil
	}
	return requireRoomVisibility(visibility)
}

func requireRoomVisibility(visibility string) (string, error) {
	if visibility != contracts.VisibilityPublic && visibility != contracts.VisibilityPrivate {
		return "", errors.New("invalid room visibility")
	}
	return visibility, nil
}

func (s *PostgresStore) RoomVisibility(ctx context.Context, roomID string) (string, error) {
	var visibility string
	err := s.db.QueryRowContext(ctx, `SELECT visibility FROM rooms WHERE room_id = $1`, roomID).Scan(&visibility)
	return visibility, err
}

func (s *PostgresStore) ListRooms(ctx context.Context, gameType string) ([]contracts.RoomRecord, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if gameType == "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT room_id, game_type, visibility, owner_user_id, owner_player_id, owner_node,
				epoch, status, max_players, player_count, progress_current, progress_total, last_applied_seq, engine_version,
				current_match_id, next_round_no, created_at, updated_at, lease_expires_at
			FROM rooms
			ORDER BY created_at DESC`)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT room_id, game_type, visibility, owner_user_id, owner_player_id, owner_node,
				epoch, status, max_players, player_count, progress_current, progress_total, last_applied_seq, engine_version,
				current_match_id, next_round_no, created_at, updated_at, lease_expires_at
			FROM rooms
			WHERE game_type = $1
			ORDER BY created_at DESC`, gameType)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rooms := []contracts.RoomRecord{}
	for rows.Next() {
		room, err := scanRoom(rows)
		if err != nil {
			return nil, err
		}
		rooms = append(rooms, room)
	}
	return rooms, rows.Err()
}

func (s *PostgresStore) PopulateSpectatorRoomAccess(ctx context.Context, rooms []contracts.RoomRecord, userID string) error {
	if len(rooms) == 0 {
		return nil
	}
	roomIDs := make([]string, 0, len(rooms))
	indexByRoomID := make(map[string]int, len(rooms))
	for index := range rooms {
		roomIDs = append(roomIDs, rooms[index].RoomID)
		indexByRoomID[rooms[index].RoomID] = index
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.room_id,
				COALESCE((
					SELECT current_match.status
					FROM matches current_match
					WHERE current_match.match_id = r.current_match_id
				), '') AS current_match_status,
			EXISTS (
				SELECT 1
				FROM spectator_snapshots snapshot
					WHERE snapshot.match_id = r.current_match_id
					AND snapshot.available_at <= clock_timestamp()
			) AS spectator_available,
			(
				r.owner_user_id = $2
				OR EXISTS (
					SELECT 1
					FROM match_players player
						WHERE player.match_id = r.current_match_id
						AND player.user_id = $2
				)
			) AS current_user_participant
		FROM rooms r
		WHERE r.room_id = ANY($1)`,
		pq.Array(roomIDs), userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var roomID string
		var matchStatus string
		var available, participant bool
		if err := rows.Scan(&roomID, &matchStatus, &available, &participant); err != nil {
			return err
		}
		if index, ok := indexByRoomID[roomID]; ok {
			rooms[index].SpectatorAvailable = available
			rooms[index].CurrentUserParticipant = participant
			rooms[index].CurrentMatchStatus = matchStatus
		}
	}
	return rows.Err()
}

func (s *PostgresStore) ListExpiredRooms(ctx context.Context, now time.Time, limit int) ([]contracts.RoomRecord, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT room_id, game_type, visibility, owner_user_id, owner_player_id, owner_node,
			epoch, status, max_players, player_count, progress_current, progress_total, last_applied_seq, engine_version,
			current_match_id, next_round_no, created_at, updated_at, lease_expires_at
		FROM rooms
		WHERE status NOT IN ('closed', 'creating')
			AND owner_node <> ''
			AND lease_expires_at IS NOT NULL
			AND lease_expires_at <= $1
		ORDER BY lease_expires_at ASC
		LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rooms := make([]contracts.RoomRecord, 0, limit)
	for rows.Next() {
		room, err := scanRoom(rows)
		if err != nil {
			return nil, err
		}
		rooms = append(rooms, room)
	}
	return rooms, rows.Err()
}

func (s *PostgresStore) ReserveRoomOwner(ctx context.Context, roomID string, expectedEpoch int64, nodeID string, now time.Time, recoveryTimeout time.Duration) (contracts.RoomRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.RoomRecord{}, err
	}
	defer tx.Rollback()

	rec, err := scanRoom(tx.QueryRowContext(ctx, `
		SELECT room_id, game_type, visibility, owner_user_id, owner_player_id, owner_node,
			epoch, status, max_players, player_count, progress_current, progress_total, last_applied_seq, engine_version,
			current_match_id, next_round_no, created_at, updated_at, lease_expires_at
		FROM rooms
		WHERE room_id = $1
		FOR UPDATE`, roomID))
	if err != nil {
		return contracts.RoomRecord{}, err
	}
	var generationEpoch int64
	var recoveryDeadline time.Time
	if err := tx.QueryRowContext(ctx, `
		SELECT epoch, recovery_deadline
		FROM room_owner_generations
		WHERE room_id = $1
		FOR UPDATE`, roomID).Scan(&generationEpoch, &recoveryDeadline); err != nil {
		return contracts.RoomRecord{}, err
	}
	if contracts.IsTerminalRoomStatus(rec.Status) ||
		rec.Epoch != expectedEpoch ||
		generationEpoch != expectedEpoch ||
		rec.LeaseExpiresAt.IsZero() ||
		rec.LeaseExpiresAt.After(now) ||
		recoveryDeadline.After(now) {
		return contracts.RoomRecord{}, ErrStaleOwnerWrite
	}
	if recoveryTimeout <= 0 {
		recoveryTimeout = 30 * time.Second
	}
	deadline := now.Add(recoveryTimeout)
	nextEpoch := expectedEpoch + 1
	if _, err := tx.ExecContext(ctx, `
		UPDATE room_owner_generations
		SET epoch = $2, owner_node = $3, state = 'recovering',
			recovery_deadline = $4, updated_at = $5
		WHERE room_id = $1`,
		roomID, nextEpoch, nodeID, deadline, now); err != nil {
		return contracts.RoomRecord{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE rooms
		SET owner_node = $2, epoch = $3, lease_expires_at = $4, updated_at = $5
		WHERE room_id = $1`,
		roomID, nodeID, nextEpoch, deadline, now); err != nil {
		return contracts.RoomRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return contracts.RoomRecord{}, err
	}
	rec.OwnerNode = nodeID
	rec.Epoch = nextEpoch
	rec.LeaseExpiresAt = deadline
	rec.UpdatedAt = now
	return rec, nil
}

func (s *PostgresStore) VerifyRoomOwnerCandidate(ctx context.Context, roomID, nodeID string, epoch int64) error {
	var valid bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM rooms r
			JOIN room_owner_generations g ON g.room_id = r.room_id
			WHERE r.room_id = $1
			  AND r.owner_node = $2
			  AND r.epoch = $3
			  AND g.owner_node = $2
			  AND g.epoch = $3
			  AND g.state IN ('recovering', 'ready')
		)`, roomID, nodeID, epoch).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrStaleOwnerWrite
	}
	return nil
}

func (s *PostgresStore) MarkRoomOwnerReady(ctx context.Context, roomID, nodeID string, epoch int64, leaseExpiresAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE room_owner_generations
		SET state = 'ready', recovery_deadline = $4, updated_at = clock_timestamp()
		WHERE room_id = $1 AND owner_node = $2 AND epoch = $3 AND state IN ('recovering', 'ready')`,
		roomID, nodeID, epoch, leaseExpiresAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrStaleOwnerWrite
	}
	result, err = tx.ExecContext(ctx, `
		UPDATE rooms
		SET lease_expires_at = $4, updated_at = clock_timestamp()
		WHERE room_id = $1 AND owner_node = $2 AND epoch = $3`,
		roomID, nodeID, epoch, leaseExpiresAt)
	if err != nil {
		return err
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrStaleOwnerWrite
	}
	return tx.Commit()
}

func (s *PostgresStore) RenewRoomOwnerProjection(ctx context.Context, roomID, nodeID string, epoch int64, leaseExpiresAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE room_owner_generations
		SET recovery_deadline = $4, updated_at = clock_timestamp()
		WHERE room_id = $1 AND owner_node = $2 AND epoch = $3`,
		roomID, nodeID, epoch, leaseExpiresAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrStaleOwnerWrite
	}
	result, err = tx.ExecContext(ctx, `
		UPDATE rooms
		SET lease_expires_at = $4, updated_at = clock_timestamp()
		WHERE room_id = $1 AND owner_node = $2 AND epoch = $3`,
		roomID, nodeID, epoch, leaseExpiresAt)
	if err != nil {
		return err
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrStaleOwnerWrite
	}
	return tx.Commit()
}

func (s *PostgresStore) RoomCountsByOwner(ctx context.Context, pendingSince time.Time) (map[string]int, map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT owner_node,
			COUNT(*) FILTER (
				WHERE status <> 'closed'
			) AS assigned_rooms,
			COUNT(*) FILTER (
				WHERE status <> 'closed'
					AND created_at >= $1
					AND player_count = 0
			) AS pending_rooms
		FROM rooms
		WHERE owner_node <> ''
		GROUP BY owner_node`, pendingSince)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	assigned := map[string]int{}
	pending := map[string]int{}
	for rows.Next() {
		var ownerNode string
		var assignedRooms, pendingRooms int
		if err := rows.Scan(&ownerNode, &assignedRooms, &pendingRooms); err != nil {
			return nil, nil, err
		}
		assigned[ownerNode] = assignedRooms
		pending[ownerNode] = pendingRooms
	}
	return assigned, pending, rows.Err()
}

func (s *PostgresStore) ReserveNextMatch(ctx context.Context, roomID, requestID string, randomSeed uint64) (contracts.RoomRecord, contracts.MatchRecord, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.RoomRecord{}, contracts.MatchRecord{}, false, err
	}
	defer tx.Rollback()

	var existingMatchID string
	err = tx.QueryRowContext(ctx, `
		SELECT match_id FROM match_start_requests
		WHERE room_id = $1 AND request_id = $2`, roomID, requestID).Scan(&existingMatchID)
	if err == nil {
		match, err := scanMatch(tx.QueryRowContext(ctx, `
			SELECT match_id, runtime_room_id, game_type, status, owner_user_id, owner_player_id,
				runtime_owner_node, epoch, max_players, player_count, engine_version,
				round_no, last_applied_seq, random_seed,
				started_at, updated_at, ended_at, duration_seconds,
				winner_user_id, winner_player_id, end_reason
			FROM matches WHERE match_id = $1`, existingMatchID))
		if err != nil {
			return contracts.RoomRecord{}, contracts.MatchRecord{}, false, err
		}
		room, err := scanRoom(tx.QueryRowContext(ctx, `
			SELECT room_id, game_type, visibility, owner_user_id, owner_player_id, owner_node,
				epoch, status, max_players, player_count, progress_current, progress_total, last_applied_seq, engine_version,
				current_match_id, next_round_no, created_at, updated_at, lease_expires_at
			FROM rooms WHERE room_id = $1`, roomID))
		return room, match, true, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return contracts.RoomRecord{}, contracts.MatchRecord{}, false, err
	}

	room, err := scanRoom(tx.QueryRowContext(ctx, `
		SELECT room_id, game_type, visibility, owner_user_id, owner_player_id, owner_node,
			epoch, status, max_players, player_count, progress_current, progress_total, last_applied_seq, engine_version,
			current_match_id, next_round_no, created_at, updated_at, lease_expires_at
		FROM rooms WHERE room_id = $1 FOR UPDATE`, roomID))
	if err != nil {
		return contracts.RoomRecord{}, contracts.MatchRecord{}, false, err
	}
	if room.Status != contracts.RoomStatusCreating && room.Status != contracts.RoomStatusOpen {
		return contracts.RoomRecord{}, contracts.MatchRecord{}, false, ErrRoomNotOpen
	}
	if room.CurrentMatchID != "" {
		var currentStatus string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM matches WHERE match_id = $1`, room.CurrentMatchID).Scan(&currentStatus); err != nil {
			return contracts.RoomRecord{}, contracts.MatchRecord{}, false, err
		}
		if !contracts.IsTerminalMatchStatus(currentStatus) {
			return contracts.RoomRecord{}, contracts.MatchRecord{}, false, ErrMatchActive
		}
	}
	roundNo := room.NextRoundNo
	if roundNo < 1 {
		roundNo = 1
	}
	now := time.Now().UTC()
	matchID := fmt.Sprintf("%s-r%d", room.RoomID, roundNo)
	match := contracts.MatchRecord{
		MatchID:          matchID,
		RuntimeRoomID:    room.RoomID,
		GameType:         room.GameType,
		Status:           contracts.MatchStatusPreparing,
		OwnerUserID:      room.OwnerUserID,
		OwnerPlayerID:    room.OwnerPlayerID,
		RuntimeOwnerNode: room.OwnerNode,
		Epoch:            room.Epoch,
		MaxPlayers:       room.MaxPlayers,
		EngineVersion:    room.EngineVersion,
		RoundNo:          roundNo,
		RandomSeed:       randomSeed,
		StartedAt:        now,
		UpdatedAt:        now,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO matches (
			match_id, runtime_room_id, game_type, status, owner_user_id, owner_player_id,
			runtime_owner_node, epoch, max_players, player_count, engine_version,
			round_no, last_applied_seq, random_seed, started_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,0,$10,$11,0,$12,$13,$13)`,
		match.MatchID, match.RuntimeRoomID, match.GameType, match.Status,
		match.OwnerUserID, match.OwnerPlayerID, match.RuntimeOwnerNode, match.Epoch,
		match.MaxPlayers, match.EngineVersion, match.RoundNo,
		strconv.FormatUint(match.RandomSeed, 10), now); err != nil {
		return contracts.RoomRecord{}, contracts.MatchRecord{}, false, err
	}
	room.CurrentMatchID = matchID
	room.NextRoundNo = roundNo + 1
	room.LastAppliedSeq = 0
	room.ProgressCurrent = 0
	room.ProgressTotal = 0
	room.UpdatedAt = now
	if _, err := tx.ExecContext(ctx, `
		UPDATE rooms SET current_match_id = $2, next_round_no = $3,
			last_applied_seq = 0, progress_current = 0, progress_total = 0, updated_at = $4
		WHERE room_id = $1`, room.RoomID, room.CurrentMatchID, room.NextRoundNo, now); err != nil {
		return contracts.RoomRecord{}, contracts.MatchRecord{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO match_start_requests (room_id, request_id, match_id, created_at)
		VALUES ($1,$2,$3,$4)`, room.RoomID, requestID, match.MatchID, now); err != nil {
		return contracts.RoomRecord{}, contracts.MatchRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return contracts.RoomRecord{}, contracts.MatchRecord{}, false, err
	}
	return room, match, false, nil
}

func (s *PostgresStore) SetMatchRuntime(ctx context.Context, matchID, status, ownerNode string, epoch, lastAppliedSeq int64, playerCount int) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE matches AS m SET status = $2, runtime_owner_node = $3,
			last_applied_seq = GREATEST(last_applied_seq, $5),
			player_count = $6, updated_at = clock_timestamp()
		WHERE match_id = $1
		  AND runtime_owner_node = $3
		  AND epoch = $4
		  AND EXISTS (
			SELECT 1 FROM room_owner_generations g
			WHERE g.room_id = m.runtime_room_id
			  AND g.owner_node = $3
			  AND g.epoch = $4
			  AND g.state = 'ready'
		  )`,
		matchID, status, ownerNode, epoch, lastAppliedSeq, playerCount)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrStaleOwnerWrite
	}
	return nil
}

func (s *PostgresStore) ClaimMatchRuntime(ctx context.Context, matchID, ownerNode string, epoch int64) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE matches AS m
		SET runtime_owner_node = $2, epoch = $3, updated_at = clock_timestamp()
		WHERE match_id = $1
		  AND epoch <= $3
		  AND status NOT IN ('ended', 'aborted')
		  AND EXISTS (
			SELECT 1 FROM room_owner_generations g
			WHERE g.room_id = m.runtime_room_id
			  AND g.owner_node = $2
			  AND g.epoch = $3
			  AND g.state IN ('recovering', 'ready')
		  )`,
		matchID, ownerNode, epoch)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrStaleOwnerWrite
	}
	return nil
}

func (s *PostgresStore) FinishMatch(ctx context.Context, roomID, matchID, status, reason, ownerNode string, epoch, lastAppliedSeq int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `
		UPDATE matches AS m SET status = $2, last_applied_seq = GREATEST(last_applied_seq, $3),
			ended_at = COALESCE(ended_at, $4),
			duration_seconds = GREATEST(0, FLOOR(EXTRACT(EPOCH FROM ($4 - started_at)))::BIGINT),
			end_reason = $5, updated_at = $4
		WHERE match_id = $1
		  AND runtime_owner_node = $6
		  AND epoch = $7
		  AND status NOT IN ('ended', 'aborted')
		  AND EXISTS (
			SELECT 1 FROM room_owner_generations g
			WHERE g.room_id = m.runtime_room_id
			  AND g.owner_node = $6
			  AND g.epoch = $7
			  AND g.state = 'ready'
		  )`,
		matchID, status, lastAppliedSeq, now, reason, ownerNode, epoch)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrStaleOwnerWrite
	}
	result, err = tx.ExecContext(ctx, `
		UPDATE rooms SET last_applied_seq = $3, updated_at = $4
		WHERE room_id = $1 AND current_match_id = $2
		  AND owner_node = $5 AND epoch = $6`,
		roomID, matchID, lastAppliedSeq, now, ownerNode, epoch)
	if err != nil {
		return err
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrStaleOwnerWrite
	}
	return tx.Commit()
}

func (s *PostgresStore) ListMatches(ctx context.Context, gameType, status, userID string, limit int) ([]contracts.MatchRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT match_id, runtime_room_id, game_type, status, owner_user_id, owner_player_id,
			runtime_owner_node, epoch, max_players, player_count, engine_version,
			round_no, last_applied_seq, random_seed,
			started_at, updated_at, ended_at, duration_seconds,
			winner_user_id, winner_player_id, end_reason
		FROM matches m
		WHERE ($1 = '' OR m.game_type = $1)
			AND ($2 = '' OR m.status = $2)
			AND (
				$3 = ''
				OR m.owner_user_id = $3
				OR EXISTS (
					SELECT 1 FROM match_players mp
					WHERE mp.match_id = m.match_id AND mp.user_id = $3
				)
			)
		ORDER BY started_at DESC
		LIMIT $4`, gameType, status, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	matches := []contracts.MatchRecord{}
	for rows.Next() {
		match, err := scanMatch(rows)
		if err != nil {
			return nil, err
		}
		players, err := s.listMatchPlayers(ctx, match.MatchID)
		if err != nil {
			return nil, err
		}
		match.Players = players
		matches = append(matches, match)
	}
	return matches, rows.Err()
}

func (s *PostgresStore) GetMatch(ctx context.Context, matchID string) (contracts.MatchRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT match_id, runtime_room_id, game_type, status, owner_user_id, owner_player_id,
			runtime_owner_node, epoch, max_players, player_count, engine_version,
			round_no, last_applied_seq, random_seed,
			started_at, updated_at, ended_at, duration_seconds,
			winner_user_id, winner_player_id, end_reason
		FROM matches
		WHERE match_id = $1`, matchID)
	match, err := scanMatch(row)
	if err != nil {
		return contracts.MatchRecord{}, err
	}
	players, err := s.listMatchPlayers(ctx, match.MatchID)
	if err != nil {
		return contracts.MatchRecord{}, err
	}
	match.Players = players
	return match, nil
}

func (s *PostgresStore) UpsertMatchPlayer(ctx context.Context, matchID string, player contracts.MatchPlayerRecord) error {
	if matchID == "" || player.PlayerID == "" {
		return nil
	}
	now := time.Now().UTC()
	if player.JoinedAt.IsZero() {
		player.JoinedAt = now
	}
	if player.LastSeen.IsZero() {
		player.LastSeen = player.JoinedAt
	}
	if player.UserID == "" {
		player.UserID = player.PlayerID
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO match_players (
			match_id, user_id, player_id, display_name, name, email, avatar,
				gateway_node, online, is_ai, seat_index, ready, joined_at, last_seen
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (match_id, player_id) DO UPDATE SET
			user_id = EXCLUDED.user_id,
				display_name = COALESCE(NULLIF(EXCLUDED.display_name, ''), match_players.display_name),
				name = COALESCE(NULLIF(EXCLUDED.name, ''), match_players.name),
				email = COALESCE(NULLIF(EXCLUDED.email, ''), match_players.email),
				avatar = COALESCE(NULLIF(EXCLUDED.avatar, ''), match_players.avatar),
				gateway_node = COALESCE(NULLIF(EXCLUDED.gateway_node, ''), match_players.gateway_node),
			online = EXCLUDED.online,
				is_ai = EXCLUDED.is_ai,
				seat_index = EXCLUDED.seat_index,
				ready = EXCLUDED.ready,
			joined_at = COALESCE(match_players.joined_at, EXCLUDED.joined_at),
			last_seen = EXCLUDED.last_seen`,
		matchID, player.UserID, player.PlayerID, player.DisplayName, player.Name, player.Email, player.Avatar,
		player.GatewayNode, player.Online, player.IsAI, player.SeatIndex, player.Ready,
		nullableTime(player.JoinedAt), nullableTime(player.LastSeen))
	return err
}

func (s *PostgresStore) SyncMatchRoster(ctx context.Context, matchID string, players []contracts.MatchPlayerRecord) error {
	aiPlayerIDs := make([]string, 0, len(players))
	for _, player := range players {
		if err := s.UpsertMatchPlayer(ctx, matchID, player); err != nil {
			return err
		}
		if player.IsAI {
			aiPlayerIDs = append(aiPlayerIDs, player.PlayerID)
		}
	}
	if len(aiPlayerIDs) == 0 {
		_, err := s.db.ExecContext(ctx, `DELETE FROM match_players WHERE match_id = $1 AND is_ai = true`, matchID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM match_players
		WHERE match_id = $1
			AND is_ai = true
			AND NOT (player_id = ANY($2))`, matchID, pq.Array(aiPlayerIDs))
	return err
}

func (s *PostgresStore) MarkMatchPlayerOffline(ctx context.Context, matchID, playerID string, lastSeen time.Time) error {
	if matchID == "" || playerID == "" {
		return nil
	}
	if lastSeen.IsZero() {
		lastSeen = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE match_players
		SET online = false,
			last_seen = $3
		WHERE match_id = $1 AND player_id = $2`, matchID, playerID, lastSeen)
	return err
}

func (s *PostgresStore) listMatchPlayers(ctx context.Context, matchID string) ([]contracts.MatchPlayerRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id, player_id, display_name, name, email, avatar,
			gateway_node, online, is_ai, seat_index, ready, joined_at, last_seen
		FROM match_players
		WHERE match_id = $1
		ORDER BY joined_at ASC, player_id ASC`, matchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	players := []contracts.MatchPlayerRecord{}
	for rows.Next() {
		player, err := scanMatchPlayer(rows)
		if err != nil {
			return nil, err
		}
		players = append(players, player)
	}
	return players, rows.Err()
}

func (s *PostgresStore) UpdateRoom(ctx context.Context, roomID string, fn func(*contracts.RoomRecord) error) (contracts.RoomRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.RoomRecord{}, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		SELECT room_id, game_type, visibility, owner_user_id, owner_player_id, owner_node,
			epoch, status, max_players, player_count, progress_current, progress_total, last_applied_seq, engine_version,
			current_match_id, next_round_no, created_at, updated_at, lease_expires_at
		FROM rooms
		WHERE room_id = $1
		FOR UPDATE`, roomID)
	rec, err := scanRoom(row)
	if err != nil {
		return contracts.RoomRecord{}, err
	}
	if err := fn(&rec); err != nil {
		return contracts.RoomRecord{}, err
	}
	rec.UpdatedAt = time.Now().UTC()
	// Runtime visibility changes must use SetRoomVisibilityIfOwner.
	if _, err := tx.ExecContext(ctx, `
		UPDATE rooms SET
			game_type = $2,
			owner_user_id = $3,
			owner_player_id = $4,
			owner_node = $5,
			epoch = $6,
			status = $7,
			max_players = $8,
			player_count = $9,
				progress_current = $10,
				progress_total = $11,
				last_applied_seq = $12,
				engine_version = $13,
				current_match_id = $14,
				next_round_no = $15,
				updated_at = $16,
				lease_expires_at = $17
		WHERE room_id = $1`,
		rec.RoomID, rec.GameType, rec.OwnerUserID, rec.OwnerPlayerID, rec.OwnerNode,
		rec.Epoch, rec.Status, rec.MaxPlayers, rec.PlayerCount, rec.ProgressCurrent, rec.ProgressTotal, rec.LastAppliedSeq, rec.EngineVersion,
		rec.CurrentMatchID, rec.NextRoundNo, rec.UpdatedAt, nullableTime(rec.LeaseExpiresAt),
	); err != nil {
		return contracts.RoomRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return contracts.RoomRecord{}, err
	}
	return rec, nil
}

func (s *PostgresStore) SaveSnapshot(ctx context.Context, rec contracts.SnapshotRecord) error {
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO room_snapshots (room_id, match_id, seq, epoch, engine_version, state, created_at)
		SELECT $1,$2,$3,$4,$5,$6,$7
		WHERE EXISTS (
			SELECT 1 FROM room_owner_generations
			WHERE room_id = $1 AND epoch = $4
		)
		ON CONFLICT (match_id, seq) DO UPDATE SET
			epoch = EXCLUDED.epoch,
			engine_version = EXCLUDED.engine_version,
			state = EXCLUDED.state,
			created_at = EXCLUDED.created_at
		WHERE room_snapshots.epoch <= EXCLUDED.epoch`,
		rec.RoomID, rec.MatchID, rec.Seq, rec.Epoch, rec.EngineVersion, rec.State, rec.CreatedAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrStaleRoomWrite
	}
	return nil
}

func (s *PostgresStore) LatestSnapshot(ctx context.Context, matchID string) (contracts.SnapshotRecord, error) {
	var rec contracts.SnapshotRecord
	err := s.db.QueryRowContext(ctx, `
		SELECT room_id, match_id, seq, epoch, engine_version, state, created_at
		FROM room_snapshots
		WHERE match_id = $1
		ORDER BY seq DESC
		LIMIT 1`, matchID).Scan(&rec.RoomID, &rec.MatchID, &rec.Seq, &rec.Epoch, &rec.EngineVersion, &rec.State, &rec.CreatedAt)
	return rec, err
}

func (s *PostgresStore) SaveSpectatorSnapshot(ctx context.Context, rec contracts.SpectatorSnapshotRecord) error {
	payload, err := json.Marshal(rec.Payload)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO spectator_snapshots (
			room_id, match_id, game_type, seq, epoch, owner_node, payload, captured_at, available_at
		)
		SELECT
			$1,$2,$3,$4,$5,$6,$7,
			clock_timestamp(),
			clock_timestamp() + ($8 * interval '1 second')
		WHERE EXISTS (
			SELECT 1 FROM room_owner_generations
			WHERE room_id = $1 AND epoch = $5 AND owner_node = $6
		)
		ON CONFLICT (match_id, seq) DO NOTHING`,
		rec.RoomID, rec.MatchID, rec.GameType, rec.Seq, rec.Epoch, rec.OwnerNode, string(payload), int(contracts.SpectatorDelay.Seconds()))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 1 {
		return nil
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM spectator_snapshots WHERE match_id = $1 AND seq = $2
		)`, rec.MatchID, rec.Seq).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	return ErrStaleOwnerWrite
}

func (s *PostgresStore) CreateSpectatorSession(ctx context.Context, session contracts.SpectatorSession) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status, ownerUserID, currentMatchID string
	if err := tx.QueryRowContext(ctx, `
		SELECT status, owner_user_id, current_match_id
		FROM rooms
		WHERE room_id = $1
		FOR SHARE`, session.RoomID).Scan(&status, &ownerUserID, &currentMatchID); err != nil {
		return err
	}
	if status != contracts.RoomStatusOpen || currentMatchID == "" {
		return ErrSpectatorUnavailable
	}
	if session.MatchID != currentMatchID {
		return ErrSpectatorUnavailable
	}
	var matchStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM matches WHERE match_id = $1`, currentMatchID).Scan(&matchStatus); err != nil {
		return err
	}
	if matchStatus != contracts.MatchStatusRunning {
		return ErrSpectatorUnavailable
	}
	var participant bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM match_players
			WHERE match_id = $1 AND user_id = $2
			)`, session.MatchID, session.UserID).Scan(&participant); err != nil {
		return err
	}
	if ownerUserID == session.UserID || participant {
		return ErrSpectatorParticipant
	}
	ttl := session.ExpiresAt.Sub(session.CreatedAt)
	if ttl <= 0 {
		return errors.New("spectator session ttl is required")
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO spectator_sessions (
			session_id, room_id, match_id, user_id, created_at, expires_at
		) VALUES (
			$1,$2,$3,$4,
			clock_timestamp(),
			clock_timestamp() + ($5 * interval '1 second')
		)`,
		session.SessionID, session.RoomID, session.MatchID, session.UserID, int(ttl.Seconds()))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) GetSpectatorSession(ctx context.Context, sessionID, userID string) (contracts.SpectatorSession, error) {
	var session contracts.SpectatorSession
	err := s.db.QueryRowContext(ctx, `
		SELECT session_id, room_id, match_id, user_id, created_at, expires_at
		FROM spectator_sessions
		WHERE session_id = $1
			AND user_id = $2
			AND expires_at > clock_timestamp()`,
		sessionID, userID).Scan(
		&session.SessionID,
		&session.RoomID,
		&session.MatchID,
		&session.UserID,
		&session.CreatedAt,
		&session.ExpiresAt,
	)
	return session, err
}

func (s *PostgresStore) LatestAvailableSpectatorSnapshot(ctx context.Context, matchID string) (contracts.SpectatorSnapshotRecord, error) {
	var rec contracts.SpectatorSnapshotRecord
	var payload []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT room_id, match_id, game_type, seq, epoch, owner_node, payload, captured_at, available_at
		FROM spectator_snapshots
		WHERE match_id = $1 AND available_at <= clock_timestamp()
		ORDER BY seq DESC
		LIMIT 1`, matchID).Scan(
		&rec.RoomID,
		&rec.MatchID,
		&rec.GameType,
		&rec.Seq,
		&rec.Epoch,
		&rec.OwnerNode,
		&payload,
		&rec.CapturedAt,
		&rec.AvailableAt,
	)
	if err != nil {
		return contracts.SpectatorSnapshotRecord{}, err
	}
	if err := json.Unmarshal(payload, &rec.Payload); err != nil {
		return contracts.SpectatorSnapshotRecord{}, err
	}
	return rec, nil
}

func (s *PostgresStore) NextSpectatorSnapshotAvailability(ctx context.Context, matchID string) (time.Time, error) {
	var next sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT MIN(available_at)
		FROM spectator_snapshots
		WHERE match_id = $1 AND available_at > clock_timestamp()`, matchID).Scan(&next)
	if err != nil {
		return time.Time{}, err
	}
	if !next.Valid {
		return time.Time{}, sql.ErrNoRows
	}
	return next.Time, nil
}

func (s *PostgresStore) SaveOutboundMessage(ctx context.Context, msg contracts.OutboundMessage) error {
	payload, err := json.Marshal(msg.Payload)
	if err != nil {
		return err
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO outbound_messages (
			message_id, room_id, match_id, seq, epoch, owner_node, target_user_id, target_player_id,
			target_session_id, target_gateway_node, trace_id, reliable,
			close_after, payload, created_at
		)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15
		WHERE EXISTS (
			SELECT 1 FROM room_owner_generations
			WHERE room_id = $2 AND epoch = $5 AND owner_node = $6
		)
		ON CONFLICT (message_id) DO NOTHING`,
		msg.MessageID, msg.RoomID, msg.MatchID, msg.Seq, msg.Epoch, msg.OwnerNode, msg.TargetUserID, msg.TargetPlayerID,
		msg.TargetSessionIDValue(), msg.TargetGatewayNodeValue(), msg.TraceID, msg.Reliable,
		msg.CloseAfter, string(payload), msg.CreatedAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrStaleOwnerWrite
	}
	return nil
}

func (s *PostgresStore) MarkOutboundDelivered(ctx context.Context, messageID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE outbound_messages
		SET delivered_at = COALESCE(delivered_at, $2),
			ack_attempts = ack_attempts + 1
		WHERE message_id = $1`,
		messageID, time.Now().UTC())
	return err
}

func (s *PostgresStore) ListOutboundMessages(ctx context.Context, roomID, matchID, playerID string, afterSeq int64, limit int) ([]contracts.OutboundMessage, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT message_id, room_id, match_id, seq, epoch, owner_node, target_user_id, target_player_id,
			target_session_id, target_gateway_node, trace_id, reliable,
			close_after, payload, created_at
		FROM outbound_messages
		WHERE room_id = $1
			AND match_id = $2
			AND target_player_id = $3
			AND seq > $4
		ORDER BY seq ASC, created_at ASC
		LIMIT $5`, roomID, matchID, playerID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []contracts.OutboundMessage{}
	for rows.Next() {
		var (
			msg        contracts.OutboundMessage
			targetConn contracts.ConnectionRef
			payload    []byte
		)
		if err := rows.Scan(
			&msg.MessageID,
			&msg.RoomID,
			&msg.MatchID,
			&msg.Seq,
			&msg.Epoch,
			&msg.OwnerNode,
			&msg.TargetUserID,
			&msg.TargetPlayerID,
			&targetConn.SessionID,
			&targetConn.GatewayNode,
			&msg.TraceID,
			&msg.Reliable,
			&msg.CloseAfter,
			&payload,
			&msg.CreatedAt,
		); err != nil {
			return nil, err
		}
		msg.TargetConn = targetConn
		if err := json.Unmarshal(payload, &msg.Payload); err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, rows.Err()
}

func (s *PostgresStore) LatestOutboundMessage(ctx context.Context, roomID, matchID, playerID string) (contracts.OutboundMessage, error) {
	var (
		msg        contracts.OutboundMessage
		targetConn contracts.ConnectionRef
		payload    []byte
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT message_id, room_id, match_id, seq, epoch, owner_node, target_user_id, target_player_id,
			target_session_id, target_gateway_node, trace_id, reliable,
			close_after, payload, created_at
		FROM outbound_messages
		WHERE room_id = $1 AND match_id = $2 AND target_player_id = $3
		ORDER BY seq DESC, created_at DESC
		LIMIT 1`, roomID, matchID, playerID).Scan(
		&msg.MessageID,
		&msg.RoomID,
		&msg.MatchID,
		&msg.Seq,
		&msg.Epoch,
		&msg.OwnerNode,
		&msg.TargetUserID,
		&msg.TargetPlayerID,
		&targetConn.SessionID,
		&targetConn.GatewayNode,
		&msg.TraceID,
		&msg.Reliable,
		&msg.CloseAfter,
		&payload,
		&msg.CreatedAt,
	)
	if err != nil {
		return contracts.OutboundMessage{}, err
	}
	msg.TargetConn = targetConn
	if err := json.Unmarshal(payload, &msg.Payload); err != nil {
		return contracts.OutboundMessage{}, err
	}
	return msg, nil
}

func (s *PostgresStore) OutboundSeqRange(ctx context.Context, roomID, matchID, playerID string) (int64, int64, error) {
	var minSeq, maxSeq sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT MIN(seq), MAX(seq)
		FROM outbound_messages
		WHERE room_id = $1 AND match_id = $2 AND target_player_id = $3`, roomID, matchID, playerID).
		Scan(&minSeq, &maxSeq)
	if err != nil {
		return 0, 0, err
	}
	if !minSeq.Valid || !maxSeq.Valid {
		return 0, 0, sql.ErrNoRows
	}
	return minSeq.Int64, maxSeq.Int64, nil
}

func (s *PostgresStore) SaveCommandAck(ctx context.Context, ack contracts.CommandAck) error {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO room_command_acks (
			room_id, match_id, command_id, epoch, owner_node, intent_hash, accepted, seq, error_code, message, created_at
		)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11
		WHERE EXISTS (
			SELECT 1 FROM room_owner_generations
			WHERE room_id = $1 AND epoch = $4 AND owner_node = $5
		)
		ON CONFLICT (match_id, command_id) DO NOTHING`,
		ack.RoomID, ack.MatchID, ack.CommandID, ack.Epoch, ack.OwnerNode,
		ack.IntentHash, ack.Accepted, ack.Seq, ack.ErrorCode, ack.Message, time.Now().UTC())
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 1 {
		return nil
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM room_command_acks WHERE match_id = $1 AND command_id = $2
		)`, ack.MatchID, ack.CommandID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	return ErrStaleOwnerWrite
}

func (s *PostgresStore) AppendGameLogEvents(ctx context.Context, events []contracts.GameLogEvent) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, event := range events {
		payload, err := json.Marshal(event.Payload)
		if err != nil {
			return err
		}
		if event.CreatedAt.IsZero() {
			event.CreatedAt = time.Now().UTC()
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO game_operation_events (
				event_id, room_id, match_id, game_type, seq, command_id, player_id,
				visibility, event_type, payload, created_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (event_id) DO NOTHING`,
			event.EventID, event.RoomID, event.MatchID, event.GameType, event.Seq, event.CommandID,
			event.PlayerID, event.Visibility, event.Type, string(payload), event.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) GetCommandAck(ctx context.Context, roomID, matchID, commandID, intentHash string) (contracts.CommandAck, error) {
	var ack contracts.CommandAck
	err := s.db.QueryRowContext(ctx, `
		SELECT command_id, room_id, match_id, epoch, owner_node, intent_hash, accepted, seq, error_code, message
		FROM room_command_acks
		WHERE room_id = $1 AND match_id = $2 AND command_id = $3`, roomID, matchID, commandID).
		Scan(&ack.CommandID, &ack.RoomID, &ack.MatchID, &ack.Epoch, &ack.OwnerNode, &ack.IntentHash, &ack.Accepted, &ack.Seq, &ack.ErrorCode, &ack.Message)
	if err != nil {
		return contracts.CommandAck{}, err
	}
	if ack.IntentHash != "" && ack.IntentHash != intentHash {
		return contracts.CommandAck{}, ErrCommandIntentConflict
	}
	return ack, nil
}

func (s *PostgresStore) SaveRuntimeEvent(ctx context.Context, event contracts.RuntimeEvent) error {
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return err
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO runtime_events (
			room_id, match_id, trace_id, node_id, component, event, message, payload, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		event.RoomID, event.MatchID, event.TraceID, event.NodeID, event.Component, event.Event, event.Message, string(payload), event.CreatedAt)
	return err
}

func (s *PostgresStore) ListRuntimeEvents(ctx context.Context, roomID string, afterID int64, limit int) ([]contracts.RuntimeEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, room_id, match_id, trace_id, node_id, component, event, message, payload, created_at
		FROM runtime_events
		WHERE room_id = $1 AND id > $2
		ORDER BY id ASC
		LIMIT $3`, roomID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []contracts.RuntimeEvent{}
	for rows.Next() {
		var (
			event   contracts.RuntimeEvent
			payload []byte
		)
		if err := rows.Scan(
			&event.ID,
			&event.RoomID,
			&event.MatchID,
			&event.TraceID,
			&event.NodeID,
			&event.Component,
			&event.Event,
			&event.Message,
			&payload,
			&event.CreatedAt,
		); err != nil {
			return nil, err
		}
		if len(payload) > 0 {
			var data interface{}
			if err := json.Unmarshal(payload, &data); err != nil {
				return nil, err
			}
			event.Payload = data
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CleanupOperationalData(ctx context.Context, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []struct {
		query  string
		before time.Time
	}{
		{query: `WITH doomed AS (
			SELECT id FROM runtime_events WHERE created_at < $1 ORDER BY id LIMIT 5000
		) DELETE FROM runtime_events WHERE id IN (SELECT id FROM doomed)`, before: now.Add(-30 * 24 * time.Hour)},
		{query: `WITH doomed AS (
			SELECT message_id FROM outbound_messages
			WHERE delivered_at IS NOT NULL AND created_at < $1
			ORDER BY created_at LIMIT 5000
		) DELETE FROM outbound_messages WHERE message_id IN (SELECT message_id FROM doomed)`, before: now.Add(-7 * 24 * time.Hour)},
		{query: `WITH doomed AS (
			SELECT a.room_id, a.command_id
			FROM room_command_acks a
			JOIN rooms r ON r.room_id = a.room_id
			WHERE r.status = 'closed' AND a.created_at < $1
			ORDER BY a.created_at LIMIT 5000
		) DELETE FROM room_command_acks a
		USING doomed d
		WHERE a.room_id = d.room_id AND a.command_id = d.command_id`, before: now.Add(-7 * 24 * time.Hour)},
		{query: `WITH doomed AS (
			SELECT owner_user_id, request_id
			FROM room_create_requests
			WHERE created_at < $1
			ORDER BY created_at LIMIT 5000
		) DELETE FROM room_create_requests r
		USING doomed d
		WHERE r.owner_user_id = d.owner_user_id AND r.request_id = d.request_id`, before: now.Add(-24 * time.Hour)},
		{query: `WITH doomed AS (
			SELECT session_id
			FROM spectator_sessions
			WHERE expires_at < $1
			ORDER BY expires_at LIMIT 5000
		) DELETE FROM spectator_sessions
		WHERE session_id IN (SELECT session_id FROM doomed)`, before: now},
		{query: `WITH doomed AS (
			SELECT room_id, seq
			FROM spectator_snapshots
			WHERE captured_at < $1
			ORDER BY captured_at LIMIT 5000
		) DELETE FROM spectator_snapshots s
		USING doomed d
		WHERE s.room_id = d.room_id AND s.seq = d.seq`, before: now.Add(-7 * 24 * time.Hour)},
		{query: `WITH ranked AS (
			SELECT s.room_id, s.seq,
				ROW_NUMBER() OVER (PARTITION BY s.room_id ORDER BY s.seq DESC) AS position
			FROM room_snapshots s
			JOIN rooms r ON r.room_id = s.room_id
			WHERE r.status = 'closed' AND s.created_at < $1
		), doomed AS (
			SELECT room_id, seq FROM ranked WHERE position > 1 LIMIT 5000
		) DELETE FROM room_snapshots s
		USING doomed d
		WHERE s.room_id = d.room_id AND s.seq = d.seq`, before: now.Add(-7 * 24 * time.Hour)},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, statement.before); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type roomScanner interface {
	Scan(dest ...any) error
}

func scanMatch(row roomScanner) (contracts.MatchRecord, error) {
	var (
		rec     contracts.MatchRecord
		endedAt sql.NullTime
		seed    string
	)
	err := row.Scan(
		&rec.MatchID,
		&rec.RuntimeRoomID,
		&rec.GameType,
		&rec.Status,
		&rec.OwnerUserID,
		&rec.OwnerPlayerID,
		&rec.RuntimeOwnerNode,
		&rec.Epoch,
		&rec.MaxPlayers,
		&rec.PlayerCount,
		&rec.EngineVersion,
		&rec.RoundNo,
		&rec.LastAppliedSeq,
		&seed,
		&rec.StartedAt,
		&rec.UpdatedAt,
		&endedAt,
		&rec.DurationSeconds,
		&rec.WinnerUserID,
		&rec.WinnerPlayerID,
		&rec.EndReason,
	)
	if err != nil {
		return contracts.MatchRecord{}, err
	}
	if endedAt.Valid {
		rec.EndedAt = endedAt.Time
	}
	if seed != "" {
		rec.RandomSeed, err = strconv.ParseUint(seed, 10, 64)
		if err != nil {
			return contracts.MatchRecord{}, err
		}
	}
	return rec, nil
}

func scanMatchPlayer(row roomScanner) (contracts.MatchPlayerRecord, error) {
	var (
		rec      contracts.MatchPlayerRecord
		joinedAt sql.NullTime
		lastSeen sql.NullTime
	)
	err := row.Scan(
		&rec.UserID,
		&rec.PlayerID,
		&rec.DisplayName,
		&rec.Name,
		&rec.Email,
		&rec.Avatar,
		&rec.GatewayNode,
		&rec.Online,
		&rec.IsAI,
		&rec.SeatIndex,
		&rec.Ready,
		&joinedAt,
		&lastSeen,
	)
	if err != nil {
		return contracts.MatchPlayerRecord{}, err
	}
	if joinedAt.Valid {
		rec.JoinedAt = joinedAt.Time
	}
	if lastSeen.Valid {
		rec.LastSeen = lastSeen.Time
	}
	return rec, nil
}

func scanRoom(row roomScanner) (contracts.RoomRecord, error) {
	var (
		rec   contracts.RoomRecord
		lease sql.NullTime
	)
	err := row.Scan(
		&rec.RoomID,
		&rec.GameType,
		&rec.Visibility,
		&rec.OwnerUserID,
		&rec.OwnerPlayerID,
		&rec.OwnerNode,
		&rec.Epoch,
		&rec.Status,
		&rec.MaxPlayers,
		&rec.PlayerCount,
		&rec.ProgressCurrent,
		&rec.ProgressTotal,
		&rec.LastAppliedSeq,
		&rec.EngineVersion,
		&rec.CurrentMatchID,
		&rec.NextRoundNo,
		&rec.CreatedAt,
		&rec.UpdatedAt,
		&lease,
	)
	if err != nil {
		return contracts.RoomRecord{}, err
	}
	if lease.Valid {
		rec.LeaseExpiresAt = lease.Time
	}
	return rec, nil
}

func nullableTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}
