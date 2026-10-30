package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/redis/go-redis/v9"
)

type RedisStore struct {
	client *redis.Client
}

const (
	nodeIndexKey     = "index:nodes"
	sessionIndexKey  = "index:sessions"
	presenceIndexKey = "index:presence"
	indexVersionKey  = "index:version:v1"
	indexRebuildLock = "index:rebuild:v1"
)

var rateLimitScript = redis.NewScript(`
local current = redis.call("INCR", KEYS[1])
if current == 1 then
	redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
local ttl = redis.call("PTTL", KEYS[1])
return {current, ttl}
`)

var claimPlayerSessionScript = redis.NewScript(`
local previous = redis.call("GET", KEYS[1])
redis.call("SET", KEYS[1], ARGV[1], "PX", ARGV[2])
return previous or ""
`)

var refreshPlayerSessionScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	redis.call("PEXPIRE", KEYS[1], ARGV[2])
	return 1
end
return 0
`)

var releasePlayerSessionScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
end
return 0
`)

var claimPlayerDisconnectScript = redis.NewScript(`
local current = redis.call("GET", KEYS[1])
if not current or current == ARGV[1] then
	redis.call("SET", KEYS[1], ARGV[2], "PX", ARGV[3])
	return 1
end
return 0
`)

func NewRedisStore(addr string, db int) *RedisStore {
	return NewRedisStoreWithPassword(addr, "", db)
}

func NewRedisStoreWithPassword(addr, password string, db int) *RedisStore {
	return &RedisStore{client: redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db})}
}

func (s *RedisStore) Close() error {
	return s.client.Close()
}

func (s *RedisStore) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

