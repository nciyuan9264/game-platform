package rules

import (
	"encoding/json"
	"log"
	"math/rand"
	"strconv"

	"github.com/nciyuan9264/game-platform-server/internal/splendor/domain"
	"github.com/nciyuan9264/game-platform-server/internal/splendor/entities"
)

// gemColors 是可作为普通宝石拿取的五种颜色（不含 Gold 万能币）。
var gemColors = map[string]bool{
	"Red":   true,
	"Green": true,
	"White": true,
	"Blue":  true,
	"Black": true,
}

// maxTokensInHand splendor 规则：玩家手中宝石（含 Gold）上限为 10。
const maxTokensInHand = 10

// countTokens 统计玩家手中宝石总数（含 Gold）。
func countTokens(ps *domain.PlayerState) int {
	total := 0
	for _, n := range ps.Gem {
		total += n
	}
	return total
}

// HandleGetGemMessage atomically applies a validated take-and-return action.
func HandleGetGemMessage(r *domain.Room, cmd domain.Command) {
	validationCmd := cmd
	validationCmd.Type = "game_get_gem"
	if err := ValidateAction(r, validationCmd); err != nil {
		log.Printf("❌ 拿取宝石失败 room=%s player=%s code=%s err=%s", r.ID, cmd.PlayerID, err.Code, err.Message)
		return
	}
	payload, err := parseGemTakePayload(cmd.Payload)
	if err != nil {
		log.Printf("❌ get_gem payload 解析失败 room=%s player=%s", r.ID, cmd.PlayerID)
		return
	}
	requested := normalizedPositiveCounts(payload.Take)
	returned := normalizedPositiveCounts(payload.Returns)

	if len(requested) == 0 {
		r.State.LastData = &domain.LastAction{
			Action:   "get_gem",
			PlayerID: cmd.PlayerID,
			Payload:  marshalGemTakeLastData(payload, requested, returned),
		}
		AdvanceTurn(r)
		return
	}

	playerState := r.State.Players[cmd.PlayerID]
	for color, num := range requested {
		r.State.Gems[color] -= num
		playerState.Gem[color] += num
	}
	for color, num := range returned {
		playerState.Gem[color] -= num
		r.State.Gems[color] += num
	}

	r.State.LastData = &domain.LastAction{
		Action:   "get_gem",
		PlayerID: cmd.PlayerID,
		Payload:  marshalGemTakeLastData(payload, requested, returned),
	}

	AdvanceTurn(r)
}

func marshalGemTakeLastData(
	original GemTakePayload,
	take map[string]int,
	returns map[string]int,
) json.RawMessage {
	var value interface{} = take
	if !original.Legacy {
		value = GemTakePayload{Take: take, Returns: returns}
	}
	data, _ := json.Marshal(value)
	return data
}

// HandleBuyCardMessage 处理购买发展卡（来自桌面明牌或玩家自己的保留卡）。
func HandleBuyCardMessage(r *domain.Room, cmd domain.Command) {
	if !isTurnPlayable(r) {
		log.Printf("❌ 当前状态不可出手 room=%s status=%s", r.ID, r.State.RoomStatus)
		return
	}
	if cmd.PlayerID != r.State.CurrentPlayer {
		log.Printf("❌ 非当前玩家回合 room=%s player=%s", r.ID, cmd.PlayerID)
		return
	}
	var cardID int
	if err := json.Unmarshal(cmd.Payload, &cardID); err != nil {
		log.Printf("❌ buy_card payload 解析失败 room=%s err=%v", r.ID, err)
		return
	}

	playerState, ok := r.State.Players[cmd.PlayerID]
	if !ok || playerState == nil {
		log.Printf("❌ 玩家不存在 room=%s player=%s", r.ID, cmd.PlayerID)
		return
	}

	// 定位卡牌：优先从玩家自己的保留卡中找；否则必须是桌面上的明牌。
	var card *entities.NormalCard
	fromReserve := false
	reserveIdx := -1
	for i := range playerState.ReserveCard {
		if playerState.ReserveCard[i].ID == cardID {
			card = &playerState.ReserveCard[i]
			fromReserve = true
			reserveIdx = i
			break
		}
	}
	if !fromReserve {
		c, exists := r.State.NormalCards[strconv.Itoa(cardID)]
		if !exists || c == nil {
			log.Printf("❌ 卡牌不存在 room=%s card=%d", r.ID, cardID)
			return
		}
		if c.State != entities.CardStateRevealed {
			log.Printf("❌ 卡牌不可购买（非桌面明牌）room=%s card=%d state=%d", r.ID, cardID, c.State)
			return
		}
		card = c
	}

	// 计算支付：先用已有发展卡的折扣，再用宝石，最后用 Gold 补足。
	cardCount := make(map[string]int)
	for _, c := range playerState.NormalCard {
		cardCount[c.Bonus]++
	}

	paidGems := make(map[string]int)
	remainingGold := playerState.Gem["Gold"]
	canBuy := true
	for color, cost := range card.Cost {
		discounted := cost - cardCount[color]
		if discounted <= 0 {
			continue
		}
		if playerState.Gem[color] >= discounted {
			paidGems[color] += discounted
		} else {
			needGold := discounted - playerState.Gem[color]
			if remainingGold >= needGold {
				paidGems[color] += playerState.Gem[color]
				paidGems["Gold"] += needGold
				remainingGold -= needGold
			} else {
				canBuy = false
				break
			}
		}
	}
	if !canBuy {
		log.Printf("❌ 玩家宝石不足 room=%s player=%s card=%d", r.ID, cmd.PlayerID, cardID)
		return
	}
	for color, amt := range paidGems {
		playerState.Gem[color] -= amt
		r.State.Gems[color] += amt
	}

	// 加入玩家发展卡区。
	bought := *card
	bought.State = entities.CardStateBought
	playerState.NormalCard = append(playerState.NormalCard, bought)

	if fromReserve {
		// 从保留卡中移除（保留时桌面已补过牌，无需再补）。
		playerState.ReserveCard = append(playerState.ReserveCard[:reserveIdx], playerState.ReserveCard[reserveIdx+1:]...)
	} else {
		// 桌面买牌：翻一张同 Level 的隐藏卡补位。
		revealReplacement(r, card.Level)
		card.State = entities.CardStateBought
	}

	settleNoble(r, playerState)

	payloadJSON, _ := json.Marshal(bought)
	r.State.LastData = &domain.LastAction{
		Action:   "buy_card",
		PlayerID: cmd.PlayerID,
		Payload:  payloadJSON,
	}

	// 权威重算分数。
	RecomputeDerivedState(r)

	// 统一收尾：终局判定（进入最后一轮 / 回合闭环时结束）+ 切换下一位玩家。
	AdvanceTurn(r)
}

