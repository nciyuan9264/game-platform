# Game Platform 全链路回归

## 覆盖矩阵

| 能力 | `probe` | `canary` | `quality` |
| --- | --- | --- | --- |
| 前端、PAM Gateway health/readiness | 是 | 否 | 否 |
| 排行榜参数校验、匿名鉴权 | 是 | 否 | 否 |
| profile、房间、历史、统计接口 | 有 token 时 | 否 | 否 |
| 私有房间创建、幂等、Control 调度 | 否 | 是 | 否 |
| WebSocket、命令 ACK、重连、关闭 | 否 | 是 | 否 |
| JetStream command log | 否 | 有 NATS 连接时 | 否 |
| PostgreSQL 异步投影 | 否 | 有只读 DSN 时 | 否 |
| Go 与前端静态、单元、构建门禁 | 否 | 否 | 是 |
| Playwright 生产页面验收 | 否 | `all` 最后执行 | 否 |

DaVinci 支持 history、stats 和 leaderboard，不支持 replay；回放接口应继续返回
`replay_not_supported`。

## 运行

```bash
./scripts/run-regression.sh quality
./scripts/run-regression.sh probe
./scripts/run-regression.sh canary
./scripts/run-regression.sh all
```

也可直接运行：

```bash
go run ./cmd/regression \
  --mode probe \
  --games acquire,davinci,splendor,yinsh,othello \
  --report-dir .artifacts/regression/manual/probe
```

## 环境变量

```bash
export REGRESSION_HTTP_BASE=https://api.gamebus.online/pam-api
export REGRESSION_WS_BASE=wss://api.gamebus.online/ws
export REGRESSION_FRONTEND_URL=https://board.gamebus.online/
export REGRESSION_ORIGIN=https://board.gamebus.online
export REGRESSION_ACCESS_TOKEN=...
export REGRESSION_POSTGRES_DSN=...
export REGRESSION_NATS_URL=...
```

- `REGRESSION_ACCESS_TOKEN` 只从环境读取，不接受命令行参数。
- `REGRESSION_POSTGRES_DSN` 必须使用没有 `INSERT`、`UPDATE`、`DELETE` 权限的账号；
  执行器启动时会检查权限。
- `probe` 没有 token 时仍执行公开检查，并把鉴权检查标记为 `skipped`。
- `canary` 必须提供 token。未提供 PostgreSQL 或 NATS 连接时，对应存储检查会标记为
  `skipped`，生产验收不应接受该状态。

## Canary 行为

每款游戏创建一个 private 房间，添加一个 AI，开始游戏并提交首个真人合法动作。执行器还会验证：

1. 相同 HTTP request ID 返回相同房间。
2. 相同 request ID 配合不同请求体返回 `idempotency_conflict`。
3. 相同 command ID 和相同 payload 返回相同 seq。
4. 相同 command ID 和不同 payload 返回 `command_id_reused`。
5. `runtime_*` 命令被 Gateway 拒绝。
6. 使用 Authorization header 和 `lastSeq` 重连成功。
7. 即使中途失败，也通过公开 API best-effort 关闭房间。

生产不执行 RoomHost 停机、NATS/RDS/Redis 阻断或旧 owner 复活。故障重试由隔离测试中的
fake store/router 覆盖，避免把故障注入风险带入生产。

## 存储通过标准

JetStream：

- 首条记录是 owner fence。
- command seq 连续。
- canary command ID 只有一个物理记录，player/type/seq 与 ACK 一致。

PostgreSQL：

- room、match、ready owner 和 active engine version 一致。
- room/match seq 追平已观察的实时 seq。
- 真人 `player_id == user_id`，AI 无真人 user ID。
- command ACK 有非空 intent hash。
- seq=0 初始快照、spectator snapshot、可靠下行存在。
- runtime events 包含 room created、WebSocket connected、command applied、snapshot saved。
- operation event 和 history event 没有重复。
- Acquire、DaVinci、Splendor、YINSH 开局后都创建 open history。
- 关闭后 room 为 closed，match 为 ended/aborted，玩家离线，早退 history 被清理。

异步条件最多轮询 40 秒，覆盖两次 15 秒 projection retry，不使用固定长时间 sleep。

## 报告与排查

每次运行写入：

- `report.json`：机器可读结果。
- `report.md`：检查项、耗时、room ID、match ID 和 request ID。

默认目录是 `.artifacts/regression/<run-id>/`。报告不会写 token、Cookie、Authorization
header 或 DSN。

失败后先使用报告中的 room ID 和 match ID 查询：

```sql
SELECT * FROM rooms WHERE room_id = '<room-id>';
SELECT * FROM matches WHERE match_id = '<match-id>';
SELECT * FROM runtime_events WHERE room_id = '<room-id>' ORDER BY id;
```

房间清理只能调用公开 `DELETE /platform/rooms/{roomID}`，不要直接删除生产数据。
