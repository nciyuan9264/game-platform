package migration

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Options struct {
	DryRun     bool
	VerifyOnly bool
	GameType   string
	AfterID    int64
	BatchSize  int
}

type PayloadCounts struct {
	GameJSON       int `json:"gameJSON"`
	EventJSON      int `json:"eventJSON"`
	StateBlobs     int `json:"stateBlobs"`
	ReplayPayloads int `json:"replayPayloads"`
}

type Report struct {
	ScannedGames      int           `json:"scannedGames"`
	MigratableGames   int           `json:"migratableGames"`
	MigratedGames     int           `json:"migratedGames"`
	SkippedGames      int           `json:"skippedGames"`
	FailedGameIDs     []int64       `json:"failedGameIDs"`
	UnknownReferences int           `json:"unknownReferences"`
	Conflicts         int           `json:"conflicts"`
	ActiveLegacyRooms int           `json:"activeLegacyRooms"`
	RemainingLegacy   int           `json:"remainingCompositeIDs"`
	ReplayV1Games     int           `json:"replayV1Games"`
	MutableRows       int           `json:"mutableRows"`
	Payloads          PayloadCounts `json:"payloads"`
	DryRun            bool          `json:"dryRun"`
	VerifyOnly        bool          `json:"verifyOnly"`
	LastGameID        int64         `json:"lastGameID"`
}

type Runner struct {
	db *sql.DB
}

func NewRunner(db *sql.DB) *Runner {
	return &Runner{db: db}
}

type identity struct {
	SourcePlayerID string
	PlayerID       string
	UserID         *int64
	DisplayName    string
	IsAI           bool
}

type gameCandidate struct {
	ID                  int64
	RoomID              string
	GameType            string
	MatchID             string
	ReplaySchemaVersion int
	Aliases             map[string]string
	Players             map[string]identity
}

type eventPayload struct {
	ID            int64
	PlayerID      string
	Payload       []byte
	StateSnapshot []byte
	StateBlob     []byte
	ReplayPayload []byte
}

type transformedGame struct {
	InitialState         []byte
	FinalResult          []byte
	InitialReplayPayload []byte
	Events               []eventPayload
	Counts               PayloadCounts
}

var (
	ErrIdentityConflict  = errors.New("player identity conflict")
	ErrUnknownLegacyID   = errors.New("unknown legacy player identity")
	ErrActiveLegacyRooms = errors.New("active legacy rooms prevent migration")
)

func (r *Runner) Run(ctx context.Context, options Options) (Report, error) {
	if r == nil || r.db == nil {
		return Report{}, errors.New("database is required")
	}
	if options.BatchSize <= 0 || options.BatchSize > 1000 {
		options.BatchSize = 100
	}
	report := Report{DryRun: options.DryRun, VerifyOnly: options.VerifyOnly, FailedGameIDs: []int64{}}
	mutableRows, err := r.countMutableLegacyRows(ctx, options.GameType)
	if err != nil {
		return report, err
	}
	report.MutableRows = mutableRows
	active, err := r.countActiveLegacyRooms(ctx)
	if err != nil {
		return report, err
	}
	report.ActiveLegacyRooms = active
	if options.VerifyOnly {
		if err := r.populateVerification(ctx, options.GameType, &report); err != nil {
			return report, err
		}
		return report, nil
	}
	if active > 0 {
		return report, ErrActiveLegacyRooms
	}

	afterID := options.AfterID
	for {
		gameIDs, err := r.listGameIDs(ctx, options.GameType, afterID, options.BatchSize)
		if err != nil {
			return report, err
		}
		if len(gameIDs) == 0 {
			break
		}
		for _, gameID := range gameIDs {
			report.ScannedGames++
			report.LastGameID = gameID
			candidate, err := r.loadCandidate(ctx, gameID)
			if err != nil {
				report.FailedGameIDs = append(report.FailedGameIDs, gameID)
				report.Conflicts++
				continue
			}
			if len(candidate.Aliases) == 0 && candidate.ReplaySchemaVersion != 1 {
				report.SkippedGames++
				continue
			}
			report.MigratableGames++
			transformed, err := r.transformGame(ctx, candidate)
			if err != nil {
				report.FailedGameIDs = append(report.FailedGameIDs, gameID)
				if errors.Is(err, ErrUnknownLegacyID) {
					report.UnknownReferences++
				} else {
					report.Conflicts++
				}
				continue
			}
			addPayloadCounts(&report.Payloads, transformed.Counts)
			if options.DryRun {
				continue
			}
			if err := r.applyGame(ctx, candidate, transformed); err != nil {
				report.FailedGameIDs = append(report.FailedGameIDs, gameID)
				report.Conflicts++
				continue
			}
			report.MigratedGames++
		}
		afterID = gameIDs[len(gameIDs)-1]
	}
	if !options.DryRun {
		if err := r.normalizeMutableIdentityTables(ctx, options.GameType); err != nil {
			return report, err
		}
	}
	if err := r.populateVerification(ctx, options.GameType, &report); err != nil {
		return report, err
	}
	sort.Slice(report.FailedGameIDs, func(i, j int) bool {
		return report.FailedGameIDs[i] < report.FailedGameIDs[j]
	})
	if len(report.FailedGameIDs) > 0 {
		return report, fmt.Errorf("%w: %d games failed", ErrIdentityConflict, len(report.FailedGameIDs))
	}
	return report, nil
}

