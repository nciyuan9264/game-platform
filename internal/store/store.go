package store

import (
	"context"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/history"
)

type Store struct {
	Redis    *RedisStore
	Postgres *PostgresStore
}

func NewStore(redis *RedisStore, postgres *PostgresStore) *Store {
	return &Store{Redis: redis, Postgres: postgres}
}

func (s *Store) Close() error {
	var err error
	if s.Redis != nil {
		err = s.Redis.Close()
	}
	if s.Postgres != nil {
		if pgErr := s.Postgres.Close(); err == nil {
			err = pgErr
		}
	}
	return err
}

func (s *Store) Ping(ctx context.Context) error {
	if s.Redis != nil {
		if err := s.Redis.Ping(ctx); err != nil {
			return err
		}
	}
	if s.Postgres != nil {
		return s.Postgres.Ping(ctx)
	}
	return nil
}

func (s *Store) Increment(ctx context.Context, key string) (int64, error) {
	return s.Redis.Increment(ctx, key)
}

func (s *Store) AllowRateLimit(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	return s.Redis.AllowRateLimit(ctx, key, limit, window)
}

func (s *Store) UpsertNode(ctx context.Context, n contracts.NodeInfo, ttl time.Duration) error {
	return s.Redis.UpsertNode(ctx, n, ttl)
}

func (s *Store) ListNodes(ctx context.Context, nodeType string) ([]contracts.NodeInfo, error) {
	return s.Redis.ListNodes(ctx, nodeType)
}

func (s *Store) SetSession(ctx context.Context, rec contracts.SessionRecord, ttl time.Duration) error {
	return s.Redis.SetSession(ctx, rec, ttl)
}

func (s *Store) GetSession(ctx context.Context, sessionID string) (contracts.SessionRecord, error) {
	return s.Redis.GetSession(ctx, sessionID)
}

func (s *Store) ListSessions(ctx context.Context) ([]contracts.SessionRecord, error) {
	return s.Redis.ListSessions(ctx)
}

func (s *Store) ListGatewaySessions(ctx context.Context, gatewayNode string) ([]contracts.SessionRecord, error) {
	return s.Redis.ListGatewaySessions(ctx, gatewayNode)
}

func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	return s.Redis.DeleteSession(ctx, sessionID)
}

func (s *Store) SetPresence(ctx context.Context, rec contracts.PresenceRecord, ttl time.Duration) error {
	return s.Redis.SetPresence(ctx, rec, ttl)
}

func (s *Store) ListPresence(ctx context.Context, roomID string) ([]contracts.PresenceRecord, error) {
	return s.Redis.ListPresence(ctx, roomID)
}

func (s *Store) ListAllPresence(ctx context.Context) ([]contracts.PresenceRecord, error) {
	return s.Redis.ListAllPresence(ctx)
}

func (s *Store) ListGatewayPresence(ctx context.Context, gatewayNode string) ([]contracts.PresenceRecord, error) {
	return s.Redis.ListGatewayPresence(ctx, gatewayNode)
}

func (s *Store) MarkPresenceOffline(ctx context.Context, roomID, sessionID string) error {
	return s.Redis.MarkPresenceOffline(ctx, roomID, sessionID)
}

func (s *Store) SetRoom(ctx context.Context, rec contracts.RoomRecord) error {
	return s.Postgres.SetRoom(ctx, rec)
}

func (s *Store) ReserveRoomCreation(ctx context.Context, rec contracts.RoomRecord, requestID, requestHash string, maxOwnedRooms int) (contracts.RoomRecord, bool, error) {
	return s.Postgres.ReserveRoomCreation(ctx, rec, requestID, requestHash, maxOwnedRooms)
}

func (s *Store) DeleteRoomCreation(ctx context.Context, roomID string) error {
	return s.Postgres.DeleteRoomCreation(ctx, roomID)
}

func (s *Store) GetRoom(ctx context.Context, roomID string) (contracts.RoomRecord, error) {
	return s.Postgres.GetRoom(ctx, roomID)
}