func (s *RedisStore) EnsureIndexes(ctx context.Context) error {
	if exists, err := s.client.Exists(ctx, indexVersionKey).Result(); err != nil {
		return err
	} else if exists > 0 {
		return nil
	}
	acquired, err := s.client.SetNX(ctx, indexRebuildLock, "1", 2*time.Minute).Result()
	if err != nil {
		return err
	}
	if !acquired {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if exists, err := s.client.Exists(ctx, indexVersionKey).Result(); err == nil && exists > 0 {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		return errors.New("redis index rebuild did not complete")
	}
	defer s.client.Del(context.Background(), indexRebuildLock)

	if err := s.rebuildNodeIndex(ctx); err != nil {
		return err
	}
	if err := s.rebuildSessionIndex(ctx); err != nil {
		return err
	}
	if err := s.rebuildPresenceIndex(ctx); err != nil {
		return err
	}
	return s.client.Set(ctx, indexVersionKey, "1", 0).Err()
}

func (s *RedisStore) Increment(ctx context.Context, key string) (int64, error) {
	return s.client.Incr(ctx, key).Result()
}

func (s *RedisStore) AllowRateLimit(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	if limit <= 0 || window <= 0 {
		return true, 0, nil
	}
	result, err := rateLimitScript.Run(ctx, s.client, []string{key}, window.Milliseconds()).Result()
	if err != nil {
		return false, 0, err
	}
	values, ok := result.([]any)
	if !ok || len(values) != 2 {
		return false, 0, errors.New("invalid redis rate limit response")
	}
	current, ok := values[0].(int64)
	if !ok {
		return false, 0, errors.New("invalid redis rate limit counter")
	}
	ttlMillis, ok := values[1].(int64)
	if !ok {
		return false, 0, errors.New("invalid redis rate limit ttl")
	}
	retryAfter := time.Duration(ttlMillis) * time.Millisecond
	if retryAfter < 0 {
		retryAfter = window
	}
	return current <= int64(limit), retryAfter, nil
}

func (s *RedisStore) UpsertNode(ctx context.Context, n contracts.NodeInfo, ttl time.Duration) error {
	n.LastHeartbeat = time.Now().UTC()
	key := fmt.Sprintf("node:%s", n.NodeID)
	return s.setIndexedJSON(ctx, key, n, ttl, nodeIndexKey)
}

func (s *RedisStore) ListNodes(ctx context.Context, nodeType string) ([]contracts.NodeInfo, error) {
	keys, err := s.client.SMembers(ctx, nodeIndexKey).Result()
	if err != nil {
		return nil, err
	}
	nodes := make([]contracts.NodeInfo, 0, len(keys))
	for _, key := range keys {
		var n contracts.NodeInfo
		if err := s.getJSON(ctx, key, &n); err != nil {
			if errors.Is(err, redis.Nil) {
				_ = s.client.SRem(ctx, nodeIndexKey, key).Err()
			}
			continue
		}
		if nodeType == "" || n.Type == nodeType {
			nodes = append(nodes, n)
		}
	}
	return nodes, nil
}

func (s *RedisStore) SetSession(ctx context.Context, rec contracts.SessionRecord, ttl time.Duration) error {
	rec.LastSeen = time.Now().UTC()
	key := fmt.Sprintf("session:%s", rec.SessionID)
	return s.setIndexedJSON(ctx, key, rec, ttl, sessionIndexKey, gatewaySessionIndex(rec.GatewayNode))
}

func (s *RedisStore) ClaimPlayerSession(
	ctx context.Context,
	roomID,
	playerID string,
	ref contracts.ConnectionRef,
	ttl time.Duration,
) (contracts.ConnectionRef, error) {
	if ttl <= 0 {
		return contracts.ConnectionRef{}, errors.New("player session ttl must be positive")
	}
	value, err := playerSessionValue(roomID, playerID, ref)
	if err != nil {
		return contracts.ConnectionRef{}, err
	}
	result, err := claimPlayerSessionScript.Run(
		ctx,
		s.client,
		[]string{playerSessionKey(roomID, playerID)},
		value,
		ttl.Milliseconds(),
	).Text()
	if err != nil {
		return contracts.ConnectionRef{}, err
	}
	if result == "" {
		return contracts.ConnectionRef{}, nil
	}
	var previous contracts.ConnectionRef
	if err := json.Unmarshal([]byte(result), &previous); err != nil {
		return contracts.ConnectionRef{}, fmt.Errorf("decode previous player session: %w", err)
	}
	return previous, nil
}

func (s *RedisStore) RefreshPlayerSession(
	ctx context.Context,
	roomID,
	playerID string,
	ref contracts.ConnectionRef,
	ttl time.Duration,
) (bool, error) {
	if ttl <= 0 {
		return false, errors.New("player session ttl must be positive")
	}
	value, err := playerSessionValue(roomID, playerID, ref)
	if err != nil {
		return false, err
	}
	result, err := refreshPlayerSessionScript.Run(
		ctx,
		s.client,
		[]string{playerSessionKey(roomID, playerID)},
		value,
		ttl.Milliseconds(),
	).Int()
	return result == 1, err
}

func (s *RedisStore) IsPlayerSessionCurrent(
	ctx context.Context,
	roomID,
	playerID string,
	ref contracts.ConnectionRef,
) (current bool, found bool, err error) {
	if roomID == "" || playerID == "" || !ref.Valid() {
		return false, false, errors.New("roomID, playerID, and connection reference are required")
	}
	value, err := s.client.Get(ctx, playerSessionKey(roomID, playerID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	var active contracts.ConnectionRef
	if err := json.Unmarshal(value, &active); err != nil {
		return false, true, fmt.Errorf("decode active player session: %w", err)
	}
	return active == ref, true, nil
}

func (s *RedisStore) ReleasePlayerSession(
	ctx context.Context,
	roomID,
	playerID string,
	ref contracts.ConnectionRef,
) (bool, error) {
	value, err := playerSessionValue(roomID, playerID, ref)
	if err != nil {
		return false, err
	}
	result, err := releasePlayerSessionScript.Run(
		ctx,
		s.client,
		[]string{playerSessionKey(roomID, playerID)},
		value,
	).Int()
	return result == 1, err
}

func (s *RedisStore) ClaimPlayerDisconnect(
	ctx context.Context,
	roomID,
	playerID string,
	ref contracts.ConnectionRef,
	ttl time.Duration,
) (bool, error) {
	if ttl <= 0 {
		return false, errors.New("player disconnect claim ttl must be positive")
	}
	value, err := playerSessionValue(roomID, playerID, ref)
	if err != nil {
		return false, err
	}
	tombstone, err := json.Marshal(contracts.ConnectionRef{SessionID: "disconnect"})
	if err != nil {
		return false, err
	}
	result, err := claimPlayerDisconnectScript.Run(
		ctx,
		s.client,
		[]string{playerSessionKey(roomID, playerID)},
		value,
		tombstone,
		ttl.Milliseconds(),
	).Int()
	return result == 1, err
}

func playerSessionKey(roomID, playerID string) string {
	return fmt.Sprintf("active-player-session:%s:%s", roomID, playerID)
}

func playerSessionValue(
	roomID,
	playerID string,
	ref contracts.ConnectionRef,
) ([]byte, error) {
	if roomID == "" || playerID == "" || !ref.Valid() {
		return nil, errors.New("roomID, playerID, and connection reference are required")
	}
	return json.Marshal(ref)
}

func (s *RedisStore) GetSession(ctx context.Context, sessionID string) (contracts.SessionRecord, error) {
	var rec contracts.SessionRecord
	err := s.getJSON(ctx, fmt.Sprintf("session:%s", sessionID), &rec)
	return rec, err
}

func (s *RedisStore) ListSessions(ctx context.Context) ([]contracts.SessionRecord, error) {
	return s.listSessionsByIndex(ctx, sessionIndexKey)
}

func (s *RedisStore) ListGatewaySessions(ctx context.Context, gatewayNode string) ([]contracts.SessionRecord, error) {
	return s.listSessionsByIndex(ctx, gatewaySessionIndex(gatewayNode))
}

func (s *RedisStore) listSessionsByIndex(ctx context.Context, indexKey string) ([]contracts.SessionRecord, error) {
	keys, err := s.client.SMembers(ctx, indexKey).Result()
	if err != nil {
		return nil, err
	}
	items := make([]contracts.SessionRecord, 0, len(keys))
	for _, key := range keys {
		var rec contracts.SessionRecord
		if err := s.getJSON(ctx, key, &rec); err != nil {
			if errors.Is(err, redis.Nil) {
				_ = s.client.SRem(ctx, indexKey, key).Err()
				_ = s.client.SRem(ctx, sessionIndexKey, key).Err()
			}
			continue
		}
		items = append(items, rec)
	}
	return items, nil
}

func (s *RedisStore) DeleteSession(ctx context.Context, sessionID string) error {
	key := fmt.Sprintf("session:%s", sessionID)
	var rec contracts.SessionRecord
	_ = s.getJSON(ctx, key, &rec)
	_, err := s.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Del(ctx, key)
		pipe.SRem(ctx, sessionIndexKey, key)
		if rec.GatewayNode != "" {
			pipe.SRem(ctx, gatewaySessionIndex(rec.GatewayNode), key)
		}
		return nil
	})
	return err
}

func (s *RedisStore) SetPresence(ctx context.Context, rec contracts.PresenceRecord, ttl time.Duration) error {
	rec.LastSeen = time.Now().UTC()
	key := fmt.Sprintf("presence:%s:%s", rec.RoomID, rec.SessionID)
	return s.setIndexedJSON(ctx, key, rec, ttl, presenceIndexKey, roomPresenceIndex(rec.RoomID), gatewayPresenceIndex(rec.GatewayNode))
}

func (s *RedisStore) ListPresence(ctx context.Context, roomID string) ([]contracts.PresenceRecord, error) {
	indexKey := roomPresenceIndex(roomID)
	keys, err := s.client.SMembers(ctx, indexKey).Result()
	if err != nil {
		return nil, err
	}
	items := make([]contracts.PresenceRecord, 0, len(keys))
	for _, key := range keys {
		var rec contracts.PresenceRecord
		if err := s.getJSON(ctx, key, &rec); err != nil {
			if errors.Is(err, redis.Nil) {
				_ = s.client.SRem(ctx, indexKey, key).Err()
				_ = s.client.SRem(ctx, presenceIndexKey, key).Err()
			}
			continue
		}
		items = append(items, rec)
	}
	return items, nil
}

func (s *RedisStore) ListAllPresence(ctx context.Context) ([]contracts.PresenceRecord, error) {
	return s.listPresenceByIndex(ctx, presenceIndexKey)
}

func (s *RedisStore) ListGatewayPresence(ctx context.Context, gatewayNode string) ([]contracts.PresenceRecord, error) {
	return s.listPresenceByIndex(ctx, gatewayPresenceIndex(gatewayNode))
}

func (s *RedisStore) listPresenceByIndex(ctx context.Context, indexKey string) ([]contracts.PresenceRecord, error) {
	keys, err := s.client.SMembers(ctx, indexKey).Result()
	if err != nil {
		return nil, err
	}
	items := make([]contracts.PresenceRecord, 0, len(keys))
	for _, key := range keys {
		var rec contracts.PresenceRecord
		if err := s.getJSON(ctx, key, &rec); err != nil {
			if errors.Is(err, redis.Nil) {
				_ = s.client.SRem(ctx, indexKey, key).Err()
				_ = s.client.SRem(ctx, presenceIndexKey, key).Err()
			}
			continue
		}
		items = append(items, rec)
	}
	return items, nil
}

func (s *RedisStore) MarkPresenceOffline(ctx context.Context, roomID, sessionID string) error {
	key := fmt.Sprintf("presence:%s:%s", roomID, sessionID)
	var rec contracts.PresenceRecord
	if err := s.getJSON(ctx, key, &rec); err != nil {
		return err
	}
	rec.Online = false
	rec.LastSeen = time.Now().UTC()
	return s.setIndexedJSON(ctx, key, rec, time.Hour, presenceIndexKey, roomPresenceIndex(roomID), gatewayPresenceIndex(rec.GatewayNode))
}

func (s *RedisStore) setIndexedJSON(ctx context.Context, key string, value any, ttl time.Duration, indexes ...string) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, key, b, ttl)
		for _, index := range indexes {
			if index != "" {
				pipe.SAdd(ctx, index, key)
			}
		}
		return nil
	})
	return err
}

