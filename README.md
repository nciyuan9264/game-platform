# Game Platform Prototype

这是一个独立于现有 `auth`、`game-backend`、`game-frontend` 的新工作区，用于实现五层解耦的在线桌游平台原型。

## 已实现服务

- `../auth/backend/cmd/auth-rpc`：独立 Auth Center Kitex RPC，平台不再内置 Mock Auth。
- `cmd/platform-api`：仅供本地 smoke 和旧集成环境使用的兼容 HTTP API；生产 HTTP 流量统一走 PAM Gateway。
- `cmd/platform-rpc`：统一 `GamePlatformService` Kitex RPC，承接玩家房间、历史、统计和排行接口。
- `cmd/control-plane`：内部 Control Plane Kitex RPC + 运维 HTTP，负责调度、房间生命周期、实例与拓扑。
- `cmd/gateway`：Realtime Gateway，负责 WebSocket 鉴权、session/presence 注册、命令路由、下行投递。
- `cmd/roomhost`：RoomHost，负责承载长期 RoomActor 和每局独立的 MatchRuntime，单房间单 owner 串行执行。
- `cmd/smoke-test`：端到端自测客户端。
- `web/`：集成测试前端页面，支持登录、创建房间、连接 WS、发送命令、查看消息和操作日志。

## 已实现分层

```text
Auth Service
Platform API
Control Plane
Distributed Runtime: registry/session/presence/router
Game Plugins: DaVinci / Acquire / Splendor
```

## 解耦边界

- Gateway 不依赖 Auth 业务服务包，只通过 `internal/authclient` 调用 Auth Center Kitex RPC。
- 浏览器登录和资料接口由 Platform API 暴露，内部调用 Auth RPC；Auth Center 不暴露 HTTP。
- Gateway 强制配置 `AUTH_RPC_ADDR`，不保留生产 Mock/JWKS fallback。
- 节点心跳逻辑位于 `internal/platform/node`，Control Plane 和 Gateway 复用该基础设施包。
- Control Plane 只提供内部房间控制 RPC 和运维观测 HTTP，不承接业务后台 API，也不进入实时命令热路径。
- Distributed Runtime 使用 `room.cmd.<ownerNode>` 承载 Gateway 游戏命令，使用 `room.control.<ownerNode>` 承载 Control Plane 生命周期命令，NATS pub/sub 处理广播，JetStream 处理 command log。
- RoomHost 和 Control Plane 只依赖 `internal/game` 插件接口，不直接依赖具体游戏。
- 当前 `internal/plugins` 注册 DaVinci、Acquire 和 Splendor；后续新增游戏应注册 `game.Definition`，而不是改 RoomHost 的执行逻辑。

## 当前架构入口

- 工作区边界：[`../WORKSPACE.md`](../WORKSPACE.md)
- 目标架构：[`docs/target-architecture.md`](docs/target-architecture.md)
- Acquire 迁移兼容矩阵：[`docs/acquire-migration-compatibility.md`](docs/acquire-migration-compatibility.md)
- 服务边界和 HTTP API 归属：[`docs/service-boundaries.md`](docs/service-boundaries.md)
- Auth RPC 迁移：[`docs/auth-rpc-migration.md`](docs/auth-rpc-migration.md)
- 游戏日志架构：[`docs/game-log-architecture.md`](docs/game-log-architecture.md)
- 游戏插件接入：[`docs/game-plugin-guide.md`](docs/game-plugin-guide.md)

## 游戏插件模型

本项目按 Nakama 风格使用“平台仓库 + 游戏插件”的接入方式：平台 runtime 固定提供 Gateway、Control Plane、RoomHost、调度、路由、租约、恢复、快照和命令日志；具体游戏以插件形式实现并注册到 `internal/plugins`。

插件需要提供：

- `game.Engine`：初始化状态、应用命令、编码/解码快照、生成每个玩家的私有视图。
- 可选 `game.AIPlanner`：根据规则层暴露的 AI request 生成普通 room command。
- `game.Definition`：声明 `gameType`、版本、默认人数、Engine 和 AI。

Runtime 不应 import 具体游戏包；具体游戏只应出现在插件清单或游戏自身实现中。

Control Plane 为每个 Match 生成安全随机 seed，RoomHost 在 Match 运行时创建成功前保存 seq=0 初始快照。
插件必须把 seed/counter 编入状态以保证重放确定性，但不得通过玩家视图、观战视图或
可推导的对象 ID 泄露隐藏随机结果。
该安全契约从 Acquire/DaVinci v2 生效；部署 v2 前需要结束仍在运行的 v1 房间。

新增游戏的最小接入步骤见 [docs/game-plugin-guide.md](docs/game-plugin-guide.md)。

## 房间创建链路

