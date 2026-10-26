package rpc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	platform "github.com/nciyuan9264/game-platform-client/kitex_gen/pam/game/platform/v1"
	"github.com/nciyuan9264/game-platform/internal/contracts"
	"github.com/nciyuan9264/game-platform/internal/history"
)

const (
	defaultHistoryLimit     = 20
	defaultReplayLimit      = 20
	defaultLeaderboardLimit = 50
	maxPageLimit            = 200
)

func (h *Handler) ListHistoryGames(ctx context.Context, req *platform.ListHistoryGamesReq) (*platform.HistoryListResp, error) {
	userID, err := numericUserID(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, platformError("invalid_request", errors.New("request is required"))
	}
	gameType, err := h.validGameType(ctx, req.GetGameType())
	if err != nil {
		return nil, err
	}
	limit, offset, err := pageParams(req.Limit, req.Offset, defaultHistoryLimit)
	if err != nil {
		return nil, err
	}
	games, err := h.store.ListHistoryGames(ctx, userID, gameType, limit, offset)
	if err != nil {
		return nil, platformError("history_error", err)
	}
	h.resolveHistoryGames(ctx, games)
	total, err := h.store.CountHistoryGames(ctx, userID, gameType)
	if err != nil {
		return nil, platformError("history_error", err)
	}
	items := make([]*platform.HistoryGame, 0, len(games))
	for _, value := range games {
		items = append(items, mapHistoryGame(value))
	}
	return &platform.HistoryListResp{
		StatusCode: http.StatusOK,
		Message:    "ok",
		Data: &platform.HistoryListData{
			Games: items,
			Page:  pageInfo(limit, offset, total),
		},
	}, nil
}