func (s *Store) SetRoomVisibility(ctx context.Context, roomID, visibility string) error {
	return s.Postgres.SetRoomVisibility(ctx, roomID, visibility)
}

func (s *Store) RoomVisibility(ctx context.Context, roomID string) (string, error) {
	return s.Postgres.RoomVisibility(ctx, roomID)
}

func (s *Store) ListRooms(ctx context.Context, gameType string) ([]contracts.RoomRecord, error) {
	return s.Postgres.ListRooms(ctx, gameType)
}

func (s *Store) PopulateSpectatorRoomAccess(ctx context.Context, rooms []contracts.RoomRecord, userID string) error {
	return s.Postgres.PopulateSpectatorRoomAccess(ctx, rooms, userID)
}

func (s *Store) ListExpiredRooms(ctx context.Context, now time.Time, limit int) ([]contracts.RoomRecord, error) {
	return s.Postgres.ListExpiredRooms(ctx, now, limit)
}

func (s *Store) ReserveRoomOwner(ctx context.Context, roomID string, expectedEpoch int64, nodeID string, now time.Time, recoveryTimeout time.Duration) (contracts.RoomRecord, error) {
	return s.Postgres.ReserveRoomOwner(ctx, roomID, expectedEpoch, nodeID, now, recoveryTimeout)
}

func (s *Store) VerifyRoomOwnerCandidate(ctx context.Context, roomID, nodeID string, epoch int64) error {
	return s.Postgres.VerifyRoomOwnerCandidate(ctx, roomID, nodeID, epoch)
}

func (s *Store) MarkRoomOwnerReady(ctx context.Context, roomID, nodeID string, epoch int64, leaseExpiresAt time.Time) error {
	return s.Postgres.MarkRoomOwnerReady(ctx, roomID, nodeID, epoch, leaseExpiresAt)
}

func (s *Store) RenewRoomOwnerProjection(ctx context.Context, roomID, nodeID string, epoch int64, leaseExpiresAt time.Time) error {
	return s.Postgres.RenewRoomOwnerProjection(ctx, roomID, nodeID, epoch, leaseExpiresAt)
}

func (s *Store) RoomCountsByOwner(ctx context.Context, pendingSince time.Time) (map[string]int, map[string]int, error) {
	return s.Postgres.RoomCountsByOwner(ctx, pendingSince)
}

func (s *Store) ListMatches(ctx context.Context, gameType, status, userID string, limit int) ([]contracts.MatchRecord, error) {
	return s.Postgres.ListMatches(ctx, gameType, status, userID, limit)
}

func (s *Store) GetMatch(ctx context.Context, matchID string) (contracts.MatchRecord, error) {
	return s.Postgres.GetMatch(ctx, matchID)
}

func (s *Store) ReserveNextMatch(ctx context.Context, roomID, requestID string, randomSeed uint64) (contracts.RoomRecord, contracts.MatchRecord, bool, error) {
	return s.Postgres.ReserveNextMatch(ctx, roomID, requestID, randomSeed)
}

func (s *Store) SetMatchRuntime(ctx context.Context, matchID, status, ownerNode string, epoch, lastAppliedSeq int64, playerCount int) error {
	return s.Postgres.SetMatchRuntime(ctx, matchID, status, ownerNode, epoch, lastAppliedSeq, playerCount)
}

func (s *Store) ClaimMatchRuntime(ctx context.Context, matchID, ownerNode string, epoch int64) error {
	return s.Postgres.ClaimMatchRuntime(ctx, matchID, ownerNode, epoch)
}

func (s *Store) FinishMatch(ctx context.Context, roomID, matchID, status, reason, ownerNode string, epoch, lastAppliedSeq int64) error {
	return s.Postgres.FinishMatch(ctx, roomID, matchID, status, reason, ownerNode, epoch, lastAppliedSeq)
}

func (s *Store) UpsertMatchPlayer(ctx context.Context, matchID string, player contracts.MatchPlayerRecord) error {
	return s.Postgres.UpsertMatchPlayer(ctx, matchID, player)
}

