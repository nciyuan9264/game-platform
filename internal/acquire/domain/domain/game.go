package domain

import (
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type RealConn struct {
	*websocket.Conn
	PingInterval time.Duration
	LastPongTime time.Time
	Done         chan struct{}
	closeOnce    sync.Once
}

func (r *RealConn) WriteMessage(messageType int, data []byte) error {
	return r.Conn.WriteMessage(messageType, data)
}

func (r *RealConn) Close() error {
	r.closeOnce.Do(func() {
		close(r.Done)
		r.Conn.Close()
	})
	return nil
}

func (r *RealConn) StartHeartbeat() {
	go func() {
		ticker := time.NewTicker(r.PingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if time.Since(r.LastPongTime) > r.PingInterval*2 {
					r.Close()
					return
				}
				r.WriteMessage(websocket.PingMessage, []byte{})
				log.Println("✅ 发送心跳包")
			case <-r.Done:
				return
			}
		}
	}()

	r.Conn.SetPongHandler(func(string) error {
		r.LastPongTime = time.Now()
		return nil
	})
}

func NewRealConn(conn *websocket.Conn) *RealConn {
	return &RealConn{
		Conn:         conn,
		PingInterval: 30 * time.Second,
		LastPongTime: time.Now(),
		Done:         make(chan struct{}),
	}
}

// 玩家连接对象结构体
type RoomStatus string

const (
	RoomStatusMatch            RoomStatus = "match"            // 初始化房间状态
	RoomStatusWaiting          RoomStatus = "waiting"          // 等待玩家加入房间
	RoomStatusSetTile          RoomStatus = "setTile"          // 等待玩家放置Tile
	RoomStatusCreateCompany    RoomStatus = "createCompany"    // 等待玩家创建公司
	RoomStatusBuyStock         RoomStatus = "buyStock"         // 等待玩家购买股票
	RoomStatusMerging          RoomStatus = "merging"          // 等待玩家选择并购公司
	RoomStatusMergingSelection RoomStatus = "mergingSelection" // 选择并购留下来的公司
	RoomStatusMergingSettle    RoomStatus = "mergingSettle"    // 结算并购
	RoomStatusEnd              RoomStatus = "end"
)

type SettleData struct {
	Hoders    []string       `json:"hoders"`
	Dividends map[string]int `json:"dividends"`
}

type MergingSettleItem struct {
	Company        string `json:"company"`
	SellAmount     int    `json:"sellAmount"`
	ExchangeAmount int    `json:"exchangeAmount"`
}

type Company struct {
	Name       string `json:"name"`
	StockPrice int    `json:"stockPrice"`
	StockTotal int    `json:"stockTotal"`
	Tiles      int    `json:"tiles"`
}
