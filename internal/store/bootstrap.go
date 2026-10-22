package store

import (
	"context"
	"fmt"
)

type Config struct {
	RedisAddr     string
	RedisPassword string
	RedisDB       int
	PostgresDSN   string
}

func Open(ctx context.Context, cfg Config) (*Store, error) {
	redis := NewRedisStoreWithPassword(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err := redis.Ping(ctx); err != nil {
		_ = redis.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	if err := redis.EnsureIndexes(ctx); err != nil {
		_ = redis.Close()
		return nil, fmt.Errorf("redis indexes: %w", err)
	}
	postgres, err := NewPostgresStore(ctx, cfg.PostgresDSN)
	if err != nil {
		_ = redis.Close()
		return nil, fmt.Errorf("postgres open: %w", err)
	}
	return NewStore(redis, postgres), nil
}