房间创建分为元数据创建和运行时 actor 创建：

```text
Platform API
  -> authenticate through Auth RPC
  -> call Control Plane Kitex RPC

Control Plane
  -> resolve game definition
  -> select RoomHost owner
  -> write room metadata
  -> reserve round 1 match metadata and random seed
  -> init owner lease
  -> send runtime_room_create to owner RoomHost
  -> RoomHost initializes RoomActor and round 1 MatchRuntime
```

首次玩家连接仍会触发 RoomHost 的懒加载兜底，用于恢复旧房间或处理临时消息乱序；正常路径应由 `runtime_room_create` 显式建立 actor。

终局只结束当前 Match，Room 保持 `open`。房主调用
`POST /platform/rooms/{roomID}/matches` 后，Control Plane 分配新的
`<roomID>-rN`，并通过 `runtime_match_create` 在原 RoomActor 内开始下一局。

## 技术选型

- Go 1.24
- Postgres：Room/Match 元数据、按 Match 分区的 snapshot、reliable outbound、command idempotency、history 和 spectator projection
- Redis：session/presence/node heartbeat 等热状态
- etcd：room owner lease、epoch CAS、后续节点租约/leader election
- NATS：Gateway 与 RoomHost 的内部命令和广播
- NATS JetStream：per-Match command log，已配置 retention、file storage、duplicate window、临时 replay consumer
- Docker Compose：生产仅编排 Game Platform 服务；Redis/NATS/etcd 由 `../infra` 托管，Postgres 使用远端 RDS
- Traefik/Nginx：统一入口和 Gateway WebSocket 负载均衡

## 房间调度与入口负载均衡

这里有两层不同的分发：

- Traefik/Nginx 按路由把业务 HTTP 发给 Platform API、运维 HTTP 发给 Control Plane、WebSocket 发给 Gateway。
- RoomHost owner 由 Control Plane 调度器选择，和请求落到哪个 Gateway 不是同一件事。

Control Plane 支持 `ROOM_SCHEDULER`：

| 策略 | 用途 |
| --- | --- |
| `load_aware` | 默认策略，按当前 `activeRooms + recentPendingRooms` 选择真实运行负载更低的 RoomHost |
| `least_assigned` | 兼容/诊断策略，按历史未结束 `assignedRooms + activeRooms` 选择，容易被历史测试房间影响 |
| `round_robin` | 诊断策略，使用 Redis 共享计数器轮转，只用于验证分发链路，不代表生产负载均衡 |

`load_aware` 不把很久以前创建、未连接、未结束的历史房间当作主负载；但会把短时间内刚创建还没连接的 pending room 计入分数，避免连续快速创建时因为 RoomHost 心跳尚未刷新而全部落到同一台。每次 `room_created` runtime event 都会记录 `schedulerPolicy`、`schedulerReason`、`schedulerWindow` 和候选 RoomHost 的分数。前端 `Message Flow` / `Backend Runtime Events` 可以直接看到房间为什么被分到某个 RoomHost。

## 存储职责

当前已经把 Redis 过载的数据库职责拆开：

| 组件 | 当前职责 |
| --- | --- |
| Postgres | `rooms`、`matches`、按 Match 分区的 snapshots/outbound/ACK、历史、观战与运行投影 |
| Redis | `node:*`、`session:*`、`presence:*` 等短期热状态 |
| etcd | room owner lease、epoch CAS |
| NATS JetStream | `GAME_COMMANDS` stream，`cmdlog.<matchID>` command log，`Nats-Msg-Id=<matchID>:<commandID>` 去重 |

本地 NATS 使用 Control/Gateway/RoomHost 独立凭据和 subject ACL：Gateway 只能发布
`room.cmd.*`，Control Plane 只能发布 `room.control.*`。JetStream 数据挂载到命名卷，
8222 monitoring 仅绑定宿主机 localhost。

## 本地自测

当前推荐使用宿主机二进制 + Docker Redis/Postgres/etcd/NATS 的本地自测脚本：

```bash
newgrp docker <<'EOF'
./scripts/run-local-smoke.sh
EOF
```

成功输出示例：

```text
room davinci_xxx owner roomhost-1
bob room sync {...}
SMOKE_OK
SMOKE_DONE
```

该测试会验证：

- Auth 登录两个用户。
- Platform API 调用 Control Plane RPC 创建 DaVinci 房间。
- Scheduler 分配房间到 RoomHost。
- 玩家 A/B 都通过统一 `gateway-lb` WebSocket 入口连接。
- Nginx 将 WebSocket 分发到 Gateway-1/Gateway-2。
- Gateway 将命令路由到房间 owner RoomHost。
- RoomHost 广播消息回不同 Gateway。
- 已应用命令写入 per-Match JetStream command log，并可通过 `GET /platform/command-log?matchID=<matchID>` 查询。
- Bob 断开后带 `lastSeq` 重连，Gateway 从 `outbound_messages` 执行补帧。
- 客户端收到 DaVinci `ROOM_SYNC`。

