package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/lib/pq"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/transport"
)

const projectionWait = 40 * time.Second

type storageVerifier struct {
	db     *sql.DB
	router *transport.Router
}

func newStorageVerifier(ctx context.Context, cfg config) (*storageVerifier, error) {
	verifier := &storageVerifier{}
	if cfg.PostgresDSN != "" {
		db, err := sql.Open("postgres", cfg.PostgresDSN)
		if err != nil {
			return nil, errors.New("open PostgreSQL verifier")
		}
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := db.PingContext(pingCtx); err != nil {
			_ = db.Close()
			return nil, errors.New("connect PostgreSQL verifier")
		}
		var canWrite bool
		if err := db.QueryRowContext(pingCtx, `
			SELECT
				has_table_privilege(current_user, 'rooms', 'INSERT') OR
				has_table_privilege(current_user, 'rooms', 'UPDATE') OR
				has_table_privilege(current_user, 'rooms', 'DELETE')`,
		).Scan(&canWrite); err != nil {
			_ = db.Close()
			return nil, errors.New("inspect PostgreSQL verifier privileges")
		}
		if canWrite {
			_ = db.Close()
			return nil, errors.New("REGRESSION_POSTGRES_DSN must use a read-only account")
		}
		verifier.db = db
	}
	if cfg.NATSURL != "" {
		router, err := transport.NewRouter(cfg.NATSURL)
		if err != nil {
			verifier.Close()
			return nil, errors.New("connect NATS verifier")
		}
		verifier.router = router
	}
	return verifier, nil
}

func (v *storageVerifier) Close() {
	if v == nil {
		return
	}
	if v.db != nil {
		_ = v.db.Close()
	}
	if v.router != nil {
		v.router.Close()
	}
}

func (v *storageVerifier) VerifyActive(
	ctx context.Context,
	observation canaryObservation,
) []checkResult {
	checks := []checkResult{}
	if v.router == nil {
		checks = append(checks, skippedCheck("JetStream command log", "REGRESSION_NATS_URL is not set"))
	} else {
		checks = append(checks, eventualCheck(ctx, "JetStream command log", func() error {
			return v.verifyCommandLog(observation)
		}))
	}
	if v.db == nil {
		checks = append(checks, skippedCheck("PostgreSQL projections", "REGRESSION_POSTGRES_DSN is not set"))
		return checks
	}
	checks = append(checks,
		eventualCheck(ctx, "room, match, catalog, and owner", func() error {
			return v.verifyRoute(ctx, observation)
		}),
		eventualCheck(ctx, "roster and command ACKs", func() error {
			return v.verifyRosterAndACKs(ctx, observation)
		}),
		eventualCheck(ctx, "snapshots, outbound, runtime, and operations", func() error {
			return v.verifyOperationalProjections(ctx, observation)
		}),
		eventualCheck(ctx, "history projection", func() error {
			return v.verifyHistory(ctx, observation)
		}),
	)
	return checks
}

func (v *storageVerifier) VerifyClosed(
	ctx context.Context,
	observation canaryObservation,
) []checkResult {
	if v.db == nil {
		return []checkResult{skippedCheck(
			"closed room storage state",
			"REGRESSION_POSTGRES_DSN is not set",
		)}
	}
	return []checkResult{eventualCheck(ctx, "closed room storage state", func() error {
		var roomStatus, matchStatus string
		var onlinePlayers, openGames, duplicateGames int
		err := v.db.QueryRowContext(ctx, `
			SELECT r.status, m.status,
				(SELECT COUNT(*) FROM match_players mp
					WHERE mp.match_id = m.match_id AND mp.online),
				(SELECT COUNT(*) FROM games g
					WHERE g.room_id = r.room_id AND g.ended_at IS NULL),
				(SELECT COUNT(*) FROM (
					SELECT started_at
					FROM games g
					WHERE g.room_id = r.room_id
					GROUP BY started_at
					HAVING COUNT(*) > 1
				) duplicates)
			FROM rooms r
			JOIN matches m ON m.match_id = r.current_match_id
			WHERE r.room_id = $1`,
			observation.Room.RoomID,
		).Scan(&roomStatus, &matchStatus, &onlinePlayers, &openGames, &duplicateGames)
		if err != nil {
			return err
		}
		if roomStatus != contracts.RoomStatusClosed {
			return fmt.Errorf("room status = %q", roomStatus)
		}
		if matchStatus != contracts.MatchStatusEnded && matchStatus != contracts.MatchStatusAborted {
			return fmt.Errorf("match status = %q", matchStatus)
		}
		if onlinePlayers != 0 {
			return fmt.Errorf("online match players = %d", onlinePlayers)
		}
		if openGames != 0 || duplicateGames != 0 {
			return fmt.Errorf("open histories = %d, duplicate histories = %d", openGames, duplicateGames)
		}
		return nil
	})}
}

