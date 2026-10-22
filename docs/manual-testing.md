# 手工测试环境

## 页面入口

| 用途          | 地址                                    |
| ----------- | ------------------------------------- |
| 登录与用户资料     | <http://localhost:18031/login>        |
| Acquire     | <http://localhost:18032/game/acquire> |
| DaVinci     | <http://localhost:18032/game/davinci> |
| 管理后台        | <http://localhost:18020/admin/>       |
| 服务日志        | <http://localhost:18020/admin/logs>   |
| NATS 监控     | <http://localhost:8222>               |

## 查看日志

Game Platform 只管理 Platform API、Control Plane、Gateway 和 RoomHost。
Auth 与共享 Redis/NATS/etcd 的日志分别在对应项目和 `../infra` 中查看；
RDS 日志通过云数据库控制台查看。

也可以在终端执行：

```bash
cd /data00/home/wangzhenyu.9264/learning/game-platform
newgrp docker <<'EOF'
docker compose ps
docker compose logs -f platform-api-lb platform-api-1 control-plane gateway-1 roomhost-1
EOF
```

## 查看数据库

Game Platform PostgreSQL 使用 `.env` 中的远端 RDS `POSTGRES_DSN`。只读排查应使用
单独的只读 RDS 账号，不要复用应用账号。

常用查询：

```sql
SELECT * FROM rooms ORDER BY created_at DESC;
SELECT * FROM matches ORDER BY started_at DESC;
SELECT * FROM match_players ORDER BY match_id, player_id;
SELECT * FROM runtime_events ORDER BY id DESC LIMIT 200;
SELECT room_id, seq, epoch, engine_version, created_at
FROM room_snapshots
ORDER BY created_at DESC;
SELECT * FROM games ORDER BY id DESC;
SELECT * FROM game_events ORDER BY id DESC LIMIT 200;
SELECT room_id, seq, captured_at, available_at
FROM spectator_snapshots
ORDER BY captured_at DESC
LIMIT 200;
SELECT * FROM spectator_sessions ORDER BY created_at DESC;
```

命令行查询：

```bash
newgrp docker <<'EOF'
docker exec -it game-platform-postgres-1 psql -U game -d game_platform
EOF
```

## 验证延迟观战

1. 使用两个账号开始一局 Acquire 或 DaVinci；
2. 使用不在该房间内的第三个账号打开大厅；
3. 在进行中房间卡片点击“延迟观战”；
4. 首个画面必须在快照产生至少 5 分钟后出现；
5. 页面中的“可见 Seq”应落后于“实时 Seq”；
6. 对局参与者点击观战应被后端拒绝；
7. 对局结束后，已经建立的观战会话可继续看到延迟到达的结尾，新用户不能再创建观战会话。

## 启停

启动当前手工测试环境：

```bash
cd /data00/home/wangzhenyu.9264/learning/game-platform
newgrp docker <<'EOF'
docker compose up -d
EOF
```

停止服务但保留数据库卷：

```bash
newgrp docker <<'EOF'
docker compose down
EOF
```
