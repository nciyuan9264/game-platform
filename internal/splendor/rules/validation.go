package rules

import (
	"encoding/json"
	"strconv"

	"github.com/nciyuan9264/game-platform-server/internal/splendor/domain"
	"github.com/nciyuan9264/game-platform-server/internal/splendor/entities"
)

type ActionError struct {
	Code    string
	Message string
}

func (e *ActionError) Error() string {
	return e.Message
}

func actionError(code, message string) *ActionError {
	return &ActionError{Code: code, Message: message}
}

func ValidateAction(r *domain.Room, cmd domain.Command) *ActionError {
	if r == nil || r.State == nil {
		return actionError("command_failed", "房间状态不可用")
	}
	if !isTurnPlayable(r) {
		return actionError("phase_changed", "当前阶段不能执行此操作")
	}
	if r.State.CurrentPlayer != cmd.PlayerID {
		return actionError("not_your_turn", "请等待你的回合")
	}
	player := r.State.Players[cmd.PlayerID]
	if player == nil {
		return actionError("invalid_player", "玩家数据尚未就绪")
	}

	switch cmd.Type {
	case "game_get_gem":
		payload, err := parseGemTakePayload(cmd.Payload)
		if err != nil {
			return err
		}
		if err := validateGemTakeShape(payload); err != nil {
			return err
		}
		if positiveCountTotal(payload.Take) == 0 {
			if positiveCountTotal(payload.Returns) > 0 {
				return actionError("invalid_gems", "未拿取宝石时不能退还宝石")
			}
			if CanPass(r, cmd.PlayerID) {
				return nil
			}
			return actionError("cannot_pass", "当前仍有可执行操作，不能跳过")
		}
		return validateGemTake(r, player, payload)
	case "game_buy_card":
		cardID, err := decodeCardID(cmd.Payload)
		if err != nil {
			return err
		}
		card := purchasableCard(r, player, cardID)
		if card == nil {
			return actionError("invalid_card", "这张发展卡已不可购买")
		}
		if !CanBuyCard(player, card) {
			return actionError("invalid_gems", "宝石不足，无法购买这张卡")
		}
	case "game_preserve_card":
		payload, err := parseReserveCardPayload(cmd.Payload)
		if err != nil {
			return err
		}
		card := r.State.NormalCards[strconv.Itoa(payload.CardID)]
		if card == nil || card.State != entities.CardStateRevealed {
			return actionError("invalid_card", "这张发展卡已不可预留")
		}
		if len(player.ReserveCard) >= 3 {
			return actionError("reserve_full", "最多预留三张发展卡")
		}
		return validateReserveReturns(r, player, payload)
	}
	return nil
}

func decodeCardID(payload json.RawMessage) (int, *ActionError) {
	var cardID int
	if json.Unmarshal(payload, &cardID) != nil || cardID <= 0 {
		return 0, actionError("invalid_payload", "发展卡编号无效")
	}
	return cardID, nil
}

func validateGemTake(r *domain.Room, player *domain.PlayerState, payload GemTakePayload) *ActionError {
	requested := make(map[string]int)
	total, maxPerColor := 0, 0
	for color, count := range payload.Take {
		if count == 0 {
			continue
		}
		if r.State.Gems[color] < count {
			return actionError("invalid_gems", "宝石库存已变化")
		}
		requested[color] = count
		total += count
		if count > maxPerColor {
			maxPerColor = count
		}
	}
	if total == 0 || total > 3 || maxPerColor > 2 {
		return actionError("invalid_gems", "宝石拿取数量无效")
	}
	if maxPerColor == 2 {
		if len(requested) != 1 {
			return actionError("invalid_gems", "拿两枚同色宝石时不能同时选择其他颜色")
		}
		for color := range requested {
			if r.State.Gems[color] < 4 {
				return actionError("invalid_gems", "库存至少四枚时才能拿两枚同色宝石")
			}
		}
	} else if len(requested) != total {
		return actionError("invalid_gems", "一次拿取的宝石颜色必须不同")
	}

	return validateTokenReturns(player, requested, payload.Returns)
}

func validateGemTakeShape(payload GemTakePayload) *ActionError {
	for color, count := range payload.Take {
		if count < 0 {
			return actionError("invalid_gems", "宝石数量不能为负数")
		}
		if color == "Gold" || !gemColors[color] {
			return actionError("invalid_gems", "不能主动拿取这种宝石")
		}
	}
	for color, count := range payload.Returns {
		if count < 0 {
			return actionError("invalid_returns", "退还宝石数量不能为负数")
		}
		if !isTokenColor(color) {
			return actionError("invalid_returns", "退还宝石颜色无效")
		}
	}
	return nil
}