func (v *storageVerifier) verifyCommandLog(observation canaryObservation) error {
	records, err := v.router.ListCommandLog(observation.Room.CurrentMatchID, 0)
	if err != nil {
		return err
	}
	if len(records) == 0 || records[0].Kind != contracts.CommandLogKindOwnerFence {
		return errors.New("initial owner fence is missing")
	}
	expectedSeq := int64(1)
	counts := make(map[string]int, len(observation.Commands))
	for _, record := range records {
		if record.Kind == contracts.CommandLogKindOwnerFence {
			continue
		}
		if record.Seq != expectedSeq {
			return fmt.Errorf("command seq = %d, want %d", record.Seq, expectedSeq)
		}
		expectedSeq++
		for _, command := range observation.Commands {
			if record.CommandID != command.CommandID {
				continue
			}
			counts[command.CommandID]++
			if record.Type != command.Type ||
				record.PlayerID != observation.PlayerID ||
				record.Seq != command.Seq {
				return fmt.Errorf("command log mismatch for %s", command.CommandID)
			}
		}
	}
	for _, command := range observation.Commands {
		if counts[command.CommandID] != 1 {
			return fmt.Errorf("command %s physical records = %d", command.CommandID, counts[command.CommandID])
		}
	}
	return nil
}

func (v *storageVerifier) verifyRoute(ctx context.Context, observation canaryObservation) error {
	var gameType, roomStatus, visibility, ownerUserID, ownerPlayerID string
	var ownerNode, engineVersion, matchID, matchStatus, matchOwner, matchVersion string
	var activeVersion, ownerState string
	var epoch, matchEpoch, roomSeq, matchSeq int64
	err := v.db.QueryRowContext(ctx, `
		SELECT r.game_type, r.status, r.visibility, r.owner_user_id, r.owner_player_id,
			r.owner_node, r.epoch, r.last_applied_seq, r.engine_version, r.current_match_id,
			m.status, m.runtime_owner_node, m.epoch, m.last_applied_seq, m.engine_version,
			c.active_engine_version, generation.state
		FROM rooms r
		JOIN matches m ON m.match_id = r.current_match_id
		JOIN game_catalog c ON c.game_type = r.game_type
		JOIN room_owner_generations generation ON generation.room_id = r.room_id
		WHERE r.room_id = $1`,
		observation.Room.RoomID,
	).Scan(
		&gameType, &roomStatus, &visibility, &ownerUserID, &ownerPlayerID,
		&ownerNode, &epoch, &roomSeq, &engineVersion, &matchID,
		&matchStatus, &matchOwner, &matchEpoch, &matchSeq, &matchVersion,
		&activeVersion, &ownerState,
	)
	if err != nil {
		return err
	}
	if gameType != observation.GameType ||
		visibility != contracts.VisibilityPrivate ||
		ownerUserID == "" ||
		ownerPlayerID != observation.PlayerID {
		return fmt.Errorf("room identity mismatch: game=%s visibility=%s owner=%s/%s",
			gameType, visibility, ownerUserID, ownerPlayerID)
	}
	if roomStatus != contracts.RoomStatusOpen ||
		matchID != observation.Room.CurrentMatchID ||
		matchStatus != contracts.MatchStatusRunning {
		return fmt.Errorf("room/match state = %s/%s/%s", roomStatus, matchID, matchStatus)
	}
	if ownerNode == "" || ownerNode != matchOwner ||
		epoch <= 0 || epoch != matchEpoch ||
		ownerState != "ready" {
		return fmt.Errorf("owner route = %s/%s epoch=%d/%d state=%s",
			ownerNode, matchOwner, epoch, matchEpoch, ownerState)
	}
	if engineVersion == "" || engineVersion != matchVersion || engineVersion != activeVersion {
		return fmt.Errorf("engine versions = room:%s match:%s active:%s",
			engineVersion, matchVersion, activeVersion)
	}
	if roomSeq < observation.LastSeq || matchSeq < observation.LastSeq || roomSeq != matchSeq {
		return fmt.Errorf("projected seq = room:%d match:%d observed:%d", roomSeq, matchSeq, observation.LastSeq)
	}
	return nil
}

