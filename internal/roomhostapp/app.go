package roomhostapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/coordination"
	"github.com/nciyuan9264/game-platform/internal/game"
	"github.com/nciyuan9264/game-platform/internal/observability"
	"github.com/nciyuan9264/game-platform/internal/roomhost"
	"github.com/nciyuan9264/game-platform/internal/store"
	"github.com/nciyuan9264/game-platform/internal/transport"
)

func Run(definition game.Definition) error {
	if len(os.Args) == 2 && os.Args[1] == "self-test" {
		return SelfTest(context.Background(), definition)
	}
	registry, err := SingleGameRegistry(definition)
	if err != nil {
		return err
	}
	return RunRegistry(registry)
}

func SelfTest(ctx context.Context, definition game.Definition) error {
	registry, err := SingleGameRegistry(definition)
	if err != nil {
		return err
	}
	definition = registry.Definitions()[0]
	state, err := definition.Engine.InitRoom(ctx, game.RoomConfig{
		RoomID:        "roomhost-self-test",
		GameType:      definition.GameType,
		OwnerUserID:   "self-test-owner",
		OwnerPlayerID: "self-test-owner",
		MaxPlayers:    definition.DefaultMaxPlayers,
		RandomSeed:    1,
		Visibility:    contracts.VisibilityPrivate,
	})
	if err != nil {
		return fmt.Errorf("initialize game state: %w", err)
	}
	encoded, err := definition.Engine.EncodeState(ctx, state)
	if err != nil {
		return fmt.Errorf("encode game state: %w", err)
	}
	decoded, err := definition.Engine.DecodeState(ctx, encoded)
	if err != nil {
		return fmt.Errorf("decode game state: %w", err)
	}
	if decoded == nil {
		return errors.New("decoded game state is nil")
	}
	return nil
}

func SingleGameRegistry(definition game.Definition) (*game.Registry, error) {
	registry := game.NewRegistry()
	if err := registry.Register(definition); err != nil {
		return nil, err
	}
	if len(registry.Definitions()) != 1 {
		return nil, errors.New("production roomhost registry must contain exactly one game")
	}
	return registry, nil
}

// RunRegistry exists for the all-games local development entrypoint.
func RunRegistry(games *game.Registry) error {
	if games == nil || len(games.Definitions()) == 0 {
		return errors.New("roomhost requires at least one game definition")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 4)
	signal.Notify(
		signals,
		os.Interrupt,
		syscall.SIGTERM,
		syscall.SIGUSR1,
		syscall.SIGUSR2,
	)
	defer signal.Stop(signals)

	nodeID := observability.Env("NODE_ID", "roomhost-1")
	st, err := store.Open(ctx, store.Config{
		RedisAddr:     observability.Env("REDIS_ADDR", "localhost:6379"),
		RedisPassword: observability.Env("REDIS_PASSWORD", ""),
		RedisDB:       observability.EnvInt("REDIS_DB", 0),
		PostgresDSN: observability.Env(
			"POSTGRES_DSN",
			"postgres://game:game@localhost:5432/game_platform?sslmode=disable",
		),
	})
	if err != nil {
		return err
	}
	defer st.Close()

	coord, err := coordination.Open(
		ctx,
		observability.Env("ETCD_ENDPOINTS", "localhost:2379"),
	)
	if err != nil {
		return err
	}
	defer coord.Close()

	router, err := transport.NewRouter(
		observability.Env("NATS_URL", "nats://localhost:4222"),
	)
	if err != nil {
		return err
	}
	defer router.Close()
	if err := router.EnsureCommandStream(); err != nil {
		return err
	}

	host := roomhost.NewHost(nodeID, st, coord, router, games)
	if err := host.Start(ctx); err != nil {
		return err
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	heartbeat := func() {
		if err := st.UpsertNode(ctx, contracts.NodeInfo{
			NodeID:       nodeID,
			Type:         contracts.NodeTypeRoomHost,
			Addr:         observability.Env("PUBLIC_ADDR", nodeID+":9000"),
			Version:      observability.Env("SERVICE_VERSION", "dev"),
			GameVersions: games.Versions(),
			Status:       host.Status(),
			ActiveRooms:  host.ActiveRooms(),
		}, 10*time.Second); err != nil {
			observability.Warn(
				"roomhost heartbeat failed",
				"nodeID",
				nodeID,
				"error",
				err,
			)
		}
	}
	heartbeat()

	for {
		select {
		case sig := <-signals:
			switch sig {
			case syscall.SIGUSR1:
				host.Drain()
				heartbeat()
				observability.Info("roomhost draining", "nodeID", nodeID)
			case syscall.SIGUSR2:
				if host.Resume() {
					heartbeat()
					observability.Info("roomhost resumed", "nodeID", nodeID)
				}
			default:
				cancel()
				shutdownCtx, shutdownCancel := context.WithTimeout(
					context.Background(),
					10*time.Second,
				)
				defer shutdownCancel()
				if err := host.Shutdown(shutdownCtx); err != nil {
					return fmt.Errorf(
						"roomhost graceful shutdown incomplete: %w",
						err,
					)
				}
				return nil
			}
		case <-ticker.C:
			heartbeat()
		}
	}
}
