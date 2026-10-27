# Game Platform

Game Platform 是桌游平台的后端服务仓库，提供房间生命周期、实时命令路由、游戏运行时、历史记录、回放、统计和排行榜能力。公开 HTTP 与浏览器认证协议由 PAM Gateway 承接，本仓库不直接暴露业务 HTTP API。

## 服务入口

生产发布包含以下入口：

- `cmd/platform-rpc`：实现 `GamePlatformService`，提供房间、历史、回放、统计和排行 RPC。
- `cmd/control-plane`：负责房间创建、节点调度、生命周期控制和运维观测。
- `cmd/gateway`：负责 WebSocket 鉴权、session/presence、命令路由和下行投递。
- `cmd/roomhost-acquire`：Acquire 单游戏 RoomHost。
- `cmd/roomhost-davinci`：DaVinci 单游戏 RoomHost。
- `cmd/roomhost-splendor`：Splendor 单游戏 RoomHost。
- `cmd/roomhost-yinsh`：YINSH 单游戏 RoomHost。
- `cmd/roomhost-othello`：Othello 单游戏 RoomHost。
- `cmd/roomhost-fugitive`：Fugitive 单游戏 RoomHost。
- `cmd/roomhost-warchest`：War Chest 单游戏 RoomHost。
- `cmd/roomhost-quoridor`：步步为营（Quoridor）单游戏 RoomHost。
- `cmd/roomhost-loveletter`：掌心密信（Love Letter）单游戏 RoomHost。
- `cmd/migrate-schema`：发布前执行的幂等数据库 Schema Migration。
- `cmd/game-release`：执行单游戏 A/B slot 发布、晋级、排空和回滚。
- `cmd/backfill-replay`：按显式 engine mapping 回填旧录像物化数据的运维工具。

## 请求路径

```text
Browser
  | HTTP
  v
PAM Gateway
  | Kitex RPC
  v
Platform RPC

Browser
  | WebSocket
  v
Realtime Gateway
  | room.cmd.<ownerNode>
  v
Per-game RoomHost
  | delivery subjects
  v
Realtime Gateway
```

房间创建由 Platform RPC 调用 Control Plane RPC。Control Plane 从 `game_catalog` 解析 active engine version，选择精确匹配游戏和版本的 ready RoomHost，写入房间与首局元数据，然后发送内部 runtime create 命令。

## 服务边界

- PAM Gateway 拥有公开 HTTP 路由、认证 Cookie 和浏览器协议适配。
- Platform RPC 拥有 Game Platform 业务 RPC，不读取 Auth 数据表。
- Control Plane 拥有房间和节点生命周期，不进入实时游戏命令热路径。
- Realtime Gateway 只负责 WebSocket、连接状态和消息路由，不执行游戏规则。
- RoomHost 串行执行房间命令，维护 MatchRuntime、快照、命令日志和历史投影。
- Auth 身份和资料通过 `internal/authclient` 调用 Auth RPC 获取。
- 每个生产 RoomHost 镜像只编译一个游戏定义；中心服务不得 import 具体游戏包。
- `internal/plugins` 仅供离线回放回填工具聚合旧版本解码器。

详细边界见 [服务边界](docs/service-boundaries.md) 和 [目标架构](docs/target-architecture.md)。

## 游戏运行时

每个 Room 是长期存在的大厅和路由边界，每局游戏是独立 Match：

```text
Room:  creating -> open -> closing -> closed
Match: preparing -> running -> ending -> ended/aborted
```

命令同时携带 `roomID` 和 `matchID`。Room owner epoch 阻止旧 owner 写入，`matchID` 阻止上一局的延迟命令进入新局。RoomHost 使用 staged state，在命令日志写入成功后才提交内存状态和序列号。

游戏通过 `game.Definition` 提供：

- `game.Engine`：初始化、校验并应用命令、编码和解码状态、生成玩家视图。
- 可选 `game.AIPlanner`：把 AI 决策转换为普通 room command。
- 游戏类型、engine version 和人数约束。

接入新游戏见 [游戏插件指南](docs/game-plugin-guide.md)，版本发布见 [游戏发布手册](docs/game-release-runbook.md)。

## 数据职责