func (r *Runner) countMutableLegacyRows(ctx context.Context, gameType string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*)
				FROM game_players gp
				JOIN games g ON g.id = gp.game_id
				WHERE gp.is_ai
					AND gp.user_id IS NOT NULL
					AND ($1 = '' OR g.game_type = $1))
			+
			(SELECT COUNT(*)
				FROM match_players mp
				JOIN matches m ON m.match_id = mp.match_id
				WHERE (
					(NOT mp.is_ai AND mp.player_id <> mp.user_id)
					OR (mp.is_ai AND mp.user_id <> '')
				)
				AND ($1 = '' OR m.game_type = $1))
			+
			(SELECT COUNT(*)
				FROM matches m
				WHERE (
					(m.owner_user_id <> '' AND m.owner_player_id <> m.owner_user_id)
					OR (
						m.winner_user_id <> ''
						AND m.winner_player_id <> m.winner_user_id
					)
				)
				AND ($1 = '' OR m.game_type = $1))
			+
			(SELECT COUNT(*)
				FROM rooms r
				WHERE r.owner_user_id <> ''
					AND r.owner_player_id <> r.owner_user_id
					AND ($1 = '' OR r.game_type = $1))`,
		gameType,
	).Scan(&count)
	return count, err
}

func (r *Runner) normalizeMutableIdentityTables(ctx context.Context, gameType string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var conflicts int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM (
			SELECT mp.match_id, mp.user_id
			FROM match_players mp
			JOIN matches m ON m.match_id = mp.match_id
			WHERE NOT mp.is_ai
				AND ($1 = '' OR m.game_type = $1)
			GROUP BY mp.match_id, mp.user_id
			HAVING COUNT(*) > 1
		) collisions`, gameType).Scan(&conflicts); err != nil {
		return err
	}
	if conflicts > 0 {
		return fmt.Errorf("%w: %d match player key collisions", ErrIdentityConflict, conflicts)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE game_players gp
		SET user_id = NULL
		FROM games g
		WHERE g.id = gp.game_id
			AND gp.is_ai
			AND gp.user_id IS NOT NULL
			AND ($1 = '' OR g.game_type = $1)`, gameType); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE match_players mp
		SET display_name = CASE
				WHEN mp.display_name = mp.player_id
					AND mp.player_id LIKE '%-' || mp.user_id
					THEN LEFT(mp.player_id, LENGTH(mp.player_id) - LENGTH(mp.user_id) - 1)
				WHEN mp.display_name <> '' THEN mp.display_name
				WHEN mp.player_id LIKE '%-' || mp.user_id
					THEN LEFT(mp.player_id, LENGTH(mp.player_id) - LENGTH(mp.user_id) - 1)
				ELSE mp.display_name
			END,
			player_id = mp.user_id
		FROM matches m
		WHERE m.match_id = mp.match_id
			AND NOT mp.is_ai
			AND mp.player_id <> mp.user_id
			AND ($1 = '' OR m.game_type = $1)`, gameType); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE match_players mp
		SET user_id = ''
		FROM matches m
		WHERE m.match_id = mp.match_id
			AND mp.is_ai
			AND mp.user_id <> ''
			AND ($1 = '' OR m.game_type = $1)`, gameType); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE matches
		SET owner_player_id = owner_user_id,
			winner_player_id = CASE
				WHEN winner_user_id <> '' THEN winner_user_id
				ELSE winner_player_id
			END
		WHERE (
			(owner_user_id <> '' AND owner_player_id <> owner_user_id)
			OR (winner_user_id <> '' AND winner_player_id <> winner_user_id)
		)
			AND ($1 = '' OR game_type = $1)`, gameType); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE rooms
		SET owner_player_id = owner_user_id
		WHERE owner_user_id <> ''
			AND owner_player_id <> owner_user_id
			AND ($1 = '' OR game_type = $1)`, gameType); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Runner) listGameIDs(ctx context.Context, gameType string, afterID int64, limit int) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id
		FROM games
		WHERE id > $1
			AND ($2 = '' OR game_type = $2)
		ORDER BY id
		LIMIT $3`, afterID, gameType, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *Runner) loadCandidate(ctx context.Context, gameID int64) (gameCandidate, error) {
	candidate := gameCandidate{
		ID:      gameID,
		Aliases: map[string]string{},
		Players: map[string]identity{},
	}
	if err := r.db.QueryRowContext(ctx, `
		SELECT room_id, game_type, replay_schema_version
		FROM games
		WHERE id = $1`, gameID).Scan(
		&candidate.RoomID,
		&candidate.GameType,
		&candidate.ReplaySchemaVersion,
	); err != nil {
		return candidate, err
	}
	_ = r.db.QueryRowContext(ctx, `
		SELECT match_id
		FROM matches
		WHERE runtime_room_id = $1
		ORDER BY ABS(EXTRACT(EPOCH FROM (
			started_at - (SELECT started_at FROM games WHERE id = $2)
		))) ASC, round_no ASC
		LIMIT 1`, candidate.RoomID, gameID).Scan(&candidate.MatchID)

	rows, err := r.db.QueryContext(ctx, `
		SELECT user_id, player_id, display_name, is_ai
		FROM game_players
		WHERE game_id = $1
		ORDER BY id`, gameID)
	if err != nil {
		return candidate, err
	}
	defer rows.Close()
	canonicalSources := map[string]string{}
	for rows.Next() {
		var (
			userID      sql.NullInt64
			sourceID    string
			displayName string
			isAI        bool
		)
		if err := rows.Scan(&userID, &sourceID, &displayName, &isAI); err != nil {
			return candidate, err
		}
		playerID := sourceID
		var canonicalUserID *int64
		if !isAI {
			if !userID.Valid || userID.Int64 <= 0 {
				return candidate, fmt.Errorf("%w: game %d player %q has no user ID", ErrIdentityConflict, gameID, sourceID)
			}
			canonical := userID.Int64
			canonicalUserID = &canonical
			playerID = strconv.FormatInt(userID.Int64, 10)
			if previous, exists := canonicalSources[playerID]; exists && previous != sourceID {
				return candidate, fmt.Errorf(
					"%w: game %d players %q and %q both map to %q",
					ErrIdentityConflict,
					gameID,
					previous,
					sourceID,
					playerID,
				)
			}
			canonicalSources[playerID] = sourceID
			if sourceID != playerID {
				if !strings.HasSuffix(sourceID, "-"+playerID) {
					return candidate, fmt.Errorf(
						"%w: game %d player %q does not end with user ID %q",
						ErrUnknownLegacyID,
						gameID,
						sourceID,
						playerID,
					)
				}
				candidate.Aliases[sourceID] = playerID
				displayName = canonicalDisplayName(sourceID, playerID, displayName)
			}
		}
		candidate.Players[playerID] = identity{
			SourcePlayerID: sourceID,
			PlayerID:       playerID,
			UserID:         canonicalUserID,
			DisplayName:    displayName,
			IsAI:           isAI,
		}
	}
	return candidate, rows.Err()
}