本地 smoke 默认使用真实 Auth Center Kitex RPC，不再提供 Mock Auth 模式。

## 集成测试环境

分阶段集成测试使用 Traefik Docker provider 做动态反向代理，适合模拟“先启动一台服务器，再启动第二台服务器加入集群”。

```bash
# 1. 启动共享基础设施和前端
newgrp docker <<'EOF'
./scripts/integration-env.sh up-infra
EOF

# 2. 启动第一套服务实例
newgrp docker <<'EOF'
./scripts/integration-env.sh up-a
EOF

# 3. 通过 Traefik 统一入口跑 smoke
newgrp docker <<'EOF'
./scripts/integration-env.sh smoke-lb
EOF

# 4. 再启动第二套服务实例，模拟新服务器加入
newgrp docker <<'EOF'
./scripts/integration-env.sh up-b
EOF

# 5. 再次跑 smoke，后续 HTTP/WS 流量会分别进入两个 Platform API/Gateway backend
newgrp docker <<'EOF'
./scripts/integration-env.sh smoke-lb
EOF
```

入口：

- 前端控制台：`http://localhost:18000`
- CloudBeaver 数据库控制台：`http://localhost:18030`
- 统一 API/WS 入口：`http://localhost:18000`、`ws://localhost:18000/ws`

CloudBeaver 首次打开会要求初始化管理员账号。连接当前项目 Postgres 时使用：

| 字段 | 值 |
| --- | --- |
| Host | `postgres` |
| Port | `5432` |
| Database | `game_platform` |
| User | `game` |
| Password | `game` |

前端页面可以逐步执行：

- 登录。
- 创建 DaVinci 房间。
- 连接 WebSocket。
- 添加 AI。
- ready / begin / game_ready。
- 发送自定义 game command。
- 查看每条 WS 消息和操作日志。

后端日志可通过：

```bash
newgrp docker <<'EOF'
./scripts/integration-env.sh logs game-platform-a-gateway-1
./scripts/integration-env.sh logs game-platform-a-roomhost-1
EOF
```


## 真实双机集成测试

当前两台机器可以按以下方式跑真实跨主机部署，默认约定：

| 角色 | IP | 职责 |
| --- | --- | --- |
| primary | `10.37.223.1` | Redis/Postgres/etcd/NATS、Auth RPC、Control Plane、Traefik、前端、Platform API A、Gateway A、RoomHost A |
| secondary | `10.37.4.66` | Platform API B、Gateway B、RoomHost B |

先保证两台机器都能访问同一份代码，至少 secondary 上要有 `game-platform` 目录。primary 需要同级的 `../auth/backend`、`../auth/keys` 和 `../game-platform-admin`，secondary 只需要构建当前仓库服务。

primary 启动：

```bash
cd /data00/home/wangzhenyu.9264/learning/game-platform
PRIMARY_HOST=10.37.223.1 SECONDARY_HOST=10.37.4.66 \
  ./scripts/distributed-env.sh up-primary
```

secondary 加入有两种方式。推荐只在 primary 保留一份代码，通过 SSH 远程控制 secondary 的 Docker daemon：

```bash
cd /data00/home/wangzhenyu.9264/learning/game-platform
PRIMARY_HOST=10.37.223.1 SECONDARY_HOST=10.37.4.66 SECONDARY_SSH=<user>@10.37.4.66 \
  ./scripts/distributed-env.sh up-secondary-remote
```

如果 secondary 本机也有代码，也可以在 secondary 上执行：

```bash
cd /data00/home/wangzhenyu.9264/learning/game-platform
PRIMARY_HOST=10.37.223.1 SECONDARY_HOST=10.37.4.66 \
  ./scripts/distributed-env.sh up-secondary
```

也可以在 primary 上一条命令部署两端并跑 smoke：

```bash
SECONDARY_SSH=<user>@10.37.4.66 ./scripts/distributed-env.sh deploy-remote
```

回到 primary，通过统一入口跑端到端 smoke：

```bash
PRIMARY_HOST=10.37.223.1 SECONDARY_HOST=10.37.4.66 \
  ./scripts/distributed-env.sh smoke
```

入口和观测：

- 集成测试控制台与统一 API/WS：`http://10.37.223.1:18000`、`ws://10.37.223.1:18000/ws`
- 登录页：`http://10.37.223.1:18031/login`
- 游戏页：`http://10.37.223.1:18032/game/acquire`、`http://10.37.223.1:18032/game/davinci`
- 后台页：`http://10.37.223.1:18000/admin`
- 实例列表：`curl -sS http://10.37.223.1:18000/platform/instances`
- 拓扑：`curl -sS http://10.37.223.1:18000/platform/topology`
- Acquire 场景：`./scripts/distributed-env.sh smoke-acquire`

