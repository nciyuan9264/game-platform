package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
)

var (
	ErrGameCatalogNotFound = errors.New("game catalog entry not found")
	ErrGameCatalogConflict = errors.New("game catalog compare-and-swap conflict")
	ErrInvalidGameCatalog  = errors.New("invalid game catalog entry")
)

const gameCatalogColumns = `
	game_type, active_engine_version, default_max_players,
	replay_schema_version, enabled_for_new_rooms, is_default,
	revision, updated_at`

func (s *PostgresStore) GetGameCatalogEntry(ctx context.Context, gameType string) (contracts.GameCatalogEntry, error) {
	entry, err := scanGameCatalogEntry(s.db.QueryRowContext(
		ctx,
		`SELECT `+gameCatalogColumns+` FROM game_catalog WHERE game_type = $1`,
		strings.TrimSpace(gameType),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return contracts.GameCatalogEntry{}, ErrGameCatalogNotFound
	}
	return entry, err
}

func (s *PostgresStore) GetDefaultGameCatalogEntry(ctx context.Context) (contracts.GameCatalogEntry, error) {
	entry, err := scanGameCatalogEntry(s.db.QueryRowContext(
		ctx,
		`SELECT `+gameCatalogColumns+`
		FROM game_catalog
		WHERE is_default AND enabled_for_new_rooms`,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return contracts.GameCatalogEntry{}, ErrGameCatalogNotFound
	}
	return entry, err
}

func (s *PostgresStore) ListGameCatalog(ctx context.Context) ([]contracts.GameCatalogEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+gameCatalogColumns+` FROM game_catalog ORDER BY game_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]contracts.GameCatalogEntry, 0)
	for rows.Next() {
		entry, err := scanGameCatalogEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *PostgresStore) RegisterGameCatalogEntry(
	ctx context.Context,
	entry contracts.GameCatalogEntry,
	artifact,
	actor string,
) (contracts.GameCatalogEntry, bool, error) {
	entry = normalizeGameCatalogEntry(entry)
	if err := validateGameCatalogEntry(entry); err != nil {
		return contracts.GameCatalogEntry{}, false, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.GameCatalogEntry{}, false, err
	}
	defer tx.Rollback()

	created := true
	inserted, err := scanGameCatalogEntry(tx.QueryRowContext(
		ctx,
		`INSERT INTO game_catalog (
			game_type, active_engine_version, default_max_players,
			replay_schema_version, enabled_for_new_rooms, is_default,
			revision, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (game_type) DO NOTHING
		RETURNING `+gameCatalogColumns,
		entry.GameType,
		entry.ActiveEngineVersion,
		entry.DefaultMaxPlayers,
		entry.ReplaySchemaVersion,
		entry.EnabledForNewRooms,
		entry.IsDefault,
		entry.Revision,
		entry.UpdatedAt,
	))
	if errors.Is(err, sql.ErrNoRows) {
		created = false
		inserted, err = scanGameCatalogEntry(tx.QueryRowContext(
			ctx,
			`SELECT `+gameCatalogColumns+` FROM game_catalog WHERE game_type = $1 FOR UPDATE`,
			entry.GameType,
		))
		if err == nil && !sameGameCatalogRegistration(inserted, entry) {
			return contracts.GameCatalogEntry{}, false, fmt.Errorf(
				"%w: %s is already registered as %s",
				ErrGameCatalogConflict,
				entry.GameType,
				inserted.ActiveEngineVersion,
			)
		}
	}
	if err != nil {
		return contracts.GameCatalogEntry{}, false, err
	}
	if created {
		if err := insertGameCatalogEvent(
			ctx,
			tx,
			contracts.GameCatalogActionRegister,
			nil,
			&inserted,
			artifact,
			actor,
		); err != nil {
			return contracts.GameCatalogEntry{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return contracts.GameCatalogEntry{}, false, err
	}
	return inserted, created, nil
}

func (s *PostgresStore) SetActiveGameVersion(
	ctx context.Context,
	gameType,
	targetVersion,
	expectedVersion,
	action,
	artifact,
	actor string,
) (contracts.GameCatalogEntry, error) {
	gameType = strings.TrimSpace(gameType)
	targetVersion = strings.TrimSpace(targetVersion)
	expectedVersion = strings.TrimSpace(expectedVersion)
	if gameType == "" || targetVersion == "" || expectedVersion == "" {
		return contracts.GameCatalogEntry{}, fmt.Errorf("%w: game and versions are required", ErrInvalidGameCatalog)
	}
	if action != contracts.GameCatalogActionPromote && action != contracts.GameCatalogActionRollback {
		return contracts.GameCatalogEntry{}, fmt.Errorf("%w: unsupported catalog action %q", ErrInvalidGameCatalog, action)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.GameCatalogEntry{}, err
	}
	defer tx.Rollback()

	previous, err := scanGameCatalogEntry(tx.QueryRowContext(
		ctx,
		`SELECT `+gameCatalogColumns+` FROM game_catalog WHERE game_type = $1 FOR UPDATE`,
		gameType,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return contracts.GameCatalogEntry{}, ErrGameCatalogNotFound
	}
	if err != nil {
		return contracts.GameCatalogEntry{}, err
	}
	if previous.ActiveEngineVersion != expectedVersion {
		return contracts.GameCatalogEntry{}, fmt.Errorf(
			"%w: %s is %s, expected %s",
			ErrGameCatalogConflict,
			gameType,
			previous.ActiveEngineVersion,
			expectedVersion,
		)
	}

	current := previous
	current.ActiveEngineVersion = targetVersion
	current.Revision++
	current.UpdatedAt = time.Now().UTC()
	result, err := tx.ExecContext(
		ctx,
		`UPDATE game_catalog
		SET active_engine_version = $1, revision = $2, updated_at = $3
		WHERE game_type = $4 AND active_engine_version = $5 AND revision = $6`,
		current.ActiveEngineVersion,
		current.Revision,
		current.UpdatedAt,
		current.GameType,
		previous.ActiveEngineVersion,
		previous.Revision,
	)
	if err != nil {
		return contracts.GameCatalogEntry{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return contracts.GameCatalogEntry{}, err
	}
	if affected != 1 {
		return contracts.GameCatalogEntry{}, ErrGameCatalogConflict
	}
	if err := insertGameCatalogEvent(ctx, tx, action, &previous, &current, artifact, actor); err != nil {
		return contracts.GameCatalogEntry{}, err
	}
	if err := tx.Commit(); err != nil {
		return contracts.GameCatalogEntry{}, err
	}
	return current, nil
}

func (s *PostgresStore) DisableGameCatalogEntry(
	ctx context.Context,
	gameType,
	artifact,
	actor string,
) (contracts.GameCatalogEntry, bool, error) {
	gameType = strings.TrimSpace(gameType)
	if gameType == "" {
		return contracts.GameCatalogEntry{}, false, fmt.Errorf("%w: game is required", ErrInvalidGameCatalog)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.GameCatalogEntry{}, false, err
	}
	defer tx.Rollback()

	previous, err := scanGameCatalogEntry(tx.QueryRowContext(
		ctx,
		`SELECT `+gameCatalogColumns+` FROM game_catalog WHERE game_type = $1 FOR UPDATE`,
		gameType,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return contracts.GameCatalogEntry{}, false, ErrGameCatalogNotFound
	}
	if err != nil {
		return contracts.GameCatalogEntry{}, false, err
	}
	if !previous.EnabledForNewRooms {
		if err := tx.Commit(); err != nil {
			return contracts.GameCatalogEntry{}, false, err
		}
		return previous, false, nil
	}

	current := previous
	current.EnabledForNewRooms = false
	current.IsDefault = false
	current.Revision++
	current.UpdatedAt = time.Now().UTC()
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE game_catalog
		SET enabled_for_new_rooms = false, is_default = false, revision = $1, updated_at = $2
		WHERE game_type = $3 AND revision = $4`,
		current.Revision,
		current.UpdatedAt,
		current.GameType,
		previous.Revision,
	); err != nil {
		return contracts.GameCatalogEntry{}, false, err
	}
	if err := insertGameCatalogEvent(
		ctx,
		tx,
		contracts.GameCatalogActionDisable,
		&previous,
		&current,
		artifact,
		actor,
	); err != nil {
		return contracts.GameCatalogEntry{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return contracts.GameCatalogEntry{}, false, err
	}
	return current, true, nil
}

func (s *PostgresStore) ListGameCatalogEvents(
	ctx context.Context,
	gameType string,
	limit int,
) ([]contracts.GameCatalogEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	query := `SELECT event_id, game_type, action, previous_entry, current_entry, artifact, actor, created_at
		FROM game_catalog_events`
	args := []any{limit}
	if strings.TrimSpace(gameType) != "" {
		query += ` WHERE game_type = $2`
		args = append(args, strings.TrimSpace(gameType))
	}
	query += ` ORDER BY event_id DESC LIMIT $1`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]contracts.GameCatalogEvent, 0)
	for rows.Next() {
		var (
			event       contracts.GameCatalogEvent
			previousRaw []byte
			currentRaw  []byte
		)
		if err := rows.Scan(
			&event.EventID,
			&event.GameType,
			&event.Action,
			&previousRaw,
			&currentRaw,
			&event.Artifact,
			&event.Actor,
			&event.CreatedAt,
		); err != nil {
			return nil, err
		}
		if len(previousRaw) > 0 {
			event.Previous = &contracts.GameCatalogEntry{}
			if err := json.Unmarshal(previousRaw, event.Previous); err != nil {
				return nil, err
			}
		}
		if len(currentRaw) > 0 {
			event.Current = &contracts.GameCatalogEntry{}
			if err := json.Unmarshal(currentRaw, event.Current); err != nil {
				return nil, err
			}
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func normalizeGameCatalogEntry(entry contracts.GameCatalogEntry) contracts.GameCatalogEntry {
	entry.GameType = strings.TrimSpace(entry.GameType)
	entry.ActiveEngineVersion = strings.TrimSpace(entry.ActiveEngineVersion)
	if entry.Revision <= 0 {
		entry.Revision = 1
	}
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = time.Now().UTC()
	}
	return entry
}

func validateGameCatalogEntry(entry contracts.GameCatalogEntry) error {
	switch {
	case entry.GameType == "":
		return fmt.Errorf("%w: game type is required", ErrInvalidGameCatalog)
	case entry.ActiveEngineVersion == "":
		return fmt.Errorf("%w: active engine version is required", ErrInvalidGameCatalog)
	case entry.DefaultMaxPlayers <= 0:
		return fmt.Errorf("%w: default max players must be positive", ErrInvalidGameCatalog)
	case entry.ReplaySchemaVersion <= 0:
		return fmt.Errorf("%w: replay schema version must be positive", ErrInvalidGameCatalog)
	default:
		return nil
	}
}

func sameGameCatalogRegistration(left, right contracts.GameCatalogEntry) bool {
	return left.GameType == right.GameType &&
		left.ActiveEngineVersion == right.ActiveEngineVersion &&
		left.DefaultMaxPlayers == right.DefaultMaxPlayers &&
		left.ReplaySchemaVersion == right.ReplaySchemaVersion &&
		left.EnabledForNewRooms == right.EnabledForNewRooms &&
		left.IsDefault == right.IsDefault
}

func insertGameCatalogEvent(
	ctx context.Context,
	tx *sql.Tx,
	action string,
	previous,
	current *contracts.GameCatalogEntry,
	artifact,
	actor string,
) error {
	gameType := ""
	if current != nil {
		gameType = current.GameType
	} else if previous != nil {
		gameType = previous.GameType
	}
	previousJSON, err := json.Marshal(previous)
	if err != nil {
		return err
	}
	currentJSON, err := json.Marshal(current)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(
		ctx,
		`INSERT INTO game_catalog_events (
			game_type, action, previous_entry, current_entry, artifact, actor, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		gameType,
		action,
		nullableCatalogJSON(previous, previousJSON),
		nullableCatalogJSON(current, currentJSON),
		strings.TrimSpace(artifact),
		strings.TrimSpace(actor),
		time.Now().UTC(),
	)
	return err
}

func nullableCatalogJSON(entry *contracts.GameCatalogEntry, encoded []byte) any {
	if entry == nil {
		return nil
	}
	return string(encoded)
}

func scanGameCatalogEntry(row roomScanner) (contracts.GameCatalogEntry, error) {
	var entry contracts.GameCatalogEntry
	err := row.Scan(
		&entry.GameType,
		&entry.ActiveEngineVersion,
		&entry.DefaultMaxPlayers,
		&entry.ReplaySchemaVersion,
		&entry.EnabledForNewRooms,
		&entry.IsDefault,
		&entry.Revision,
		&entry.UpdatedAt,
	)
	return entry, err
}
