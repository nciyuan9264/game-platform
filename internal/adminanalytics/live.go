package adminanalytics

import (
	"context"
	"fmt"
	"sort"
)

func (s *Service) Live(ctx context.Context) (Live, error) {
	generatedAt := s.now().UTC()
	rows, err := s.gameDB.QueryContext(ctx, `
		SELECT m.runtime_room_id, m.match_id, m.game_type, m.status,
			COALESCE(player_counts.human_players, 0),
			COALESCE(player_counts.online_players, 0),
			m.started_at
		FROM matches m
		JOIN rooms r ON r.room_id = m.runtime_room_id AND r.current_match_id = m.match_id
		LEFT JOIN LATERAL (
			SELECT COUNT(*) AS human_players,
				COUNT(*) FILTER (WHERE online) AS online_players
			FROM match_players
			WHERE match_id = m.match_id AND NOT is_ai AND user_id <> ''
		) player_counts ON TRUE
		WHERE m.status IN ('preparing', 'running', 'ending') AND r.status <> 'closed'
		ORDER BY m.started_at DESC, m.match_id`)
	if err != nil {
		return Live{}, fmt.Errorf("query live analytics: %w", err)
	}
	defer rows.Close()
	result := Live{GeneratedAt: generatedAt, Rooms: []LiveRoom{}}
	byGame := make(map[string]LiveGame)
	for rows.Next() {
		var room LiveRoom
		if err := rows.Scan(
			&room.RoomID,
			&room.MatchID,
			&room.GameType,
			&room.Status,
			&room.HumanPlayers,
			&room.OnlinePlayers,
			&room.StartedAt,
		); err != nil {
			return Live{}, err
		}
		result.Rooms = append(result.Rooms, room)
		result.ActiveRooms++
		result.ActiveMatches++
		result.OnlinePlayers += room.OnlinePlayers
		game := byGame[room.GameType]
		game.GameType = room.GameType
		game.ActiveRooms++
		game.ActiveMatches++
		game.OnlinePlayers += room.OnlinePlayers
		byGame[room.GameType] = game
	}
	if err := rows.Err(); err != nil {
		return Live{}, err
	}
	result.Games = make([]LiveGame, 0, len(byGame))
	for _, game := range byGame {
		result.Games = append(result.Games, game)
	}
	sort.Slice(result.Games, func(left, right int) bool {
		if result.Games[left].OnlinePlayers == result.Games[right].OnlinePlayers {
			return result.Games[left].GameType < result.Games[right].GameType
		}
		return result.Games[left].OnlinePlayers > result.Games[right].OnlinePlayers
	})
	return result, nil
}