func (r *Runner) transformGame(ctx context.Context, candidate gameCandidate) (transformedGame, error) {
	var (
		initialState         []byte
		finalResult          []byte
		initialReplayPayload []byte
	)
	if err := r.db.QueryRowContext(ctx, `
		SELECT initial_state, final_result, initial_replay_payload
		FROM games
		WHERE id = $1`, candidate.ID).Scan(
		&initialState,
		&finalResult,
		&initialReplayPayload,
	); err != nil {
		return transformedGame{}, err
	}
	result := transformedGame{}
	var err error
	if len(initialState) > 0 {
		result.InitialState, err = transformJSON(initialState, candidate.Aliases, candidate.Players)
		if err != nil {
			return result, fmt.Errorf("initial state: %w", err)
		}
		result.Counts.GameJSON++
	}
	if len(finalResult) > 0 {
		result.FinalResult, err = transformJSON(finalResult, candidate.Aliases, candidate.Players)
		if err != nil {
			return result, fmt.Errorf("final result: %w", err)
		}
		result.Counts.GameJSON++
	}
	if len(initialReplayPayload) == 0 &&
		(candidate.GameType == "acquire" || candidate.GameType == "splendor") {
		return result, fmt.Errorf("%w: game %d has no initial replay payload", ErrIdentityConflict, candidate.ID)
	}
	if len(initialReplayPayload) > 0 {
		result.InitialReplayPayload, err = transformCompressedJSON(
			initialReplayPayload,
			candidate.Aliases,
			candidate.Players,
			true,
		)
		if err != nil {
			return result, fmt.Errorf("initial replay payload: %w", err)
		}
		result.Counts.ReplayPayloads++
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT id, player_id, payload, state_snapshot, state_blob, replay_payload
		FROM game_events
		WHERE game_id = $1
		ORDER BY seq`, candidate.ID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var event eventPayload
		if err := rows.Scan(
			&event.ID,
			&event.PlayerID,
			&event.Payload,
			&event.StateSnapshot,
			&event.StateBlob,
			&event.ReplayPayload,
		); err != nil {
			return result, err
		}
		if canonical, ok := candidate.Aliases[event.PlayerID]; ok {
			event.PlayerID = canonical
		}
		event.Payload, err = transformJSON(event.Payload, candidate.Aliases, candidate.Players)
		if err != nil {
			return result, fmt.Errorf("event %d payload: %w", event.ID, err)
		}
		result.Counts.EventJSON++
		if len(event.StateSnapshot) > 0 {
			event.StateSnapshot, err = transformJSON(event.StateSnapshot, candidate.Aliases, candidate.Players)
			if err != nil {
				return result, fmt.Errorf("event %d state snapshot: %w", event.ID, err)
			}
			result.Counts.EventJSON++
		}
		if len(event.StateBlob) > 0 {
			event.StateBlob, err = transformCompressedJSON(
				event.StateBlob,
				candidate.Aliases,
				candidate.Players,
				true,
			)
			if err != nil {
				return result, fmt.Errorf("event %d state blob: %w", event.ID, err)
			}
			result.Counts.StateBlobs++
		}
		if len(event.ReplayPayload) == 0 &&
			(candidate.GameType == "acquire" || candidate.GameType == "splendor") {
			return result, fmt.Errorf("%w: event %d has no replay payload", ErrIdentityConflict, event.ID)
		}
		if len(event.ReplayPayload) > 0 {
			event.ReplayPayload, err = transformCompressedJSON(
				event.ReplayPayload,
				candidate.Aliases,
				candidate.Players,
				true,
			)
			if err != nil {
				return result, fmt.Errorf("event %d replay payload: %w", event.ID, err)
			}
			result.Counts.ReplayPayloads++
		}
		result.Events = append(result.Events, event)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func (r *Runner) applyGame(ctx context.Context, candidate gameCandidate, transformed transformedGame) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	for sourceID, playerID := range candidate.Aliases {
		player := candidate.Players[playerID]
		var existingPlayerID string
		var existingUserID sql.NullInt64
		var existingAI bool
		err := tx.QueryRowContext(ctx, `
			SELECT player_id, user_id, is_ai
			FROM player_identity_aliases
			WHERE game_id = $1 AND source_player_id = $2`,
			candidate.ID,
			sourceID,
		).Scan(&existingPlayerID, &existingUserID, &existingAI)
		if err == nil {
			if existingPlayerID != playerID ||
				existingAI != player.IsAI ||
				!sameNullableInt64(existingUserID, player.UserID) {
				return fmt.Errorf("%w: alias %q changed", ErrIdentityConflict, sourceID)
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		} else {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO player_identity_aliases (
					game_id, match_id, source_player_id, player_id, user_id, is_ai, created_at
				) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
				candidate.ID,
				candidate.MatchID,
				sourceID,
				playerID,
				nullableInt64(player.UserID),
				player.IsAI,
				now,
			); err != nil {
				return err
			}
		}
	}
	for _, player := range candidate.Players {
		if _, err := tx.ExecContext(ctx, `
			UPDATE game_players
			SET player_id = $3,
				user_id = $4,
				display_name = $5
			WHERE game_id = $1 AND player_id = $2`,
			candidate.ID,
			player.SourcePlayerID,
			player.PlayerID,
			nullableInt64(player.UserID),
			player.DisplayName,
		); err != nil {
			return err
		}
	}
	for sourceID, playerID := range candidate.Aliases {
		if _, err := tx.ExecContext(ctx, `
			UPDATE game_events SET player_id = $3
			WHERE game_id = $1 AND player_id = $2`,
			candidate.ID,
			sourceID,
			playerID,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE games SET winner_player_id = $3
			WHERE id = $1 AND winner_player_id = $2`,
			candidate.ID,
			sourceID,
			playerID,
		); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE games
		SET initial_state = $2,
			final_result = $3,
			initial_replay_payload = $4,
			replay_schema_version = CASE
				WHEN initial_replay_payload IS NOT NULL THEN 2
				ELSE replay_schema_version
			END
		WHERE id = $1`,
		candidate.ID,
		nullableJSON(transformed.InitialState),
		nullableJSON(transformed.FinalResult),
		nullableBytes(transformed.InitialReplayPayload),
	); err != nil {
		return err
	}
	for _, event := range transformed.Events {
		if _, err := tx.ExecContext(ctx, `
			UPDATE game_events
			SET player_id = $2,
				payload = $3,
				state_snapshot = $4,
				state_blob = $5,
				replay_payload = $6
			WHERE id = $1`,
			event.ID,
			event.PlayerID,
			nullableJSON(event.Payload),
			nullableJSON(event.StateSnapshot),
			nullableBytes(event.StateBlob),
			nullableBytes(event.ReplayPayload),
		); err != nil {
			return err
		}
	}
	if candidate.MatchID != "" {
		for sourceID, playerID := range candidate.Aliases {
			player := candidate.Players[playerID]
			var conflicting int
			if err := tx.QueryRowContext(ctx, `
				SELECT COUNT(*)
				FROM match_players
				WHERE match_id = $1
					AND player_id = $2
					AND player_id <> $3`,
				candidate.MatchID,
				playerID,
				sourceID,
			).Scan(&conflicting); err != nil {
				return err
			}
			if conflicting > 0 {
				return fmt.Errorf("%w: match player %q already exists", ErrIdentityConflict, playerID)
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE match_players
				SET player_id = $3,
					user_id = $3,
					display_name = COALESCE(NULLIF($4, ''), display_name)
				WHERE match_id = $1 AND player_id = $2`,
				candidate.MatchID,
				sourceID,
				playerID,
				player.DisplayName,
			); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE matches
				SET owner_player_id = CASE WHEN owner_player_id = $2 THEN $3 ELSE owner_player_id END,
					winner_player_id = CASE WHEN winner_player_id = $2 THEN $3 ELSE winner_player_id END
				WHERE match_id = $1`,
				candidate.MatchID,
				sourceID,
				playerID,
			); err != nil {
				return err
			}
		}
	}
	for sourceID, playerID := range candidate.Aliases {
		if _, err := tx.ExecContext(ctx, `
			UPDATE rooms
			SET owner_player_id = $3
			WHERE room_id = $1 AND owner_player_id = $2`,
			candidate.RoomID,
			sourceID,
			playerID,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Runner) populateVerification(ctx context.Context, gameType string, report *Report) error {
	err := r.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*)
				FROM game_players gp
				JOIN games g ON g.id = gp.game_id
				WHERE (
					(
						NOT gp.is_ai
						AND (gp.user_id IS NULL OR gp.player_id <> gp.user_id::text)
					)
					OR (gp.is_ai AND gp.user_id IS NOT NULL)
				)
					AND ($1 = '' OR g.game_type = $1))
			+
			(SELECT COUNT(*)
				FROM match_players mp
				JOIN matches m ON m.match_id = mp.match_id
				WHERE (
					(NOT mp.is_ai AND mp.player_id <> mp.user_id)
					OR (mp.is_ai AND mp.user_id <> '')
				)
					AND ($1 = '' OR m.game_type = $1))
			+
			(SELECT COUNT(*)
				FROM games g
				WHERE g.winner_user_id IS NOT NULL
					AND g.winner_player_id <> g.winner_user_id::text
					AND ($1 = '' OR g.game_type = $1))
			+
			(SELECT COUNT(*)
				FROM game_events ge
				JOIN games g ON g.id = ge.game_id
				JOIN player_identity_aliases alias
					ON alias.game_id = ge.game_id
					AND alias.source_player_id = ge.player_id
				WHERE ($1 = '' OR g.game_type = $1))
			+
			(SELECT COUNT(*)
				FROM games g
				JOIN player_identity_aliases alias
					ON alias.game_id = g.id
					AND alias.source_player_id = g.winner_player_id
				WHERE ($1 = '' OR g.game_type = $1))
			+
			(SELECT COUNT(*)
				FROM matches m
				WHERE (
					(m.owner_user_id <> '' AND m.owner_player_id <> m.owner_user_id)
					OR (
						m.winner_user_id <> ''
						AND m.winner_player_id <> m.winner_user_id
					)
				)
					AND ($1 = '' OR m.game_type = $1))
			+
			(SELECT COUNT(*)
				FROM rooms r
				WHERE r.owner_user_id <> ''
					AND r.owner_player_id <> r.owner_user_id
					AND ($1 = '' OR r.game_type = $1))`,
		gameType,
	).Scan(&report.RemainingLegacy)
	if err != nil {
		return err
	}
	payloadAliases, err := r.countPayloadAliases(ctx, gameType)
	if err != nil {
		return err
	}
	report.RemainingLegacy += payloadAliases
	return r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM games
		WHERE replay_schema_version = 1
			AND initial_replay_payload IS NOT NULL
			AND ($1 = '' OR game_type = $1)`,
		gameType,
	).Scan(&report.ReplayV1Games)
}

func (r *Runner) countPayloadAliases(ctx context.Context, gameType string) (int, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT alias.game_id
		FROM player_identity_aliases alias
		JOIN games g ON g.id = alias.game_id
		WHERE $1 = '' OR g.game_type = $1
		ORDER BY alias.game_id`, gameType)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var gameIDs []int64
	for rows.Next() {
		var gameID int64
		if err := rows.Scan(&gameID); err != nil {
			return 0, err
		}
		gameIDs = append(gameIDs, gameID)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	remaining := 0
	for _, gameID := range gameIDs {
		aliases, err := r.aliasesForGame(ctx, gameID)
		if err != nil {
			return 0, err
		}
		var initialState, finalResult, initialReplay []byte
		if err := r.db.QueryRowContext(ctx, `
			SELECT initial_state, final_result, initial_replay_payload
			FROM games
			WHERE id = $1`, gameID).Scan(&initialState, &finalResult, &initialReplay); err != nil {
			return 0, err
		}
		for _, payload := range []struct {
			value      []byte
			compressed bool
		}{
			{value: initialState},
			{value: finalResult},
			{value: initialReplay, compressed: true},
		} {
			found, err := payloadContainsAlias(payload.value, aliases, payload.compressed)
			if err != nil {
				return 0, fmt.Errorf("verify game %d payload: %w", gameID, err)
			}
			if found {
				remaining++
			}
		}
		eventRows, err := r.db.QueryContext(ctx, `
			SELECT payload, state_snapshot, state_blob, replay_payload
			FROM game_events
			WHERE game_id = $1`, gameID)
		if err != nil {
			return 0, err
		}
		for eventRows.Next() {
			var payload, snapshot, blob, replay []byte
			if err := eventRows.Scan(&payload, &snapshot, &blob, &replay); err != nil {
				eventRows.Close()
				return 0, err
			}
			for _, item := range []struct {
				value      []byte
				compressed bool
			}{
				{value: payload},
				{value: snapshot},
				{value: blob, compressed: true},
				{value: replay, compressed: true},
			} {
				found, err := payloadContainsAlias(item.value, aliases, item.compressed)
				if err != nil {
					eventRows.Close()
					return 0, fmt.Errorf("verify game %d event payload: %w", gameID, err)
				}
				if found {
					remaining++
				}
			}
		}
		if err := eventRows.Err(); err != nil {
			eventRows.Close()
			return 0, err
		}
		if err := eventRows.Close(); err != nil {
			return 0, err
		}
	}
	return remaining, nil
}

