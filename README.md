# TinyCast

极简的直播推流平台：**OBS 推流 → 浏览器直接观看**。

- 无需注册登录，无需数据库，所有业务状态都在 Go 进程内存里
- SRS 6 负责 RTMP 接入与 WebRTC 分发，Go 只做鉴权、房间生命周期与在线人数
- 前端 Vue 3 + TypeScript + Vite，播放走 WebRTC（WHEP），状态通过 SSE 实时推送
- 推流身份（用户名 + 推流令牌）由 Go 后端严格校验，令牌只存 SHA-256 哈希

## 功能

| 能力 | 说明 |
| --- | --- |
| 创建推流配置 | 首页填写用户名与房间名称，浏览器本地生成 ≥256 bit 随机推流令牌，不请求后端 |
| OBS 推流 | 复制服务器地址与推流码即可开播 |
| 房间生命周期 | 只有真实推流成功才创建房间，LIVE → GRACE（断流保留 5 分钟）→ RELEASED |
| 断线重连 | 5 分钟内用原用户名 + 原令牌重新推流，房间与观看者保持不变 |
| 实时观看 | 房间详情页通过 WebRTC 低延迟播放 |
| 在线人数 | 基于 SRS on_play / on_stop 回调统计有效观看会话，SSE 实时更新 |
| 断流兜底 | 定时任务核对 SRS 客户端列表，清理丢回调造成的僵尸会话 |

## 架构

```
            RTMP 1935                    WebRTC(WHEP) 1985 + UDP 8000
  OBS ─────────────────► SRS 6 ──────────────────────────────► 浏览器
                          │  http_hooks (on_publish/unpublish/play/stop)
                          ▼
                    Go 后端 (tinycast, :8080)
                    ├── 内存状态：publishersByUsername / publishersByToken / rooms / viewers
                    ├── /api/*          房间与配置接口
                    ├── /api/events     SSE 快照推送
                    ├── /internal/srs/* SRS 回调（仅内网）
                    ├── /rtc/*          反代到 SRS HTTP API（同源 WHEP 信令）
                    └── /               内嵌前端静态页面（go:embed）
```

SRS 与 Go 后端可在同一台服务器上，通过 http_hooks 与 HTTP API 通信；Go 二进制自带前端页面，部署时只需要两个进程。

## 目录结构

```
cmd/tinycast/            程序入口（信号处理、HTTP 服务、优雅退出）
internal/config/         环境变量配置
internal/hub/            内存状态中心：推流鉴权、房间生命周期、观看会话、SSE 广播
internal/httpapi/        HTTP 路由、SSE、SRS 回调、中间件
internal/srsapi/         SRS HTTP API 客户端（客户端列表核对）
internal/janitor/        定时任务：超时释放 + 僵尸会话核对
internal/webui/          前端产物嵌入与 SPA 回退
web/                     Vue 3 + TypeScript + Vite 前端
deploy/srs/srs.conf      SRS 6 配置（RTMP + WebRTC + 回调）
deploy/systemd/          Linux systemd 单元与环境变量样例
deploy/windows/          Windows 构建/启动脚本
scripts/                 构建与开发脚本
```

## 快速开始（Docker Compose）

```bash
cp .env.example .env         # 修改 SRS_CANDIDATE 为服务器公网 IP 或域名
docker compose up -d --build
```

- 页面：http://<服务器IP>:8080
- RTMP 推流：rtmp://<服务器IP>/live
- 需要放通的端口：1935/tcp（推流）、8000/udp（WebRTC 媒体）、8080/tcp（页面与接口）；1985/tcp 只在需要跨机部署或调试时开放。

## 手动部署

### Linux

```bash
./scripts/build.sh            # 生成 bin/tinycast（前端已内嵌）
sudo mkdir -p /opt/tinycast /etc/tinycast
sudo cp bin/tinycast /opt/tinycast/
sudo cp deploy/systemd/tinycast.env.example /etc/tinycast/tinycast.env   # 按需修改
sudo cp deploy/systemd/tinycast.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now tinycast
```