func (v *storageVerifier) verifyRosterAndACKs(ctx context.Context, observation canaryObservation) error {
	var humans, ais, invalidHumanIDs int
	err := v.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE NOT is_ai),
			COUNT(*) FILTER (WHERE is_ai),
			COUNT(*) FILTER (WHERE NOT is_ai AND (user_id = '' OR player_id <> user_id))
		FROM match_players
		WHERE match_id = $1`,
		observation.Room.CurrentMatchID,
	).Scan(&humans, &ais, &invalidHumanIDs)
	if err != nil {
		return err
	}
	if humans != 1 || ais != 1 || invalidHumanIDs != 0 {
		return fmt.Errorf("roster humans=%d ais=%d invalidHumanIDs=%d", humans, ais, invalidHumanIDs)
	}
	for _, command := range observation.Commands {
		var accepted bool
		var seq int64
		var intentHash string
		err := v.db.QueryRowContext(ctx, `
			SELECT accepted, seq, intent_hash
			FROM room_command_acks
			WHERE match_id = $1 AND command_id = $2`,
			observation.Room.CurrentMatchID,
			command.CommandID,
		).Scan(&accepted, &seq, &intentHash)
		if err != nil {
			return err
		}
		if !accepted || seq != command.Seq || intentHash == "" {
			return fmt.Errorf("ACK %s = accepted:%v seq:%d hash:%t",
				command.CommandID, accepted, seq, intentHash != "")
		}
	}
	return nil
}

func (v *storageVerifier) verifyOperationalProjections(
	ctx context.Context,
	observation canaryObservation,
) error {
	var initialSnapshots, latestSnapshotSeq, latestSpectatorSeq, latestOutboundSeq int64
	err := v.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM room_snapshots WHERE match_id = $1 AND seq = 0),
			COALESCE((SELECT MAX(seq) FROM room_snapshots WHERE match_id = $1), 0),
			COALESCE((SELECT MAX(seq) FROM spectator_snapshots WHERE match_id = $1), 0),
			COALESCE((SELECT MAX(seq) FROM outbound_messages
				WHERE match_id = $1 AND target_player_id = $2 AND reliable), 0)`,
		observation.Room.CurrentMatchID,
		observation.PlayerID,
	).Scan(&initialSnapshots, &latestSnapshotSeq, &latestSpectatorSeq, &latestOutboundSeq)
	if err != nil {
		return err
	}
	if initialSnapshots != 1 || latestSnapshotSeq < 1 {
		return fmt.Errorf("snapshots initial=%d latest=%d", initialSnapshots, latestSnapshotSeq)
	}
	if latestSpectatorSeq < observation.LastSeq || latestOutboundSeq < observation.LastSeq {
		return fmt.Errorf("latest projection seq spectator=%d outbound=%d observed=%d",
			latestSpectatorSeq, latestOutboundSeq, observation.LastSeq)
	}
	var requiredEvents int
	err = v.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT event)
		FROM runtime_events
		WHERE room_id = $1
			AND event IN ('room_created', 'websocket_connected', 'command_applied', 'snapshot_saved')`,
		observation.Room.RoomID,
	).Scan(&requiredEvents)
	if err != nil {
		return err
	}
	if requiredEvents != 4 {
		return fmt.Errorf("required runtime event types = %d, want 4", requiredEvents)
	}
	var duplicateOperations, futureOperations int
	err = v.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM (
				SELECT event_id
				FROM game_operation_events
				WHERE match_id = $1
				GROUP BY event_id
				HAVING COUNT(*) > 1
			) duplicates),
			(SELECT COUNT(*) FROM game_operation_events WHERE match_id = $1 AND seq > $2)`,
		observation.Room.CurrentMatchID,
		observation.LastSeq,
	).Scan(&duplicateOperations, &futureOperations)
	if err != nil {
		return err
	}
	if duplicateOperations != 0 || futureOperations != 0 {
		return fmt.Errorf("operation duplicates=%d future=%d", duplicateOperations, futureOperations)
	}
	return nil
}