func (r *Runner) aliasesForGame(ctx context.Context, gameID int64) (map[string]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT source_player_id, player_id
		FROM player_identity_aliases
		WHERE game_id = $1`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	aliases := map[string]string{}
	for rows.Next() {
		var sourceID, playerID string
		if err := rows.Scan(&sourceID, &playerID); err != nil {
			return nil, err
		}
		aliases[sourceID] = playerID
	}
	return aliases, rows.Err()
}

func payloadContainsAlias(raw []byte, aliases map[string]string, compressed bool) (bool, error) {
	if len(raw) == 0 {
		return false, nil
	}
	decoded := raw
	if compressed {
		var err error
		decoded, _, err = decodeMaybeGzip(raw)
		if err != nil {
			return false, err
		}
	}
	var value any
	if err := json.Unmarshal(decoded, &value); err != nil {
		return false, err
	}
	return containsAlias(value, aliases), nil
}

func (r *Runner) countActiveLegacyRooms(ctx context.Context) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT room_id)
		FROM (
			SELECT r.room_id
			FROM rooms r
			WHERE r.status <> 'closed'
				AND r.owner_user_id <> ''
				AND r.owner_player_id <> r.owner_user_id
			UNION ALL
			SELECT m.runtime_room_id
			FROM matches m
			JOIN match_players mp ON mp.match_id = m.match_id
			WHERE m.status NOT IN ('ended', 'aborted')
				AND NOT mp.is_ai
				AND mp.player_id <> mp.user_id
		) active_legacy`).Scan(&count)
	return count, err
}

