package domain

import (
	"time"

	"github.com/nciyuan9264/game-platform/internal/acquire/roomcore"
)

// 类型别名：保持外部使用 domain.Command 等不变
type WriteOnlyConn = roomcore.WriteOnlyConn
type ReadWriteConn = roomcore.ReadWriteConn
type Command = roomcore.Command
type PlayerConn = roomcore.PlayerConn

type Tile struct {
	ID     string `json:"id"`     // "1A"
	Belong string `json:"belong"` // 公司名
}

type PlayerState struct {
	Money  int            `json:"money"`
	Stocks map[string]int `json:"stocks"`
	Tiles  []string       `json:"tiles"`
}

type MergingSelection struct {
	MainCompany  []string `json:"mainCompany"`
	OtherCompany []string `json:"otherCompany"`
}
type CompanyState struct {
	Name       string `json:"name"`
	Tiles      int    `json:"tiles"`
	StockTotal int    `json:"stockTotal"`
	StockPrice int    `json:"stockPrice"`
}

const DefaultMaxPlayers = 6

type GameState struct {
	CurrentPlayer string     `json:"currentPlayer"`
	GameStartTime time.Time  `json:"gameStartTime"`
	LastTileKey   string     `json:"lastTileKey"`
	RoomStatus    RoomStatus `json:"roomStatus"`
	OwnerID       string     `json:"ownerID"`
	MaxPlayers    int        `json:"maxPlayers"`
	RandomSeed    uint64     `json:"randomSeed"`
	RandomCounter uint64     `json:"randomCounter"`

	BoardTiles map[string]*Tile         `json:"boardTiles"`
	Players    map[string]*PlayerState  `json:"players"`
	Companies  map[string]*CompanyState `json:"companies"`

	MergeMainCompany string                `json:"mergeMainCompany"`
	MergingSelection MergingSelection      `json:"mergingSelection"`
	MergeSettleData  map[string]SettleData `json:"mergeSettleData"`
}

type Room struct {
	*roomcore.Base

	State *GameState `json:"state"`
}
