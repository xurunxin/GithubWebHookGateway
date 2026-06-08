# GitHub Webhook Relay 接入文档

## 适用场景

本服务用于将 GitHub App Webhook 事件从公网 VPS 安全转发到内网 OpenClaw。

典型场景：
- OpenClaw 部署在内网（无公网 IP），需要接收 GitHub 事件触发自动化任务
- 需要一个轻量、可靠的中继服务，不依赖 Redis/RabbitMQ 等额外中间件

## 架构概览

```
 GitHub (公网)                    VPS (公网)                  内网
┌──────────┐   HTTPS POST   ┌─────────────────┐   WSS   ┌───────────┐
│  GitHub  │ ───────────────→│ webhook-relay   │←────────│ OpenClaw  │
│   App    │                │ (Go + SQLite)   │         │  Agent    │
└──────────┘   ← 202 OK     └─────────────────┘         └───────────┘
```

## 第一步：VPS 部署服务

### 1.1 准备环境

- Docker 20.10+
- Docker Compose v2
- 一个域名（用于 HTTPS）+ SSL 证书（可用 Caddy/certbot 自动获取）

### 1.2 生成安全 Token

```bash
# 生成三个独立的 Token
echo "GITHUB_WEBHOOK_SECRET=$(openssl rand -hex 32)"
echo "OPENCLAW_AGENT_TOKEN=$(openssl rand -hex 32)"
echo "ADMIN_TOKEN=$(openssl rand -hex 32)"
```

### 1.3 配置 .env

```bash
cp .env.example .env
```

编辑 `.env`，填入上一步生成的 Token：

```env
GITHUB_WEBHOOK_SECRET=xxxx
OPENCLAW_AGENT_TOKEN=xxxx
ADMIN_TOKEN=xxxx
```

### 1.4 启动服务

```bash
docker compose up -d
```

验证：

```bash
curl http://localhost:8080/health
# {"ok":true,"service":"github-webhook-relay","time":"..."}
```

### 1.5 配置反向代理（Nginx 示例）

```nginx
server {
    listen 443 ssl http2;
    server_name relay.your-domain.com;

    ssl_certificate     /etc/ssl/certs/your-domain.crt;
    ssl_certificate_key /etc/ssl/private/your-domain.key;

    # Webhook 接口
    location /webhook/github {
        proxy_pass http://127.0.0.1:8080/webhook/github;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        client_max_body_size 25m;
    }

    # WebSocket 长连接（OpenClaw Agent）
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

    # 管理接口
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

## 第二步：配置 GitHub App Webhook

### 2.1 在 GitHub App 设置中

1. 进入你的 GitHub App → Settings → Webhooks
2. Webhook URL 填写：`https://relay.your-domain.com/webhook/github`
3. Content type 选择 `application/json`
4. Secret 填入 `.env` 中的 `GITHUB_WEBHOOK_SECRET`
5. 勾选需要的事件（至少勾选 `Pull requests`、`Issues`、`Push` 等）
6. 确保 `Active` 已勾选

### 2.2 验证 Webhook

在 GitHub App → Advanced 中，可以查看 Recent Deliveries，确认返回 `202 Accepted`。

## 第三步：配置 OpenClaw Agent

### 3.1 连接 WebSocket

Agent 连接地址：

```
wss://relay.your-domain.com/ws/openclaw
```

鉴权方式（二选一）：

```python
# Header 方式（推荐）
import websockets
import asyncio

async def connect():
    async with websockets.connect(
        "wss://relay.your-domain.com/ws/openclaw",
        extra_headers={"Authorization": "Bearer YOUR_OPENCLAW_AGENT_TOKEN"}
    ) as ws:
        ...
```

```python
# Query 方式
async with websockets.connect(
    "wss://relay.your-domain.com/ws/openclaw?token=YOUR_OPENCLAW_AGENT_TOKEN"
) as ws:
    ...
```

### 3.2 握手协议

连接建立后，Agent 必须发送 hello 消息：

```python
import json

hello = {
    "type": "hello",
    "agent_id": "openclaw-home-01",  # 唯一标识
    "last_ack_id": 0                  # 上次成功处理的事件 ID，首次填 0
}
await ws.send(json.dumps(hello))

# 等待服务端确认
response = await ws.recv()
print(json.loads(response))
# {"type":"hello_ack","ok":true,"server_time":"..."}
```

### 3.3 接收事件