func transformJSON(raw []byte, aliases map[string]string, players map[string]identity) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	transformed, err := transformValue(value, aliases)
	if err != nil {
		return nil, err
	}
	rebuildPlayerProfiles(transformed, players)
	if containsAlias(transformed, aliases) {
		return nil, ErrUnknownLegacyID
	}
	return json.Marshal(transformed)
}

func transformCompressedJSON(
	raw []byte,
	aliases map[string]string,
	players map[string]identity,
	forceGzip bool,
) ([]byte, error) {
	decoded, compressed, err := decodeMaybeGzip(raw)
	if err != nil {
		return nil, err
	}
	transformed, err := transformJSON(decoded, aliases, players)
	if err != nil {
		return nil, err
	}
	if !compressed && !forceGzip {
		return transformed, nil
	}
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	if _, err := writer.Write(transformed); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func decodeMaybeGzip(raw []byte) ([]byte, bool, error) {
	if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		return raw, false, nil
	}
	reader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, true, err
	}
	defer reader.Close()
	decoded, err := io.ReadAll(reader)
	return decoded, true, err
}

func transformValue(value any, aliases map[string]string) (any, error) {
	switch typed := value.(type) {
	case string:
		if canonical, ok := aliases[typed]; ok {
			return canonical, nil
		}
		return typed, nil
	case []any:
		for index := range typed {
			transformed, err := transformValue(typed[index], aliases)
			if err != nil {
				return nil, err
			}
			typed[index] = transformed
		}
		return typed, nil
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			targetKey := key
			if canonical, ok := aliases[key]; ok {
				targetKey = canonical
			}
			if _, exists := result[targetKey]; exists {
				return nil, fmt.Errorf("%w: JSON key %q collides at %q", ErrIdentityConflict, key, targetKey)
			}
			if isDisplayField(key) {
				result[targetKey] = item
				continue
			}
			transformed, err := transformValue(item, aliases)
			if err != nil {
				return nil, err
			}
			result[targetKey] = transformed
		}
		return result, nil
	default:
		return value, nil
	}
}