func validateReserveReturns(
	r *domain.Room,
	player *domain.PlayerState,
	payload ReserveCardPayload,
) *ActionError {
	for color, count := range payload.Returns {
		if count < 0 {
			return actionError("invalid_returns", "退还宝石数量不能为负数")
		}
		if !isTokenColor(color) {
			return actionError("invalid_returns", "退还宝石颜色无效")
		}
	}
	additions := map[string]int{}
	if r.State.Gems["Gold"] > 0 {
		if payload.Legacy {
			if countTokens(player) < maxTokensInHand {
				additions["Gold"] = 1
			}
		} else {
			additions["Gold"] = 1
		}
	}
	return validateTokenReturns(player, additions, payload.Returns)
}

func validateTokenReturns(
	player *domain.PlayerState,
	additions map[string]int,
	returns map[string]int,
) *ActionError {
	combinedTotal := countTokens(player)
	for _, count := range additions {
		combinedTotal += count
	}

	returnTotal := 0
	for color, count := range returns {
		if count < 0 {
			return actionError("invalid_returns", "退还宝石数量不能为负数")
		}
		if !isTokenColor(color) {
			return actionError("invalid_returns", "退还宝石颜色无效")
		}
		if count == 0 {
			continue
		}
		if count > player.Gem[color]+additions[color] {
			return actionError("invalid_returns", "退还数量超过持有数量")
		}
		returnTotal += count
	}

	requiredReturns := combinedTotal - maxTokensInHand
	if requiredReturns < 0 {
		requiredReturns = 0
	}
	if returnTotal != requiredReturns {
		return actionError("invalid_returns", "退还数量必须与超出上限的数量一致")
	}
	return nil
}

func isTokenColor(color string) bool {
	return color == "Gold" || gemColors[color]
}

func positiveCountTotal(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		if count > 0 {
			total += count
		}
	}
	return total
}

func normalizedPositiveCounts(counts map[string]int) map[string]int {
	normalized := make(map[string]int)
	for color, count := range counts {
		if count > 0 {
			normalized[color] = count
		}
	}
	return normalized
}

func CanBuyCard(player *domain.PlayerState, card *entities.NormalCard) bool {
	if player == nil || card == nil {
		return false
	}
	discounts := make(map[string]int)
	for _, owned := range player.NormalCard {
		discounts[owned.Bonus]++
	}
	gold := player.Gem["Gold"]
	for color, cost := range card.Cost {
		missing := cost - discounts[color] - player.Gem[color]
		if missing <= 0 {
			continue
		}
		if gold < missing {
			return false
		}
		gold -= missing
	}
	return true
}

func CanReserveCard(r *domain.Room, player *domain.PlayerState) bool {
	if r == nil || r.State == nil || player == nil || len(player.ReserveCard) >= 3 {
		return false
	}
	for _, card := range r.State.NormalCards {
		if card != nil && card.State == entities.CardStateRevealed {
			return true
		}
	}
	return false
}

func CanTakeGems(r *domain.Room, player *domain.PlayerState) bool {
	if r == nil || r.State == nil || player == nil {
		return false
	}
	remaining := maxTokensInHand - countTokens(player)
	if remaining <= 0 {
		return false
	}
	for color := range gemColors {
		if r.State.Gems[color] > 0 {
			return true
		}
	}
	return false
}

func CanPass(r *domain.Room, playerID string) bool {
	if r == nil || r.State == nil {
		return false
	}
	player := r.State.Players[playerID]
	if player == nil || CanTakeGems(r, player) || CanReserveCard(r, player) {
		return false
	}
	for _, card := range r.State.NormalCards {
		if card != nil && card.State == entities.CardStateRevealed && CanBuyCard(player, card) {
			return false
		}
	}
	for i := range player.ReserveCard {
		if CanBuyCard(player, &player.ReserveCard[i]) {
			return false
		}
	}
	return true
}

func purchasableCard(r *domain.Room, player *domain.PlayerState, cardID int) *entities.NormalCard {
	for i := range player.ReserveCard {
		if player.ReserveCard[i].ID == cardID {
			return &player.ReserveCard[i]
		}
	}
	card := r.State.NormalCards[strconv.Itoa(cardID)]
	if card == nil || card.State != entities.CardStateRevealed {
		return nil
	}
	return card
}
