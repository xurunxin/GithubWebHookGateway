# GitHub Webhook Relay

轻量级 GitHub Webhook Relay 服务，部署在 VPS 上，接收 GitHub App Webhook 请求，通过 WebSocket 长连接将事件可靠转发给内网 OpenClaw Agent。

## 架构

```
GitHub App Webhook (HTTPS POST)
        |
        v
VPS: github-webhook-relay
        | 1. 校验 HMAC-SHA256 签名
        | 2. delivery_id 幂等去重
        | 3. 事件写入 SQLite
        | 4. 快速返回 202 Accepted
        v
SQLite Event Queue
        |
        | WSS/WS 长连接 (agent_token 鉴权)
        v
内网 OpenClaw Agent
        |
        v
内网 OpenClaw
```

## 技术栈

- **语言**: Go（纯 Go SQLite 驱动，CGO_ENABLED=0）
- **HTTP**: Go 标准库 `net/http`
- **WebSocket**: `gorilla/websocket`
- **数据库**: SQLite（`modernc.org/sqlite`，纯 Go 实现）
- **部署**: Docker + Docker Compose

## 快速开始

### 1. 配置环境变量

```bash
cp .env.example .env
```

编辑 `.env`，设置三个 Token：

```env
GITHUB_WEBHOOK_SECRET=your-github-webhook-secret
OPENCLAW_AGENT_TOKEN=your-random-long-token
ADMIN_TOKEN=your-random-admin-token
```

### 2. 启动服务

```bash
docker compose up -d
```

### 3. 验证

```bash
curl http://localhost:8080/health
# {"ok":true,"service":"github-webhook-relay","time":"..."}
```

## GitHub Webhook 配置

在 GitHub App 设置中，将 Webhook URL 指向：

```
https://your-domain.com/webhook/github
```

Secret 需要与 `GITHUB_WEBHOOK_SECRET` 一致。

内容类型选择 `application/json`。

## WebSocket 协议

### 连接

```
wss://your-domain.com/ws/openclaw
```

鉴权（二选一）：

- Header: `Authorization: Bearer <OPENCLAW_AGENT_TOKEN>`
- Query: `?token=<OPENCLAW_AGENT_TOKEN>`

### 握手

Agent 连接后发送：

```json
{"type":"hello","agent_id":"openclaw-home-01","last_ack_id":123}
```

服务端响应：

```json
{"type":"hello_ack","ok":true,"server_time":"2026-06-08T12:00:00Z"}
```

### 接收事件

```json
{
  "type":"github_event",
  "event_id":123,
  "delivery_id":"abc-def",
  "github_event":"pull_request",
  "repository_full_name":"owner/repo",
  "installation_id":123456,
  "received_at":"2026-06-08T12:00:00Z",
  "payload":{...}
}
```

### ACK 确认

成功：

```json
{"type":"ack","event_id":123,"status":"ok"}
```

失败但可重试：

```json
{"type":"ack","event_id":123,"status":"failed","retryable":true,"reason":"timeout"}
```

失败不可重试：

```json
{"type":"ack","event_id":123,"status":"failed","retryable":false,"reason":"unsupported"}
```

### 心跳

Agent 发送：`{"type":"ping","time":"..."}`
服务端响应：`{"type":"pong","time":"..."}`

服务端同时支持 WebSocket 协议层 Ping/Pong。

## 管理 API

需要 `Authorization: Bearer <ADMIN_TOKEN>` 鉴权。

### 健康检查（无需鉴权）

```bash
GET /health
```

### 运行状态

```bash
curl -H "Authorization: Bearer $ADMIN_TOKEN" http://localhost:8080/status
```

响应：

```json
{
  "ok":true,
  "agent_connected":true,
  "connected_agents":1,
  "pending_events":3,
  "delivering_events":0,
  "acked_events":120,
  "dead_events":1
}
```

### 事件列表

```bash
curl -H "Authorization: Bearer $ADMIN_TOKEN" \
  "http://localhost:8080/events?limit=50&status=pending"
```

### 手动重试死信

```bash
curl -X POST -H "Authorization: Bearer $ADMIN_TOKEN" \
  http://localhost:8080/events/42/retry
```

## 环境变量完整列表

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `APP_ENV` | `production` | 运行环境 |
| `HTTP_ADDR` | `0.0.0.0:8080` | 监听地址 |
| `GITHUB_WEBHOOK_SECRET` | `change-me` | GitHub Webhook Secret |
| `OPENCLAW_AGENT_TOKEN` | `change-me` | Agent 连接 Token |
| `ADMIN_TOKEN` | `change-me` | 管理 API Token |
| `SQLITE_PATH` | `/data/relay.db` | 数据库文件路径 |
| `EVENT_MAX_RETRY` | `10` | 最大重试次数 |
| `EVENT_RETRY_INITIAL_SECONDS` | `5` | 初始重试间隔（秒） |
| `EVENT_RETRY_MAX_SECONDS` | `300` | 最大重试间隔（秒） |
| `EVENT_DELIVERY_BATCH_SIZE` | `10` | 批量下发数量 |
| `WS_WRITE_TIMEOUT_SECONDS` | `10` | WebSocket 写超时 |
| `WS_READ_TIMEOUT_SECONDS` | `90` | WebSocket 读超时 |
| `WS_PING_INTERVAL_SECONDS` | `30` | 心跳间隔 |
| `MAX_BODY_BYTES` | `26214400` | HTTP Body 大小限制 (25MB) |
| `LOG_LEVEL` | `info` | 日志级别 |

## 构建与推送镜像

```bash
# 构建
make build

# 构建并推送
make build-push TAG=v1.0.0

# 运行测试
make test
```

## Nginx 反向代理

```nginx
server {
    listen 443 ssl;
    server_name your-domain.com;

    # SSL 证书配置...

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location /ws/openclaw {
        proxy_pass http://127.0.0.1:8080/ws/openclaw;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

## 事件状态机

```
 webhook POST
      |
      v
  [pending] ──→ delivering ──→ [acked]
      ^              |
      |              | retryable=false
      |              v
      |           [dead]
      |
      +── retryable=true ──┘
      (指数退避, next_retry_at)
```

- **pending**: 等待下发
- **delivering**: 正在下发
- **acked**: Agent 确认处理成功
- **dead**: 超过最大重试次数或不可重试，死信
- **duplicate**: 重复的 delivery_id

## 常见问题

### Q: Agent 断线后事件会丢失吗？

不会。事件先写入 SQLite 再返回 GitHub，Agent 断线期间事件保持在 pending 状态，重连后自动下发。

### Q: 如何生成安全 Token？

```bash
openssl rand -hex 32
```

### Q: 数据库文件在哪？

Docker 部署在 `./data/relay.db`，通过 volume 映射到宿主机。

### Q: 如何查看日志？

```bash
make logs
# 或
docker logs -f github-webhook-relay
```

## 项目结构

```
.
├── cmd/server/main.go          # 入口
├── internal/
│   ├── admin/handler.go        # 管理 API
│   ├── config/config.go        # 配置加载
│   ├── github/handler.go       # Webhook 接收
│   ├── relay/relay.go          # 事件中继核心
│   ├── storage/                # SQLite 存储层
│   │   ├── db.go
│   │   ├── events.go
│   │   ├── agents.go
│   │   ├── event_logs.go
│   │   └── models.go
│   └── websocket/handler.go    # WebSocket 服务
├── migrations/001_init.sql     # 数据库迁移
├── Dockerfile
├── docker-compose.yml
├── Makefile
└── .env.example
```