```python
async def handle_event(data):
    event = json.loads(data)
    # 示例字段：
    # event["type"]              = "github_event"
    # event["event_id"]          = 123           # VPS 本地 ID
    # event["delivery_id"]       = "abc-def"     # GitHub Delivery ID
    # event["github_event"]      = "pull_request"
    # event["repository_full_name"] = "owner/repo"
    # event["installation_id"]   = 123456
    # event["payload"]           = {...}         # GitHub 原始 payload

    event_id = event["event_id"]

    try:
        # 调用 OpenClaw 本地 API 处理事件
        result = await call_openclaw(event)

        # 成功 ACK
        ack = {"type": "ack", "event_id": event_id, "status": "ok"}
        await ws.send(json.dumps(ack))

    except TimeoutError:
        # 失败但可重试
        ack = {
            "type": "ack",
            "event_id": event_id,
            "status": "failed",
            "retryable": True,
            "reason": "openclaw_timeout"
        }
        await ws.send(json.dumps(ack))

    except Exception as e:
        # 不可重试的错误
        ack = {
            "type": "ack",
            "event_id": event_id,
            "status": "failed",
            "retryable": False,
            "reason": str(e)
        }
        await ws.send(json.dumps(ack))

async def listen():
    async with websockets.connect(...) as ws:
        # 握手
        await ws.send(json.dumps(hello_msg))
        await ws.recv()  # hello_ack

        # 接收事件
        async for message in ws:
            data = json.loads(message)
            if data["type"] == "github_event":
                await handle_event(data)
```

### 3.4 心跳保持

Agent 应定时发送心跳，或依赖 WebSocket 协议层 Ping/Pong：

```python
import asyncio

async def heartbeat(ws):
    while True:
        await asyncio.sleep(25)  # 比服务端 30s 间隔短
        try:
            ping = {"type": "ping", "time": datetime.utcnow().isoformat()}
            await ws.send(json.dumps(ping))
        except:
            break
```

### 3.5 断线重连

```python
async def run_agent():
    retry_delay = 5
    while True:
        try:
            await connect_and_listen()
        except Exception as e:
            print(f"Connection lost: {e}, reconnecting in {retry_delay}s")
            await asyncio.sleep(retry_delay)
            retry_delay = min(retry_delay * 2, 60)  # 指数退避，最大 60s
```

## 第四步：操作与监控

### 查看服务状态

```bash
curl -H "Authorization: Bearer $ADMIN_TOKEN" \
  https://relay.your-domain.com/status
```

响应示例：

```json
{
  "ok": true,
  "agent_connected": true,
  "connected_agents": 1,
  "pending_events": 3,
  "acked_events": 120,
  "dead_events": 0
}
```

### 查看未处理事件

```bash
curl -H "Authorization: Bearer $ADMIN_TOKEN" \
  "https://relay.your-domain.com/events?status=pending&limit=20"
```

### 手动重试死信

```bash
curl -X POST -H "Authorization: Bearer $ADMIN_TOKEN" \
  https://relay.your-domain.com/events/42/retry
```

## 事件可靠性保证

| 场景 | 行为 |
|------|------|
| GitHub 发送 Webhook，Agent 在线 | 事件写入 SQLite → 即时下发 → Agent ACK → 标记 acked |
| Agent 离线 | 事件写入 SQLite，保持 pending；Agent 重连后自动下发 |
| Agent 处理失败（可重试） | 指数退避重试，默认最多 10 次，初始 5s，最大 300s |
| Agent 处理失败（不可重试） | 标记 dead，管理员可手动重试 |
| 重复 delivery_id | 幂等去重，返回 duplicate，不重复写入 |
| VPS 重启 | SQLite 持久化，重启后未 ACK 事件保持不变 |
| GitHub 签名错误 | 返回 401，不写入数据库 |

## 常见问题

### Q: 如何确认 WebSocket 已连接？

查看 `/status` 接口的 `agent_connected` 字段，或查看服务日志：

```bash
docker logs -f github-webhook-relay | grep "ws"
```

### Q: 事件一直处于 pending 状态？

检查 Agent 是否在线（`agent_connected`），以及 WebSocket 连接是否正常。

### Q: 如何修改重试策略？

修改 `.env` 中的以下参数，然后重启服务：

```env
EVENT_MAX_RETRY=10          # 最大重试次数
EVENT_RETRY_INITIAL_SECONDS=5   # 初始重试间隔
EVENT_RETRY_MAX_SECONDS=300     # 最大重试间隔
```

```bash
docker compose down && docker compose up -d
```

### Q: SQLite 数据库如何备份？

```bash
# 直接复制文件（WAL 模式下安全）
cp data/relay.db data/relay.db.backup
```

## 消息协议全览

```
┌──────────────┬────────────┬──────────────────────────────┐
│ 方向          │ type       │ 说明                         │
├──────────────┼────────────┼──────────────────────────────┤
│ Agent → VPS  │ hello      │ 握手，携带 agent_id           │
│ VPS → Agent  │ hello_ack  │ 握手确认                      │
│ VPS → Agent  │ github_event│ 下发 GitHub 事件               │
│ Agent → VPS  │ ack        │ 事件处理结果确认               │
│ Agent → VPS  │ ping       │ 应用层心跳                     │
│ VPS → Agent  │ pong       │ 心跳响应                       │
└──────────────┴────────────┴──────────────────────────────┘
```