SRS 侧：把 deploy/srs/srs.conf 中四个回调地址里的 tinycast 换成 127.0.0.1，复制到 SRS 的 conf/ 目录后用 ./objs/srs -c conf/srs.conf 启动（也可直接使用 deploy/systemd/srs.service）。

### Windows

```powershell
powershell -ExecutionPolicy Bypass -File deploy\windows\build.ps1
powershell -ExecutionPolicy Bypass -File deploy\windows\run.ps1
```

SRS 使用官方 Windows 版本 srs.exe -c conf\srs.conf，并把回调地址改为 127.0.0.1:8080。

## OBS 推流配置

在首页点「创建推流配置」，填写用户名与房间名称，浏览器本地生成令牌后展示：

| 项 | 值 |
| --- | --- |
| 服务器 | rtmp://live.example.com/live |
| 推流码 | username?room=房间名称&token=随机令牌 |

OBS「设置 → 直播 → 服务：自定义」中填入服务器与推流码即可。最终推流地址等价于 rtmp://live.example.com/live/username?room=...&token=...，SRS 会把查询串原样放进回调的 param 字段。

- 用户名同时是 SRS 流名，因此房间详情页地址为 /r/<username>
- 房间名称仅用于展示，不要求唯一
- 令牌、用户名与房间名称保存在浏览器 localStorage，刷新后仍可查看，且不会上传服务器
- 用户名规则：3–32 位，字母/数字/下划线/中划线，首字符须为字母或数字

## 推流鉴权规则（Go 后端实现）

1. 用户名不能绑定多个不同令牌
2. 同一令牌不能绑定多个不同用户名
3. 相同用户名 + 相同令牌不能同时建立多个推流连接（同一 SRS 客户端重复回调幂等放行）
4. 断开后 5 分钟内允许原用户名 + 原令牌重新推流
5. 超过 5 分钟未重推则释放用户名与令牌（可被其他令牌/用户名重新占用）
6. 房间名称不参与唯一性约束

房间状态机：

```
on_publish 校验通过 ──► LIVE ──on_unpublish──► GRACE(5m) ──超时──► RELEASED（记录删除）
                         ▲                        │
                         └──── on_publish 恢复 ────┘
```

## HTTP 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | /api/healthz | 健康检查 |
| GET | /api/config | 应用名、RTMP 服务器地址、WebRTC 地址、保留秒数 |
| GET | /api/rooms | 全部 LIVE + GRACE 房间 |
| GET | /api/rooms/{username} | 单个房间详情（RELEASED 返回 404） |
| GET | /api/rooms/{username}/play | 播放信息（GRACE 时 playback 为 null） |
| GET | /api/events | SSE，事件名 rooms，data 为完整快照 |
| POST | /internal/srs/on_publish | SRS 推流开始回调 |
| POST | /internal/srs/on_unpublish | SRS 推流结束回调 |
| POST | /internal/srs/on_play | SRS 播放开始回调 |
| POST | /internal/srs/on_stop | SRS 播放结束回调 |

回调放行条件：HTTP 200 且响应体为 0；拒绝时返回 403、响应体 1 并带 X-Tinycast-Reason 头（SRS 会因此断开该连接），拒绝原因同时写入日志。