| 存储 | 职责 |
| --- | --- |
| PostgreSQL | 房间、对局、快照、可靠下行、ACK、历史、回放、观战投影和 owner epoch |
| Redis | 节点心跳、session 和 presence 等短期热状态 |
| etcd | 带租约的 owner/ready route |
| NATS | Gateway、Control Plane 与 RoomHost 的内部命令和广播 |
| JetStream | 按 Match 排序的 owner fence 与 command log |

旧录像格式和历史规则兼容代码属于持久化数据契约，不能因当前 engine 升级而删除。

## 开发与验证

需要 Go 1.24。常用质量门禁：

```bash
test -z "$(gofmt -l $(find . -name '*.go' -not -path './.git/*'))"
go vet ./...
go test ./...
./scripts/changed-release-components-test.sh
./scripts/validate-engine-version-bump-test.sh
./scripts/validate-center-imports.sh
```

构建全部生产入口：

```bash
for service in \
  backfill-replay control-plane game-release gateway migrate-schema \
  platform-rpc roomhost-acquire roomhost-davinci roomhost-splendor \
  roomhost-yinsh roomhost-othello roomhost-fugitive roomhost-warchest \
  roomhost-quoridor roomhost-loveletter
do
  go build -o "/tmp/${service}" "./cmd/${service}"
done
```

RPC 代码从固定版本的 `pam-idl` 生成。修改 IDL 后运行 `scripts/generate-rpc.sh`，不要手工编辑 `kitex_gen`。

生产拓扑的 HTTP、WebSocket、Control、JetStream 和 PostgreSQL 手动回归统一通过：

```bash
./scripts/run-regression.sh quality
./scripts/run-regression.sh probe
./scripts/run-regression.sh canary
```

环境变量、生产写入范围和通过标准见 [全链路回归手册](docs/regression-testing.md)。

## 生产部署

仓库只维护 [`docker-compose.prod.yml`](docker-compose.prod.yml)。它包含：

- 一次性 `schema-migrate`；
- `control-plane`、`platform-rpc`、`gateway`；
- Acquire、DaVinci、Splendor、YINSH、Othello、Fugitive、War Chest、
  Quoridor 和 Love Letter 各自的 A/B RoomHost slot。

共享 Redis、NATS、etcd 和 OpenTelemetry Collector 由相邻 `infra/docker-compose.yml` 管理，PostgreSQL 使用远端 RDS。部署前必须设置 `.env` 中的 RDS DSN、WebSocket Origin allowlist 和各游戏 slot 的镜像与版本。

```bash
docker network inspect global-network >/dev/null 2>&1 || docker network create global-network
docker compose -f ../infra/docker-compose.yml up -d redis nats etcd otel-collector
./scripts/validate-prod-config.sh
docker compose -f docker-compose.prod.yml up -d
```

`validate-prod-config.sh` 会拒绝本地数据库、非 TLS RDS、旧 Platform API、通用 RoomHost、编号副本和内部负载均衡等失效拓扑。

GitHub Actions 根据变更路径构建不可变镜像。中心服务按 Schema Migration、运行服务顺序发布；游戏通过 A/B slot 独立晋级，旧 slot 仅在活动房间归零后停止。

## 运维工具

Schema Migration 是每次发布的一部分：

```bash
go run ./cmd/migrate-schema
```

旧录像回填必须显式提供游戏、engine version 和 replay schema version：

```bash
go run ./cmd/backfill-replay \
  --game-type acquire \
  --engine-version acquire-v2 \
  --replay-schema-version 1 \
  --batch-size 100
```

回填工具只填充空 payload，不猜测未知 engine version。具体流程见 [游戏发布手册](docs/game-release-runbook.md)。

## 架构文档

- [目标架构](docs/target-architecture.md)
- [服务边界](docs/service-boundaries.md)
- [游戏日志架构](docs/game-log-architecture.md)
- [游戏插件指南](docs/game-plugin-guide.md)
- [游戏发布手册](docs/game-release-runbook.md)
- [全链路回归手册](docs/regression-testing.md)

## 已接受限制

- 当前生产为单服务器 Compose：Control Plane、Platform RPC、Gateway 和共享 Redis/NATS/etcd 都是单实例。
- PostgreSQL 由远端 RDS 托管。
- 内部 Kitex RPC 依赖可信私网隔离，应用层尚未实现服务身份鉴权。
- Control Plane 不挂载 Docker socket；服务日志依赖外部集中日志系统。