// revealReplacement 翻开一张指定 Level 的隐藏卡到桌面。
func revealReplacement(r *domain.Room, level int) {
	hiddenIDs := make([]string, 0)
	for k, c := range r.State.NormalCards {
		if c.State == entities.CardStateHidden && c.Level == level {
			hiddenIDs = append(hiddenIDs, k)
		}
	}
	if len(hiddenIDs) > 0 {
		pick := hiddenIDs[rand.Intn(len(hiddenIDs))]
		r.State.NormalCards[pick].State = entities.CardStateRevealed
	}
}

// settleNoble 贵族结算：每回合最多迎接一位贵族（满足条件的取 ID 最小者）。
func settleNoble(r *domain.Room, playerState *domain.PlayerState) {
	bonusCount := make(map[string]int)
	for _, c := range playerState.NormalCard {
		bonusCount[c.Bonus]++
	}

	bestID := ""
	var bestNoble *entities.NobleCard
	for nobleID, n := range r.State.NobleCards {
		if n.State != entities.CardStateRevealed {
			continue
		}
		qualified := true
		for color, req := range n.Cost {
			if bonusCount[color] < req {
				qualified = false
				break
			}
		}
		if !qualified {
			continue
		}
		if bestID == "" || nobleID < bestID {
			bestID = nobleID
			bestNoble = n
		}
	}
	if bestNoble != nil {
		bestNoble.State = entities.CardStateBought
		playerState.NobleCard = append(playerState.NobleCard, *bestNoble)
	}
}

// HandleReserveCardMessage atomically reserves a public card, grants Gold,
// and applies any required token returns.
func HandleReserveCardMessage(r *domain.Room, cmd domain.Command) {
	validationCmd := cmd
	validationCmd.Type = "game_preserve_card"
	if err := ValidateAction(r, validationCmd); err != nil {
		log.Printf("❌ 预留发展卡失败 room=%s player=%s code=%s err=%s", r.ID, cmd.PlayerID, err.Code, err.Message)
		return
	}
	payload, err := parseReserveCardPayload(cmd.Payload)
	if err != nil {
		log.Printf("❌ preserve_card payload 解析失败 room=%s player=%s", r.ID, cmd.PlayerID)
		return
	}
	card := r.State.NormalCards[strconv.Itoa(payload.CardID)]
	playerState := r.State.Players[cmd.PlayerID]
	returned := normalizedPositiveCounts(payload.Returns)

	grantGold := r.State.Gems["Gold"] > 0 &&
		(!payload.Legacy || countTokens(playerState) < maxTokensInHand)
	if grantGold {
		r.State.Gems["Gold"]--
		playerState.Gem["Gold"]++
	}
	for color, num := range returned {
		playerState.Gem[color] -= num
		r.State.Gems[color] += num
	}

	playerState.ReserveCard = append(playerState.ReserveCard, entities.NormalCard{
		ID:     card.ID,
		Level:  card.Level,
		Bonus:  card.Bonus,
		Points: card.Points,
		Cost:   card.Cost,
		State:  entities.CardStateBought,
	})

	// 桌面补一张同 Level 的隐藏卡，并把原卡标记为已离开桌面。
	revealReplacement(r, card.Level)
	card.State = entities.CardStateBought

	var lastPayload interface{} = card
	if !payload.Legacy {
		lastPayload = struct {
			Card    *entities.NormalCard `json:"card"`
			Returns map[string]int       `json:"returns,omitempty"`
		}{
			Card:    card,
			Returns: returned,
		}
	}
	payloadJSON, _ := json.Marshal(lastPayload)
	r.State.LastData = &domain.LastAction{
		Action:   "preserve_card",
		PlayerID: cmd.PlayerID,
		Payload:  payloadJSON,
	}

	AdvanceTurn(r)
}
