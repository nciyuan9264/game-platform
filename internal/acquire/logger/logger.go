package logger

import (
	"encoding/json"
	"log"

	"github.com/nciyuan9264/game-platform-server/internal/acquire/timeutil"
)

// Field 日志字段类型
type Field struct {
	Key   string
	Value any
}

// F 创建日志字段的辅助函数
func F(key string, value any) Field {
	return Field{Key: key, Value: value}
}

// Logger 结构化日志记录器
type Logger struct {
	Module string
}

// NewLogger 创建新的日志记录器
func NewLogger(module string) *Logger {
	return &Logger{Module: module}
}

// 预定义的模块日志记录器
var (
	DefaultLogger = NewLogger("app")
	RoomLogger    = NewLogger("room")
	AILogger      = NewLogger("ai")
)

// log 核心日志记录方法
func (l *Logger) log(level, msg string, fields ...Field) {
	ts := timeutil.Now().Format("2006-01-02 15:04:05.000")

	m := map[string]any{
		"time":   ts,
		"level":  level,
		"module": l.Module,
		"msg":    msg,
	}

	for _, f := range fields {
		m[f.Key] = f.Value
	}

	b, _ := json.Marshal(m)
	log.Println(string(b))
}

// Info 记录信息级别的日志
func (l *Logger) Info(msg string, fields ...Field) {
	l.log("INFO", msg, fields...)
}

// Warn 记录警告级别的日志
func (l *Logger) Warn(msg string, fields ...Field) {
	l.log("WARN", msg, fields...)
}

// Error 记录错误级别的日志
func (l *Logger) Error(msg string, fields ...Field) {
	l.log("ERROR", msg, fields...)
}

// Debug 记录调试级别的日志
func (l *Logger) Debug(msg string, fields ...Field) {
	l.log("DEBUG", msg, fields...)
}

// 便捷函数，使用默认日志记录器
func Info(msg string, fields ...Field) {
	DefaultLogger.Info(msg, fields...)
}

func Warn(msg string, fields ...Field) {
	DefaultLogger.Warn(msg, fields...)
}

func Error(msg string, fields ...Field) {
	DefaultLogger.Error(msg, fields...)
}

func Debug(msg string, fields ...Field) {
	DefaultLogger.Debug(msg, fields...)
}
