package roomcore

import (
	"encoding/json"
	"time"
)

// WriteOnlyConn 只写连接接口（适用于 AI 虚拟连接）。
type WriteOnlyConn interface {
	WriteMessage(messageType int, data []byte) error
	Close() error
}

// ReadWriteConn 读写连接接口（真实客户端连接）。
type ReadWriteConn interface {
	WriteOnlyConn
	ReadMessage() (messageType int, p []byte, err error)
}

// Command 是房间内消息命令。
type Command struct {
	Type     string          `json:"type"`
	PlayerID string          `json:"playerID"`
	Payload  json.RawMessage `json:"payload"`
	Conn     ReadWriteConn   `json:"-"`
}

// PlayerConn 描述房间中的一个玩家连接。
type PlayerConn struct {
	PlayerID string        `json:"playerID"`
	Conn     WriteOnlyConn `json:"-"`
	Online   bool          `json:"online"`
	Ready    bool          `json:"ready"`
	AI       bool          `json:"ai"`
}

// Base 是所有游戏房间共享的基础结构。各 game 的 domain.Room 通过内嵌 *Base 复用。
type Base struct {
	ID          string                 `json:"roomID"`
	Connections map[string]*PlayerConn `json:"connections"`
	PlayerSeq   []string               `json:"playerSeq"`

	CmdCh  chan Command  `json:"-"`
	QuitCh chan struct{} `json:"-"`

	// HealthTicker 周期健康检查定时器（房间级，单实例）。
	HealthTicker *time.Ticker `json:"-"`
	// NoHumanChecks 连续"无真人在线"的检查次数，达到阈值后删除房间。
	NoHumanChecks int  `json:"noHumanChecks,omitempty"`
	AIRunning     bool `json:"aiRunning,omitempty"`

	// ThinkTimer 当前回合的思考超时定时器（房间级，单实例）。
	ThinkTimer *time.Timer `json:"-"`
	// TurnDeadline 当前回合预期截止时刻；零值表示当前没有计时。
	TurnDeadline time.Time `json:"turnDeadline,omitempty"`
	// TurnTimeout 当前回合的总思考时长，供前端做进度环分母（零值表示未计时）。
	TurnTimeout time.Duration `json:"turnTimeout,omitempty"`
}

// NewBase 构造一个 Base。cmdBuf 为命令通道缓冲长度（建议 128）。
func NewBase(roomID string, cmdBuf int) *Base {
	return &Base{
		ID:          roomID,
		Connections: make(map[string]*PlayerConn),
		PlayerSeq:   []string{},
		CmdCh:       make(chan Command, cmdBuf),
		QuitCh:      make(chan struct{}),
	}
}

// StatusMatch 是 lifecycle 内部用于和 game 自身 RoomStatus 字符串比较的常量。
const StatusMatch = "match"

// HealthTickChan 返回健康检查 ticker 的通道；ticker 为 nil 时返回 nil 通道
// （nil 通道在 select 中永久阻塞，安全），避免健康检查停止后主循环 select 解引用 nil panic。
func (b *Base) HealthTickChan() <-chan time.Time {
	if b.HealthTicker == nil {
		return nil
	}
	return b.HealthTicker.C
}

// CommandTypeTurnTimeout 是 roomcore 在思考超时时投递到 CmdCh 的命令类型。
// 各 game 在 BuildTimeoutCommand 中产出真正要执行的具体命令；此常量目前只是语义占位。
const CommandTypeTurnTimeout = "turn_timeout"