```bash
curl http://127.0.0.1:8080/api/rooms
curl -N http://127.0.0.1:8080/api/events      # 持续接收快照
```

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| HTTP_ADDR | :8080 | 监听地址 |
| LOG_LEVEL | info | debug / info / warn / error |
| ROOM_GRACE | 5m | 断流保留时长，支持 300（秒）或 5m |
| JANITOR_INTERVAL | 15s | 定时清理任务间隔 |
| SRS_APP | live | SRS 应用名，需与推流地址中的 /live 一致 |
| SRS_CALLBACK_SECRET | 空 | 回调共享密钥，配置后需在 srs.conf 回调地址后加 ?secret=... |
| SRS_CALLBACK_ALLOWED_CIDRS | 回环 + 私网 | 允许回调的来源网段，逗号分隔 |
| SRS_CALLBACK_ALLOW_ALL | false | 关闭来源 IP 校验（仅在反代已隔离时使用） |
| SRS_RTMP_PUBLIC_BASE | rtmp://localhost:1935/live | 展示给推流用户的服务器地址 |
| SRS_WEBRTC_PUBLIC_BASE | webrtc://localhost/live | 展示用的 WebRTC 地址 |
| SRS_RTC_PROXY_TARGET | 空 | 设置后把同源 /rtc/* 反代到 SRS HTTP API |
| SRS_RTC_PUBLIC_BASE | 空 | 设置后 WHEP 地址使用绝对地址直连 SRS |
| SRS_CLIENTS_API_URL | 空 | SRS 客户端列表地址，设置后启用僵尸会话核对 |
| SRS_CLIENTS_API_USER / SRS_CLIENTS_API_PASSWORD | 空 | 访问 SRS HTTP API 的 BasicAuth |
| CORS_ALLOWED_ORIGINS | 空 | 前后端不同源时的允许来源 |
| PUBLIC_BASE_URL | 空 | 对外访问地址（用于生成绝对链接） |

## 本地开发

```bash
source scripts/dev-env.sh     # 仅在 $HOME 不可写、需要仓库内工具链时使用
./scripts/dev.sh              # 后端 :8080 + Vite :5173（/api、/rtc 已代理到后端）
go test -race ./...           # 后端测试
cd web && pnpm test           # 前端单元测试（vitest + happy-dom）
cd web && pnpm build          # 前端生产构建，产物进入 internal/webui/dist
```

如果机器的 `$HOME` 不可写（例如受限的容器环境），在仓库根与 `web/` 各放一份 `.npmrc` 指定本地缓存目录即可，例如：

```ini
store-dir=./.tools/pnpm-store
cache-dir=./.tools/npm-cache
```

没有 SRS 时也可以用 curl 模拟回调调试：

```bash
curl -X POST http://127.0.0.1:8080/internal/srs/on_publish \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"p1","app":"live","stream":"alex","param":"?room=room1&token=<你的令牌>"}'
curl -X POST http://127.0.0.1:8080/internal/srs/on_play \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"v1","app":"live","stream":"alex"}'
```

## 设计取舍

- **内存状态**：单实例、无数据库；进程重启即丢失全部房间与人数（可接受）
- **令牌哈希**：内存只保存 SHA-256(token)，任何接口都不回显明文或哈希
- **并发安全**：单一 sync.RWMutex 保护全部状态；同身份校验与占用在同一把写锁内完成原子性
- **SSE 全量快照**：每次推送完整房间列表（30ms 合并窗口），前端不做增量合并，逻辑简单且不易错位
- **观看会话按 SRS client_id 索引**：on_stop 只带 client_id 也能精确回收，重复回调幂等
- **回调来源限制**：默认只信任回环与私网，另可用共享密钥加固；/internal/* 不应暴露到公网

## 故障排查

| 现象 | 排查方向 |
| --- | --- |
| OBS 提示推流失败 | 看 Go 日志中的 reason 字段：username_bound_to_other_token / token_bound_to_other_username / already_publishing 等；确认 5 分钟保留期是否已释放 |
| 房间列表没有房间 | 只有 on_publish 放行后才会创建房间；检查 srs.conf 的 http_hooks 地址与端口 |
| 页面能打开但一直黑屏 | 确认 CANDIDATE / SRS_CANDIDATE 为公网可达 IP；放通 8000/udp；HTTPS 页面必须配 HTTPS 的 WHEP 地址，否则浏览器会拦截混合内容 |
| 在线人数不准 | 确认 SRS 已开启 on_play / on_stop 回调；异常断开由定时任务核对 SRS 客户端列表兜底，可配置 SRS_CLIENTS_API_URL |
| 播放 404 / CORS 报错 | 同源部署时配置 SRS_RTC_PROXY_TARGET=http://127.0.0.1:1985，由后端反代 /rtc/* |

## 许可

见 [LICENSE](./LICENSE)。
