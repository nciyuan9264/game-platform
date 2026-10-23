package roompkg

import (
	"github.com/nciyuan9264/game-platform/internal/acquire/domain/domain"
	"github.com/nciyuan9264/game-platform/internal/acquire/roomcore"
)

// cloneRoomForAISimulation creates an isolated in-memory room for AI search.
// Search must never reuse live channels or connections, otherwise simulation
// could trigger virtual writes, broadcasts, or history recording side effects.
func cloneRoomForAISimulation(r *domain.Room) *domain.Room {
	if r == nil {
		return nil
	}
	base := roomcore.NewBase(r.ID+"_ai_sim", 128)
	base.PlayerSeq = append([]string(nil), r.PlayerSeq...)
	base.AIRunning = r.AIRunning
	for pid, pc := range r.Connections {
		if pc == nil {
			continue
		}
		base.Connections[pid] = &roomcore.PlayerConn{
			PlayerID:  pc.PlayerID,
			Online:    pc.Online,
			Ready:     pc.Ready,
			AI:        pc.AI,
			SeatIndex: pc.SeatIndex,
			Conn:      nil,
		}
	}
	return &domain.Room{
		Base:  base,
		State: cloneGameState(r.State),
	}
}

func cloneGameState(s *domain.GameState) *domain.GameState {
	if s == nil {
		return nil
	}
	return &domain.GameState{
		CurrentPlayer:    s.CurrentPlayer,
		GameStartTime:    s.GameStartTime,
		LastTileKey:      s.LastTileKey,
		RoomStatus:       s.RoomStatus,
		OwnerID:          s.OwnerID,
		MaxPlayers:       s.MaxPlayers,
		RandomSeed:       s.RandomSeed,
		RandomCounter:    s.RandomCounter,
		Visibility:       s.Visibility,
		BoardTiles:       cloneBoardTiles(s.BoardTiles),
		Players:          clonePlayerStateMap(s.Players),
		Companies:        cloneCompanyStateMap(s.Companies),
		MergeMainCompany: s.MergeMainCompany,
		MergingSelection: cloneMergingSelection(s.MergingSelection),
		MergeSettleData:  cloneMergeSettleData(s.MergeSettleData),
		EndGame:          cloneEndGameState(s.EndGame),
	}
}

func cloneBoardTiles(in map[string]*domain.Tile) map[string]*domain.Tile {
	if in == nil {
		return nil
	}
	out := make(map[string]*domain.Tile, len(in))
	for key, tile := range in {
		if tile == nil {
			out[key] = nil
			continue
		}
		cp := *tile
		out[key] = &cp
	}
	return out
}

func clonePlayerStateMap(in map[string]*domain.PlayerState) map[string]*domain.PlayerState {
	if in == nil {
		return nil
	}
	out := make(map[string]*domain.PlayerState, len(in))
	for pid, player := range in {
		if player == nil {
			out[pid] = nil
			continue
		}
		stocks := make(map[string]int, len(player.Stocks))
		for company, count := range player.Stocks {
			stocks[company] = count
		}
		out[pid] = &domain.PlayerState{
			Money:  player.Money,
			Stocks: stocks,
			Tiles:  append([]string(nil), player.Tiles...),
		}
	}
	return out
}

func cloneCompanyStateMap(in map[string]*domain.CompanyState) map[string]*domain.CompanyState {
	if in == nil {
		return nil
	}
	out := make(map[string]*domain.CompanyState, len(in))
	for name, company := range in {
		if company == nil {
			out[name] = nil
			continue
		}
		cp := *company
		out[name] = &cp
	}
	return out
}

func cloneMergingSelection(in domain.MergingSelection) domain.MergingSelection {
	return domain.MergingSelection{
		MainCompany:  append([]string(nil), in.MainCompany...),
		OtherCompany: append([]string(nil), in.OtherCompany...),
	}
}

func cloneMergeSettleData(in map[string]domain.SettleData) map[string]domain.SettleData {
	if in == nil {
		return nil
	}
	out := make(map[string]domain.SettleData, len(in))
	for company, data := range in {
		dividends := make(map[string]int, len(data.Dividends))
		for pid, amount := range data.Dividends {
			dividends[pid] = amount
		}
		settled := make(map[string]domain.MergingSettleResult, len(data.Settled))
		for playerID, result := range data.Settled {
			settled[playerID] = result
		}
		out[company] = domain.SettleData{
			Hoders:    append([]string(nil), data.Hoders...),
			Dividends: dividends,
			Settled:   settled,
		}
	}
	return out
}

func cloneEndGameState(in domain.EndGameState) domain.EndGameState {
	votes := make(map[string]bool, len(in.Votes))
	for playerID, allow := range in.Votes {
		votes[playerID] = allow
	}
	return domain.EndGameState{
		Eligible:  in.Eligible,
		Condition: in.Condition,
		Company:   in.Company,
		TileCount: in.TileCount,
		Votes:     votes,
	}
}
