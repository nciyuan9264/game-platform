// Package timeutil 提供全仓统一的北京时间（UTC+8）工具。
package timeutil

import "time"

// Beijing 固定东八区时区。用 FixedZone 而非 LoadLocation("Asia/Shanghai")，
// 避免依赖部署环境的 tzdata。
var Beijing = time.FixedZone("CST", 8*3600)

// Now 返回北京时间的当前时刻。
func Now() time.Time { return time.Now().In(Beijing) }