func (s *RedisStore) getJSON(ctx context.Context, key string, out any) error {
	b, err := s.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return err
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func gatewaySessionIndex(gatewayNode string) string {
	if gatewayNode == "" {
		return ""
	}
	return "index:gateway:" + gatewayNode + ":sessions"
}

func roomPresenceIndex(roomID string) string {
	return "index:room:" + roomID + ":presence"
}

func gatewayPresenceIndex(gatewayNode string) string {
	if gatewayNode == "" {
		return ""
	}
	return "index:gateway:" + gatewayNode + ":presence"
}

func (s *RedisStore) rebuildNodeIndex(ctx context.Context) error {
	keys, err := s.scanPattern(ctx, "node:*")
	if err != nil || len(keys) == 0 {
		return err
	}
	return s.client.SAdd(ctx, nodeIndexKey, stringSliceToAny(keys)...).Err()
}

func (s *RedisStore) rebuildSessionIndex(ctx context.Context) error {
	keys, err := s.scanPattern(ctx, "session:*")
	if err != nil {
		return err
	}
	for _, key := range keys {
		var rec contracts.SessionRecord
		if err := s.getJSON(ctx, key, &rec); err != nil {
			continue
		}
		if _, err := s.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.SAdd(ctx, sessionIndexKey, key)
			pipe.SAdd(ctx, gatewaySessionIndex(rec.GatewayNode), key)
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *RedisStore) rebuildPresenceIndex(ctx context.Context) error {
	keys, err := s.scanPattern(ctx, "presence:*")
	if err != nil {
		return err
	}
	for _, key := range keys {
		var rec contracts.PresenceRecord
		if err := s.getJSON(ctx, key, &rec); err != nil {
			continue
		}
		if _, err := s.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.SAdd(ctx, presenceIndexKey, key)
			pipe.SAdd(ctx, roomPresenceIndex(rec.RoomID), key)
			pipe.SAdd(ctx, gatewayPresenceIndex(rec.GatewayNode), key)
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *RedisStore) scanPattern(ctx context.Context, pattern string) ([]string, error) {
	var cursor uint64
	var keys []string
	for {
		batch, next, err := s.client.Scan(ctx, cursor, pattern, 500).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			return keys, nil
		}
	}
}

func stringSliceToAny(values []string) []any {
	out := make([]any, len(values))
	for i := range values {
		out[i] = values[i]
	}
	return out
}