func (v *storageVerifier) verifyHistory(ctx context.Context, observation canaryObservation) error {
	var gameID int64
	var gameCount int
	err := v.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(id), 0), COUNT(*)
		FROM games
		WHERE room_id = $1 AND game_type = $2 AND ended_at IS NULL`,
		observation.Room.RoomID,
		observation.GameType,
	).Scan(&gameID, &gameCount)
	if err != nil {
		return err
	}
	if gameID == 0 || gameCount != 1 {
		return fmt.Errorf("open history game ID/count = %d/%d", gameID, gameCount)
	}
	var humans, ais, invalidHumanIDs int
	err = v.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE NOT is_ai),
			COUNT(*) FILTER (WHERE is_ai),
			COUNT(*) FILTER (WHERE NOT is_ai AND
				(user_id IS NULL OR player_id <> user_id::text OR display_name = ''))
		FROM game_players
		WHERE game_id = $1`,
		gameID,
	).Scan(&humans, &ais, &invalidHumanIDs)
	if err != nil {
		return err
	}
	if humans != 1 || ais != 1 || invalidHumanIDs != 0 {
		return fmt.Errorf("history players humans=%d ais=%d invalid=%d", humans, ais, invalidHumanIDs)
	}
	action := observation.Commands[len(observation.Commands)-1]
	var actionEvents, duplicateEvents int
	err = v.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE cmd_type = $2),
			(SELECT COUNT(*) FROM (
				SELECT seq
				FROM game_events
				WHERE game_id = $1
				GROUP BY seq
				HAVING COUNT(*) > 1
			) duplicates)
		FROM game_events
		WHERE game_id = $1`,
		gameID,
		action.Type,
	).Scan(&actionEvents, &duplicateEvents)
	if err != nil {
		return err
	}
	if actionEvents != 1 || duplicateEvents != 0 {
		return fmt.Errorf("history action events=%d duplicates=%d", actionEvents, duplicateEvents)
	}
	return nil
}

func eventualCheck(ctx context.Context, name string, fn func() error) checkResult {
	started := time.Now()
	deadline := time.NewTimer(projectionWait)
	defer deadline.Stop()
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		if err := fn(); err == nil {
			return checkResult{
				Name:       name,
				Status:     checkPassed,
				DurationMS: time.Since(started).Milliseconds(),
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return checkResult{
				Name:       name,
				Status:     checkFailed,
				DurationMS: time.Since(started).Milliseconds(),
				Message:    ctx.Err().Error(),
			}
		case <-deadline.C:
			return checkResult{
				Name:       name,
				Status:     checkFailed,
				DurationMS: time.Since(started).Milliseconds(),
				Message:    lastErr.Error(),
			}
		case <-ticker.C:
		}
	}
}