func rebuildPlayerProfiles(value any, players map[string]identity) {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			rebuildPlayerProfiles(item, players)
		}
	case map[string]any:
		if playerID, ok := typed["playerID"].(string); ok {
			if player, exists := players[playerID]; exists {
				typed["playerID"] = player.PlayerID
				typed["displayName"] = player.DisplayName
				if player.UserID != nil {
					typed["userID"] = strconv.FormatInt(*player.UserID, 10)
				} else {
					delete(typed, "userID")
				}
			}
		}
		for _, item := range typed {
			rebuildPlayerProfiles(item, players)
		}
	}
}

func containsAlias(value any, aliases map[string]string) bool {
	switch typed := value.(type) {
	case string:
		_, found := aliases[typed]
		return found
	case []any:
		for _, item := range typed {
			if containsAlias(item, aliases) {
				return true
			}
		}
	case map[string]any:
		for key, item := range typed {
			if _, found := aliases[key]; found {
				return true
			}
			if !isDisplayField(key) && containsAlias(item, aliases) {
				return true
			}
		}
	}
	return false
}

func isDisplayField(key string) bool {
	switch key {
	case "displayName", "name", "email", "avatar":
		return true
	default:
		return false
	}
}

func canonicalDisplayName(sourcePlayerID, playerID, displayName string) string {
	displayName = strings.TrimSpace(displayName)
	if displayName != "" && displayName != sourcePlayerID {
		return displayName
	}
	return strings.TrimSuffix(sourcePlayerID, "-"+playerID)
}

func sameNullableInt64(value sql.NullInt64, expected *int64) bool {
	if expected == nil {
		return !value.Valid
	}
	return value.Valid && value.Int64 == *expected
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return string(value)
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func addPayloadCounts(target *PayloadCounts, value PayloadCounts) {
	target.GameJSON += value.GameJSON
	target.EventJSON += value.EventJSON
	target.StateBlobs += value.StateBlobs
	target.ReplayPayloads += value.ReplayPayloads
}