需要放通的最小网络面：primary 接收 secondary 的 `5432`、`6379`、`2379`、`4222`、`19090`、`18091`；secondary 接收 primary Traefik 的 `18083`、`18081`；浏览器或压测机访问 primary 的 `18000`。这套配置是 integration 级别，会返回开发验证码并使用 HTTP，不能直接当生产安全配置使用。

## Level 2 恢复自测

恢复测试脚本：

```bash
newgrp docker <<'EOF'
./scripts/run-recovery-smoke.sh
EOF
```

该测试会验证：

- 只启动 `roomhost-1` 创建并运行房间。
- 玩家分别连接 `gateway-1` 和 `gateway-2`。
- 房间命令写入 command log。
- 房间状态保存 snapshot。
- 测试中途杀掉 `roomhost-1`。
- 启动 `roomhost-2`。
- `roomhost-2` 等待 lease 过期后抢占 room owner，epoch +1。
- `roomhost-2` 从 snapshot 和 command log 恢复房间状态。
- Gateway 后续命令按最新 owner 路由到 `roomhost-2`。
- 客户端继续收到 `ROOM_SYNC`。

## Docker Compose

`docker-compose.yml` 是本地应用清单，同样使用 `../infra` 的共享服务和环境变量中的 PostgreSQL DSN；自包含故障测试使用 `docker-compose.integration.*`。`docker-compose.prod.yml` 是独立生产清单，不与本地清单合并。生产只部署 Schema Migration、Control Plane、Platform RPC、Gateway 和 RoomHost；HTTP API 由 PAM Gateway 转发到 Platform RPC。

生产环境先启动 `../infra` 中的共享 Redis、NATS、etcd 和 OpenTelemetry Collector，再执行 Game Platform 配置校验：

```bash
docker network inspect global-network >/dev/null 2>&1 || docker network create global-network
docker compose -f ../infra/docker-compose.yml up -d redis nats etcd otel-collector
./scripts/validate-prod-config.sh
docker compose -f docker-compose.prod.yml up -d
```

生产 `.env` 可从 [`.env.example`](.env.example) 初始化，必须提供指向远端 RDS 的
`POSTGRES_DSN` 和 `WS_ALLOWED_ORIGINS`。该数据库账号需要
同时具备 Schema Migration 和运行期读写权限。Redis 默认使用 `global-redis:6379`，
NATS 默认使用 `nats://global-nats:4222`，Auth RPC 由独立 Auth 服务提供。
生产数据库统一使用阿里云 RDS `gamebus_prod`，连接串必须启用
`sslmode=require`。部署前校验会拒绝旧 Neon、本地主机和非目标数据库连接。

生产基线默认不返回开发验证码、不配置默认管理员、不开启 Acquire 压测接口。
本地调试需要开发验证码时必须同时显式设置
`AUTH_DEV_CODE_LOG=true` 和 `AUTH_DEV_CODE_RESPONSE=true`。管理员角色只能通过
`auth_center.users.role` 的受控数据库变更配置，用户邮箱不会授予管理员权限。压测接口需要显式设置
`ACQUIRE_LOADTEST_ENABLED=true`，并提供至少 32 字节的随机
`ACQUIRE_LOADTEST_TOKEN`。

在当前开发机上，Docker Hub 和容器内 Go module 下载可能受网络影响；脚本方式更稳定。后续可将构建改为宿主机预编译二进制 + 轻量 runtime 镜像。

## 当前限制

- Platform 全部运行服务为单实例；共享 Redis、NATS、etcd 也仍是单实例，是当前单服务器部署明确接受的单点。PostgreSQL 由远端 RDS 托管。
- Control Plane 和 Traefik 已移除原始 Docker socket；服务日志接口暂时下线，等待集中日志后端。
- 内部 Kitex RPC 不做服务身份鉴权；用户操作通过 TTHeader 透传 access token，并在 RPC 服务端
  重新校验用户和角色。RPC 端口必须限制在可信私网，生产传输加密仍依赖真实部署环境。
- Gateway 只按 etcd ready route 路由；RoomHost 接管使用 PostgreSQL 持久化 epoch、
  JetStream `OWNER_FENCE` 和 fenced projections。
- Acquire loadtest runner 已完成编译测试，尚未固化容量基线和 SLO。

## 下一步

1. 接入 OpenTelemetry 和集中日志后端。
2. 运行 Acquire 压测并固化容量基线、告警阈值和 SLO。