func (h *Handler) GetHistoryDetail(ctx context.Context, req *platform.GetHistoryDetailReq) (*platform.HistoryDetailResp, error) {
	userID, err := numericUserID(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil || req.GetId() <= 0 {
		return nil, platformError("invalid_request", errors.New("positive history id is required"))
	}
	gameType, err := h.validGameType(ctx, req.GetGameType())
	if err != nil {
		return nil, err
	}
	gameRecord, players, events, err := h.store.GetCompletedHistoryGameForUser(
		ctx,
		req.GetId(),
		userID,
		gameType,
		false,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, platformError("not_found", errors.New("history game not found"))
	}
	if err != nil {
		return nil, platformError("history_error", err)
	}
	h.resolveHistoryPlayers(ctx, players)
	displayNames := historyDisplayNames(players)
	metas := make([]*platform.HistoryEventMeta, 0, len(events))
	for _, event := range events {
		event.DisplayName = displayNames[event.PlayerID]
		metas = append(metas, &platform.HistoryEventMeta{
			Seq:         int32(event.Seq),
			PlayerID:    event.PlayerID,
			CmdType:     event.CommandType,
			DisplayName: stringPointer(event.DisplayName),
		})
	}
	return &platform.HistoryDetailResp{
		StatusCode: http.StatusOK,
		Message:    "ok",
		Data: &platform.HistoryDetailData{
			Game:    mapHistoryGame(gameRecord),
			Players: mapHistoryPlayers(players),
			Events:  metas,
		},
	}, nil
}

func (h *Handler) GetSnapshot(ctx context.Context, req *platform.GetSnapshotReq) (*platform.SnapshotResp, error) {
	userID, err := numericUserID(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil || req.GetId() <= 0 || req.GetSeq() < -1 {
		return nil, platformError("invalid_request", errors.New("valid history id and seq are required"))
	}
	gameType, err := h.replayGameType(ctx, req.GetGameType())
	if err != nil {
		return nil, err
	}
	eventOffset, eventLimit := 0, 0
	if req.GetSeq() >= 0 {
		eventOffset = int(req.GetSeq())
		eventLimit = 1
	}
	gameRecord, players, totalEvents, events, err := h.store.HistoryReplayPageForUser(
		ctx,
		req.GetId(),
		userID,
		gameType,
		eventOffset,
		eventLimit,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, platformError("not_found", errors.New("history game not found"))
	}
	if err != nil {
		return nil, platformError("history_error", err)
	}
	h.resolveHistoryPlayers(ctx, players)
	var event *history.Event
	if req.GetSeq() >= 0 {
		if len(events) != 1 || events[0].Seq != int(req.GetSeq()) {
			return nil, platformError("not_found", errors.New("replay snapshot not found"))
		}
		event = &events[0]
	}
	snapshot, err := h.buildReplaySnapshot(ctx, gameRecord, players, event, int(req.GetSeq()), totalEvents)
	if err != nil {
		return nil, err
	}
	return &platform.SnapshotResp{StatusCode: http.StatusOK, Message: "ok", Data: snapshot}, nil
}

func (h *Handler) ListSnapshots(ctx context.Context, req *platform.ListSnapshotsReq) (*platform.SnapshotListResp, error) {
	userID, err := numericUserID(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil || req.GetId() <= 0 {
		return nil, platformError("invalid_request", errors.New("positive history id is required"))
	}
	gameType, err := h.replayGameType(ctx, req.GetGameType())
	if err != nil {
		return nil, err
	}
	limit, offset, err := pageParams(req.Limit, req.Offset, defaultReplayLimit)
	if err != nil {
		return nil, err
	}
	eventOffset := offset - 1
	eventLimit := limit
	includeInitial := offset == 0
	if includeInitial {
		eventOffset = 0
		eventLimit = limit - 1
	}
	gameRecord, players, totalEvents, events, err := h.store.HistoryReplayPageForUser(
		ctx,
		req.GetId(),
		userID,
		gameType,
		eventOffset,
		eventLimit,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, platformError("not_found", errors.New("history game not found"))
	}
	if err != nil {
		return nil, platformError("history_error", err)
	}
	h.resolveHistoryPlayers(ctx, players)
	snapshots := make([]*platform.ReplaySnapshot, 0, len(events)+1)
	if includeInitial {
		initial, err := h.buildReplaySnapshot(ctx, gameRecord, players, nil, -1, totalEvents)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, initial)
	}
	for index := range events {
		event := &events[index]
		snapshot, err := h.buildReplaySnapshot(ctx, gameRecord, players, event, event.Seq, totalEvents)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	totalSnapshots := totalEvents + 1
	return &platform.SnapshotListResp{
		StatusCode: http.StatusOK,
		Message:    "ok",
		Data: &platform.SnapshotListData{
			Snapshots:      snapshots,
			TotalEvents:    int32(totalEvents),
			TotalSnapshots: int32(totalSnapshots),
			Page:           pageInfo(limit, offset, totalSnapshots),
		},
	}, nil
}

func (h *Handler) GetStats(ctx context.Context, req *platform.GetStatsReq) (*platform.StatsResp, error) {
	userID, err := numericUserID(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, platformError("invalid_request", errors.New("request is required"))
	}
	gameType, err := h.validGameType(ctx, req.GetGameType())
	if err != nil {
		return nil, err
	}
	stats, err := h.store.HistoryStats(ctx, userID, gameType)
	if err != nil {
		return nil, platformError("history_error", err)
	}
	gamesByRole := make(map[string]int32, len(stats.GamesByRole))
	for role, total := range stats.GamesByRole {
		gamesByRole[role] = int32(total)
	}
	winsByRole := make(map[string]int32, len(stats.WinsByRole))
	for role, total := range stats.WinsByRole {
		winsByRole[role] = int32(total)
	}
	return &platform.StatsResp{
		StatusCode: http.StatusOK,
		Message:    "ok",
		Data: &platform.HistoryStats{
			TotalGames:  int32(stats.TotalGames),
			Wins:        int32(stats.Wins),
			WinRate:     stats.WinRate,
			AvgScore:    stats.AvgScore,
			GamesByRole: gamesByRole,
			WinsByRole:  winsByRole,
		},
	}, nil
}

func (h *Handler) GetLeaderboard(ctx context.Context, req *platform.GetLeaderboardReq) (*platform.LeaderboardResp, error) {
	if req == nil {
		return nil, platformError("invalid_request", errors.New("request is required"))
	}
	gameType, err := h.validGameType(ctx, req.GetGameType())
	if err != nil {
		return nil, err
	}
	sortBy := req.GetSort()
	if sortBy == "" {
		sortBy = "metric"
	}
	if sortBy != "metric" && sortBy != "totalGames" {
		return nil, platformError("invalid_sort", errors.New("sort must be metric or totalGames"))
	}
	gameRole := req.GetGameRole()
	if gameRole != "" {
		if gameType != contracts.GameFugitive ||
			(gameRole != "fugitive" && gameRole != "marshal") {
			return nil, platformError(
				"invalid_request",
				errors.New("gameRole must be fugitive or marshal"),
			)
		}
	}
	limit, offset, err := pageParams(req.Limit, req.Offset, defaultLeaderboardLimit)
	if err != nil {
		return nil, err
	}
	entries, err := h.store.HistoryLeaderboardSortedByRole(
		ctx,
		gameType,
		sortBy,
		gameRole,
		limit,
		offset,
	)
	if err != nil {
		return nil, platformError("history_error", err)
	}
	h.resolveLeaderboard(ctx, entries)
	total, err := h.store.CountLeaderboardEntriesByRole(
		ctx,
		gameType,
		gameRole,
	)
	if err != nil {
		return nil, platformError("history_error", err)
	}
	mapped := make([]*platform.LeaderboardEntry, 0, len(entries))
	for _, entry := range entries {
		metric := &platform.LeaderboardMetric{}
		if gameType == contracts.GameDaVinci {
			metric.Davinci = &platform.DavinciLeaderboardMetric{
				Wins:    int32(valueOrZero(entry.Wins)),
				WinRate: floatValueOrZero(entry.WinRate),
			}
		} else if gameType == contracts.GameYinsh {
			metric.Yinsh = &platform.YinshLeaderboardMetric{
				Wins:    int32(valueOrZero(entry.Wins)),
				WinRate: floatValueOrZero(entry.WinRate),
			}
		} else if gameType == contracts.GameOthello {
			metric.Othello = &platform.OthelloLeaderboardMetric{
				Wins:    int32(valueOrZero(entry.Wins)),
				WinRate: floatValueOrZero(entry.WinRate),
			}
		} else if gameType == contracts.GameFugitive {
			metric.Fugitive = &platform.FugitiveLeaderboardMetric{
				Wins:    int32(valueOrZero(entry.Wins)),
				WinRate: floatValueOrZero(entry.WinRate),
			}
		} else if gameType == contracts.GameWarChest {
			metric.Warchest = &platform.WarChestLeaderboardMetric{
				Wins:    int32(valueOrZero(entry.Wins)),
				WinRate: floatValueOrZero(entry.WinRate),
			}
		} else {
			metric.Rank = &platform.RankLeaderboardMetric{AvgRank: floatValueOrZero(entry.AvgRank)}
		}
		mapped = append(mapped, &platform.LeaderboardEntry{
			UserID:      entry.UserID,
			PlayerID:    entry.PlayerID,
			TotalGames:  int32(entry.TotalGames),
			Metric:      metric,
			DisplayName: stringPointer(entry.DisplayName),
		})
	}
	return &platform.LeaderboardResp{
		StatusCode: http.StatusOK,
		Message:    "ok",
		Data: &platform.LeaderboardData{
			GameType: platform.GameType(gameType),
			Entries:  mapped,
			Page:     pageInfo(limit, offset, total),
		},
	}, nil
}

func (h *Handler) replayGameType(ctx context.Context, value string) (string, error) {
	gameType, err := h.validGameType(ctx, value)
	if err != nil {
		return "", err
	}
	if gameType == contracts.GameDaVinci ||
		gameType == contracts.GameFugitive {
		return "", platformError("replay_not_supported", errors.New("davinci replay is not supported"))
	}
	if gameType != contracts.GameAcquire &&
		gameType != contracts.GameSplendor &&
		gameType != contracts.GameYinsh &&
		gameType != contracts.GameOthello &&
		gameType != contracts.GameWarChest {
		return "", platformError("replay_not_supported", errors.New("game replay is not supported"))
	}
	return gameType, nil
}

func (h *Handler) buildReplaySnapshot(
	ctx context.Context,
	gameRecord history.Game,
	players []history.Player,
	event *history.Event,
	seq,
	total int,
) (*platform.ReplaySnapshot, error) {
	_ = ctx
	if event != nil {
		event.DisplayName = historyDisplayNames(players)[event.PlayerID]
	}
	payload := gameRecord.InitialReplayPayload
	if event != nil {
		payload = event.ReplayPayload
	}
	encoded, err := history.MaterializedReplayJSON(
		gameRecord.ReplaySchemaVersion,
		payload,
		seq,
		total,
		event,
		players,
	)
	if err != nil {
		return nil, replayMaterializationError(err)
	}
	result := &platform.ReplaySnapshot{}
	switch gameRecord.GameType {
	case contracts.GameAcquire:
		result.Acquire = &platform.AcquireReplaySnapshot{}
		if err := json.Unmarshal(encoded, result.Acquire); err != nil {
			return nil, platformError("replay_error", err)
		}
	case "splendor":
		result.Splendor = &platform.SplendorReplaySnapshot{}
		if err := json.Unmarshal(encoded, result.Splendor); err != nil {
			return nil, platformError("replay_error", err)
		}
	case contracts.GameYinsh:
		result.Yinsh = &platform.YinshReplaySnapshot{}
		if err := json.Unmarshal(encoded, result.Yinsh); err != nil {
			return nil, platformError("replay_error", err)
		}
	case contracts.GameOthello:
		result.Othello = &platform.OthelloReplaySnapshot{}
		if err := json.Unmarshal(encoded, result.Othello); err != nil {
			return nil, platformError("replay_error", err)
		}
	case contracts.GameWarChest:
		result.Warchest = &platform.WarChestReplaySnapshot{}
		if err := json.Unmarshal(encoded, result.Warchest); err != nil {
			return nil, platformError("replay_error", err)
		}
	default:
		return nil, platformError("replay_not_supported", errors.New("game replay is not supported"))
	}
	return result, nil
}

func replayMaterializationError(err error) error {
	switch {
	case errors.Is(err, history.ErrReplayMaterializationMissing):
		return platformError("replay_materialization_missing", err)
	case errors.Is(err, history.ErrReplaySchemaUnsupported):
		return platformError("replay_schema_unsupported", err)
	default:
		return platformError("replay_error", err)
	}
}

func numericUserID(ctx context.Context) (int64, error) {
	value, err := requireIdentity(ctx)
	if err != nil {
		return 0, err
	}
	userID, parseErr := strconv.ParseInt(value.UserID, 10, 64)
	if parseErr != nil || userID <= 0 {
		return 0, platformError("unauthorized", errors.New("invalid authenticated user id"))
	}
	return userID, nil
}

func pageParams(limitValue, offsetValue *int32, fallback int) (int, int, error) {
	limit := fallback
	if limitValue != nil {
		limit = int(*limitValue)
	}
	offset := 0
	if offsetValue != nil {
		offset = int(*offsetValue)
	}
	if limit <= 0 || limit > maxPageLimit || offset < 0 {
		return 0, 0, platformError("invalid_pagination", errors.New("limit must be 1..200 and offset must be non-negative"))
	}
	return limit, offset, nil
}

func pageInfo(limit, offset, total int) *platform.PageInfo {
	next := offset + limit
	hasMore := next < total
	result := &platform.PageInfo{
		Limit:   int32(limit),
		Offset:  int32(offset),
		HasMore: hasMore,
		Total:   int32(total),
	}
	if hasMore {
		value := int32(next)
		result.NextOffset = &value
	}
	return result
}

func valueOrZero(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func floatValueOrZero(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}