func (s *Store) SyncMatchRoster(ctx context.Context, matchID string, players []contracts.MatchPlayerRecord) error {
	return s.Postgres.SyncMatchRoster(ctx, matchID, players)
}

func (s *Store) MarkMatchPlayerOffline(ctx context.Context, matchID, playerID string, lastSeen time.Time) error {
	return s.Postgres.MarkMatchPlayerOffline(ctx, matchID, playerID, lastSeen)
}

func (s *Store) ListMatchPlayers(ctx context.Context, matchID string) ([]contracts.MatchPlayerRecord, error) {
	return s.Postgres.listMatchPlayers(ctx, matchID)
}

func (s *Store) UpdateRoom(ctx context.Context, roomID string, fn func(*contracts.RoomRecord) error) (contracts.RoomRecord, error) {
	return s.Postgres.UpdateRoom(ctx, roomID, fn)
}

func (s *Store) SaveSnapshot(ctx context.Context, rec contracts.SnapshotRecord) error {
	return s.Postgres.SaveSnapshot(ctx, rec)
}

func (s *Store) LatestSnapshot(ctx context.Context, matchID string) (contracts.SnapshotRecord, error) {
	return s.Postgres.LatestSnapshot(ctx, matchID)
}

func (s *Store) SaveSpectatorSnapshot(ctx context.Context, rec contracts.SpectatorSnapshotRecord) error {
	return s.Postgres.SaveSpectatorSnapshot(ctx, rec)
}

func (s *Store) CreateSpectatorSession(ctx context.Context, session contracts.SpectatorSession) error {
	return s.Postgres.CreateSpectatorSession(ctx, session)
}

func (s *Store) GetSpectatorSession(ctx context.Context, sessionID, userID string) (contracts.SpectatorSession, error) {
	return s.Postgres.GetSpectatorSession(ctx, sessionID, userID)
}

func (s *Store) LatestAvailableSpectatorSnapshot(ctx context.Context, matchID string) (contracts.SpectatorSnapshotRecord, error) {
	return s.Postgres.LatestAvailableSpectatorSnapshot(ctx, matchID)
}

func (s *Store) NextSpectatorSnapshotAvailability(ctx context.Context, matchID string) (time.Time, error) {
	return s.Postgres.NextSpectatorSnapshotAvailability(ctx, matchID)
}

func (s *Store) SaveOutboundMessage(ctx context.Context, msg contracts.OutboundMessage) error {
	return s.Postgres.SaveOutboundMessage(ctx, msg)
}

func (s *Store) MarkOutboundDelivered(ctx context.Context, messageID string) error {
	return s.Postgres.MarkOutboundDelivered(ctx, messageID)
}

func (s *Store) ListOutboundMessages(ctx context.Context, roomID, matchID, playerID string, afterSeq int64, limit int) ([]contracts.OutboundMessage, error) {
	return s.Postgres.ListOutboundMessages(ctx, roomID, matchID, playerID, afterSeq, limit)
}

func (s *Store) LatestOutboundMessage(ctx context.Context, roomID, matchID, playerID string) (contracts.OutboundMessage, error) {
	return s.Postgres.LatestOutboundMessage(ctx, roomID, matchID, playerID)
}

func (s *Store) OutboundSeqRange(ctx context.Context, roomID, matchID, playerID string) (int64, int64, error) {
	return s.Postgres.OutboundSeqRange(ctx, roomID, matchID, playerID)
}

func (s *Store) SaveCommandAck(ctx context.Context, ack contracts.CommandAck) error {
	return s.Postgres.SaveCommandAck(ctx, ack)
}

func (s *Store) AppendGameLogEvents(ctx context.Context, events []contracts.GameLogEvent) error {
	return s.Postgres.AppendGameLogEvents(ctx, events)
}

func (s *Store) GetCommandAck(ctx context.Context, roomID, matchID, commandID, intentHash string) (contracts.CommandAck, error) {
	return s.Postgres.GetCommandAck(ctx, roomID, matchID, commandID, intentHash)
}

