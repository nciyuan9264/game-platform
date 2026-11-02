package adminanalytics

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/lib/pq"
)

func (s *Service) Live(ctx context.Context) (Live, error) {
	generatedAt := s.now().UTC()
	rows, err := s.gameDB.QueryContext(ctx, `
		SELECT m.runtime_room_id, m.match_id, m.game_type, m.status,
			m.owner_user_id, m.started_at
		FROM matches m
		JOIN rooms r ON r.room_id = m.runtime_room_id AND r.current_match_id = m.match_id
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
			&room.OwnerUserID,
			&room.StartedAt,
		); err != nil {
			return Live{}, err
		}
		result.Rooms = append(result.Rooms, room)
	}
	if err := rows.Err(); err != nil {
		return Live{}, err
	}
	if err := rows.Close(); err != nil {
		return Live{}, err
	}
	if err := s.populateLivePlayers(ctx, result.Rooms); err != nil {
		return Live{}, fmt.Errorf("load live players: %w", err)
	}
	for index := range result.Rooms {
		room := &result.Rooms[index]
		room.HumanPlayers = int64(len(room.Players))
		for _, player := range room.Players {
			if player.Online {
				room.OnlinePlayers++
			}
		}
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

func (s *Service) populateLivePlayers(ctx context.Context, rooms []LiveRoom) error {
	if len(rooms) == 0 {
		return nil
	}
	matchIDs := make([]string, 0, len(rooms))
	roomByMatchID := make(map[string]int, len(rooms))
	for index := range rooms {
		rooms[index].Players = []LivePlayer{}
		matchIDs = append(matchIDs, rooms[index].MatchID)
		roomByMatchID[rooms[index].MatchID] = index
	}
	rows, err := s.gameDB.QueryContext(ctx, `
		SELECT match_id, user_id, display_name, name, avatar, online
		FROM match_players
		WHERE match_id = ANY($1) AND NOT is_ai AND user_id <> ''
		ORDER BY match_id, seat_index, joined_at, player_id`, pq.Array(matchIDs))
	if err != nil {
		return err
	}
	for rows.Next() {
		var matchID, userID, displayName, name, avatar string
		var online bool
		if err := rows.Scan(&matchID, &userID, &displayName, &name, &avatar, &online); err != nil {
			rows.Close()
			return err
		}
		index, ok := roomByMatchID[matchID]
		if !ok {
			continue
		}
		if displayName == "" {
			displayName = name
		}
		rooms[index].Players = append(rooms[index].Players, LivePlayer{
			UserID:      userID,
			DisplayName: displayName,
			Avatar:      avatar,
			Online:      online,
			IsOwner:     userID == rooms[index].OwnerUserID,
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for index := range rooms {
		ownerPresent := false
		for _, player := range rooms[index].Players {
			if player.UserID == rooms[index].OwnerUserID {
				ownerPresent = true
				break
			}
		}
		if !ownerPresent && rooms[index].OwnerUserID != "" {
			rooms[index].Players = append([]LivePlayer{{
				UserID:  rooms[index].OwnerUserID,
				IsOwner: true,
			}}, rooms[index].Players...)
		}
	}
	profiles, err := s.loadLivePlayerProfiles(ctx, rooms)
	if err != nil {
		return err
	}
	for roomIndex := range rooms {
		for playerIndex := range rooms[roomIndex].Players {
			player := &rooms[roomIndex].Players[playerIndex]
			if profile, ok := profiles[player.UserID]; ok {
				player.DisplayName = profile.DisplayName
				if profile.Avatar != "" {
					player.Avatar = profile.Avatar
				}
			}
			if player.DisplayName == "" {
				player.DisplayName = "用户 " + player.UserID
			}
		}
	}
	return nil
}

func (s *Service) loadLivePlayerProfiles(ctx context.Context, rooms []LiveRoom) (map[string]User, error) {
	userIDs := make([]int64, 0)
	seen := make(map[int64]struct{})
	for _, room := range rooms {
		for _, player := range room.Players {
			userID, err := strconv.ParseInt(player.UserID, 10, 64)
			if err != nil || userID <= 0 {
				continue
			}
			if _, ok := seen[userID]; ok {
				continue
			}
			seen[userID] = struct{}{}
			userIDs = append(userIDs, userID)
		}
	}
	result := make(map[string]User, len(userIDs))
	if len(userIDs) == 0 {
		return result, nil
	}
	rows, err := s.authDB.QueryContext(ctx, `
		SELECT id,
			COALESCE(NULLIF(btrim(name), ''), NULLIF(split_part(email, '@', 1), ''), '用户 ' || id::text),
			COALESCE(avatar, '')
		FROM public.users
		WHERE id = ANY($1) AND deleted_at IS NULL`, pq.Array(userIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var profile User
		if err := rows.Scan(&profile.ID, &profile.DisplayName, &profile.Avatar); err != nil {
			return nil, err
		}
		result[strconv.FormatInt(profile.ID, 10)] = profile
	}
	return result, rows.Err()
}