func (s *Store) SaveRuntimeEvent(ctx context.Context, event contracts.RuntimeEvent) error {
	return s.Postgres.SaveRuntimeEvent(ctx, event)
}

func (s *Store) ListRuntimeEvents(ctx context.Context, roomID string, afterID int64, limit int) ([]contracts.RuntimeEvent, error) {
	return s.Postgres.ListRuntimeEvents(ctx, roomID, afterID, limit)
}

func (s *Store) CleanupOperationalData(ctx context.Context, now time.Time) error {
	return s.Postgres.CleanupOperationalData(ctx, now)
}

func (s *Store) StartHistoryGame(ctx context.Context, gameRecord history.Game, players []history.Player) (int64, error) {
	return s.Postgres.StartHistoryGame(ctx, gameRecord, players)
}

func (s *Store) OpenHistoryGame(ctx context.Context, roomID, gameType string) (int64, int, error) {
	return s.Postgres.OpenHistoryGame(ctx, roomID, gameType)
}

func (s *Store) ResetOpenHistoryGame(ctx context.Context, roomID, gameType string) error {
	return s.Postgres.ResetOpenHistoryGame(ctx, roomID, gameType)
}

func (s *Store) AppendHistoryEvent(ctx context.Context, event history.Event) error {
	return s.Postgres.AppendHistoryEvent(ctx, event)
}

func (s *Store) FinishHistoryGame(ctx context.Context, gameID int64, input history.FinalizeInput) error {
	return s.Postgres.FinishHistoryGame(ctx, gameID, input)
}

func (s *Store) DiscardHistoryGame(ctx context.Context, gameID int64) error {
	return s.Postgres.DiscardHistoryGame(ctx, gameID)
}

func (s *Store) ListHistoryGames(ctx context.Context, userID int64, gameType string, limit, offset int) ([]history.Game, error) {
	return s.Postgres.ListHistoryGames(ctx, userID, gameType, limit, offset)
}

func (s *Store) CountHistoryGames(ctx context.Context, userID int64, gameType string) (int, error) {
	return s.Postgres.CountHistoryGames(ctx, userID, gameType)
}

func (s *Store) GetHistoryGame(ctx context.Context, gameID int64, gameType string, includeEvents bool) (history.Game, []history.Player, []history.Event, error) {
	return s.Postgres.GetHistoryGame(ctx, gameID, gameType, includeEvents)
}

func (s *Store) GetCompletedHistoryGameForUser(ctx context.Context, gameID, userID int64, gameType string, includeEvents bool) (history.Game, []history.Player, []history.Event, error) {
	return s.Postgres.GetCompletedHistoryGameForUser(ctx, gameID, userID, gameType, includeEvents)
}

func (s *Store) HistoryReplayPageForUser(
	ctx context.Context,
	gameID,
	userID int64,
	gameType string,
	eventOffset,
	eventLimit int,
) (history.Game, []history.Player, int, []history.Event, error) {
	return s.Postgres.HistoryReplayPageForUser(ctx, gameID, userID, gameType, eventOffset, eventLimit)
}

func (s *Store) HistoryStats(ctx context.Context, userID int64, gameType string) (history.Stats, error) {
	return s.Postgres.HistoryStats(ctx, userID, gameType)
}

func (s *Store) HistoryLeaderboard(ctx context.Context, gameType string, limit, offset int) ([]history.LeaderboardEntry, error) {
	return s.Postgres.HistoryLeaderboard(ctx, gameType, limit, offset)
}

func (s *Store) HistoryLeaderboardSorted(ctx context.Context, gameType, sortBy string, limit, offset int) ([]history.LeaderboardEntry, error) {
	return s.Postgres.HistoryLeaderboardSorted(ctx, gameType, sortBy, limit, offset)
}

func (s *Store) CountLeaderboardEntries(ctx context.Context, gameType string) (int, error) {
	return s.Postgres.CountLeaderboardEntries(ctx, gameType)
}
