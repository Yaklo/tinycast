# TinyCast

轻量直播推流平台：**OBS 推流 → 浏览器直接观看**，双击就能跑。

- 无需注册登录、无需数据库，业务状态全在 Go 进程内存里，进程退出即清空
- **MediaMTX** 负责 RTMP 接入与 HLS / WebRTC 分发，Go 只做推流鉴权、房间生命周期与在线人数
- 前端 Vue 3 + TypeScript + Vite，播放器默认 LL-HLS（有声音、支持 H.265），可在 LL-HLS / HLS / WebRTC 三个通道间切换
- 推流身份（用户名 + 令牌）由后端严格校验，令牌只存 SHA-256 哈希
- 单个 Go 二进制内嵌全部前端资源，部署只有两个进程：mediamtx + tinycast

## 功能

| 能力 | 说明 |
| --- | --- |
| 创建推流配置 | 首页填用户名与房间名，浏览器用 `crypto.getRandomValues()` 生成 ≥256 bit 令牌，不请求后端 |
| OBS 推流 | 复制服务器地址与推流码即可开播，推流码整段带上 `room` 与 `token` |
| 推流鉴权 | 用户名与令牌一一绑定；同一身份不能同时推两路；回调在服务端校验，前端改不了 |
| 房间生命周期 | 只有真正推流成功才建房：LIVE → GRACE（断流保留 5 分钟）→ 释放 |
| 断线重连 | 5 分钟内用原用户名 + 原令牌重推，房间与观看者保持不变 |
| 三通道播放 | 默认 LL-HLS（1.2～3 秒、有声音、支持 H.265）；可切 HLS（整段分片，弱网最稳）或 WebRTC（约 0.3 秒、无声音）；WebRTC 取不到画面时自动回退 LL-HLS |
| 延迟档位 | LL-HLS 下可选「低延时（1.2 秒）」或「稳定（3 秒，默认）」，在 ⚙ 播放参数卡片里改 |
| 播放参数卡片 | 控制栏 ⚙ 打开：延迟档位、播放方式（自动 / HLS.js / 原生）都收在播放器下方，不挡画面 |
| 播放器控制 | 网页全屏、浏览器全屏（双击画面亦可）、画中画，Esc 退出网页全屏；鼠标静止 2.5 秒控制条自动收起 |
| 播放统计 | 🩺 面板（收在播放器下方，不遮挡画面）：播放通道 / 播放方式 / 分发协议、编码、分辨率、帧率、音视频码率、下载速率、累计接收、缓冲余量、直播延迟，默认画最近 60 秒下载速率曲线；RTT / 抖动 / 丢包只在 WebRTC 通道有 |
| 在线人数 | 播放器带稳定 `viewerId` 定时心跳，超时自动剔除；SSE 实时推送到所有页面 |
| 异常兜底 | 心跳超时、鉴权通过却没真正推流的绑定、断流超时，都由巡检循环回收 |

## 工作原理

```
                    RTMP 1935
  OBS ───────────────────────────────► MediaMTX ──────► HLS 8888 ─┐
   │                                      │  ▲                     ├─► 浏览器
   │  username?room=..&token=..           │  │ 媒体              │   （同源，经 Go 反代）
   │                                      │  └────► WebRTC 8889 ──┘
   │                        auth 回调（publish / read）
   ▼                                      ▼
                      Go 后端 tinycast :8080
                      ├── /internal/mtx/auth  推流鉴权（仅内网）
                      ├── 轮询 /v3/paths/list 判断推流开/停 → 房间状态
                      ├── /api/*              房间、配置、心跳
                      ├── /api/events         SSE 快照推送
                      ├── /hls/*  /rtc/*      反代到 MediaMTX（浏览器只跟 Go 同源交互）
                      └── /                   内嵌前端（go:embed）
```

两个进程可以同机也可以容器编排。浏览器全程只访问 8080，媒体端口无需对外暴露（WebRTC 的 UDP 8000 除外）。

## 快速开始（Docker）

```bash
# 1. 改一下对外地址（可选，局域网/公网访问时填服务器 IP 或域名）
cp .env.example .env
# 2. 起服务
docker compose up -d --build
# 3. 打开 http://<服务器地址>:8080
```

正式部署记得改 `deploy/mediamtx/mediamtx.docker.yml` 里的 `webrtcAdditionalHosts` 与 compose 里的
`MEDIAMTX_RTMP_PUBLIC_BASE`，否则观众拿到的候选地址仍是 `127.0.0.1`。

## 手动部署（Linux）

```bash
# MediaMTX：官方单文件程序
curl -L -o /tmp/mtx.tar.gz https://github.com/bluenviron/mediamtx/releases/download/v1.21.3/mediamtx_v1.21.3_linux_amd64.tar.gz
sudo tar -xzf /tmp/mtx.tar.gz -C /usr/local/bin mediamtx
sudo install -d /etc/mediamtx && sudo install -m 640 deploy/mediamtx/mediamtx.yml /etc/mediamtx/mediamtx.yml

# TinyCast：自带前端
./scripts/build.sh
sudo install -m 755 bin/tinycast /usr/local/bin/tinycast
sudo install -d /etc/tinycast && sudo install -m 640 deploy/systemd/tinycast.env.example /etc/tinycast/tinycast.env

# systemd
sudo install -m 644 deploy/systemd/mediamtx.service deploy/systemd/tinycast.service /etc/systemd/system/
sudo useradd -r -s /usr/sbin/nologin mediamtx -d /var/lib/mediamtx   # 首次
sudo useradd -r -s /usr/sbin/nologin tinycast
sudo systemctl daemon-reload && sudo systemctl enable --now mediamtx tinycast
```

改 `/etc/tinycast/tinycast.env` 后 `sudo systemctl restart tinycast` 生效。

## Windows 运行包

```bash
./scripts/package-windows.sh     # 产出 bin/tinycast-windows-amd64.zip（含 mediamtx.exe）
```

解压后右键 `open-firewall.cmd` → 以管理员身份运行，再双击 `start-all.cmd`，浏览器会自动打开。
详细步骤见包内 `部署说明.txt`。

## OBS 配置

| 项目 | 值 |
| --- | --- |
| 服务 | 自定义 |
| 服务器 | `rtmp://127.0.0.1:1935/live`（对外部署换成域名或 IP） |
| 推流码 | `username?room=房间名&token=随机令牌`（页面生成，整段复制） |

> 推流码请**整段复制**，不要手动改房间名。页面生成时房间名已经做过 URL 编码，
> OBS 还会再解一次；手改容易变成非法 URL 导致 MediaMTX 无法识别路径。

## 推流鉴权规则

规则全部在 `POST /internal/mtx/auth` 里完成，MediaMTX 每次推流/播放都会来问一次：

1. 一个用户名只能绑定一个令牌；
2. 一个令牌只能绑定一个用户名；
3. 同一用户名 + 同一令牌不能同时存在两路推流；
4. 原推流断开后，5 分钟内允许原用户名 + 原令牌重新推流；
5. 超过 5 分钟未重推，释放用户名与令牌；
6. 房间名仅用于展示，允许重名。

回调只接受内网来源（`MEDIAMTX_AUTH_ALLOWED_CIDRS`），并可加 `?secret=` 共享密钥，
因此外网无法直接调用它伪造身份。

## 房间状态机

```
（无记录） --推流成功--> LIVE --断流--> GRACE --5 分钟--> 释放（不再显示）
                              ▲          │
                              └──重推────┘
```

| 状态 | 含义 | 首页 | 详情页 |
| --- | --- | --- | --- |
| `LIVE` | 正在推流 | 显示“直播中” | 播放实时画面 |
| `GRACE` | 断流保留中 | 显示“断流保留”与倒计时 | 只显示提示与剩余时间，不假装播放 |
| 释放 | 记录已删除 | 不显示 | 显示“房间已释放” |

状态的来源是 `MEDIAMTX_API_URL/v3/paths/list`：路径 ready 即 LIVE，路径消失即 GRACE。
MediaMTX 短暂不可达时保持现状，不会把全部房间打成断流。

## 播放通道与编码支持

| 通道 | 延迟 | 声音 | H.264 | H.265 | AV1 | 说明 |
| --- | --- | --- | --- | --- | --- | --- |
| LL-HLS（默认） | 1.2～3 秒 | 有 | ✅ | ✅* | ✅* | 0.2 秒分片，分片在内存，不落盘 |
| HLS | 十秒级 | 有 | ✅ | ✅* | ✅* | 按整段分片缓冲 3 个，每秒请求数少约 20 倍，弱网最稳 |
| WebRTC | ~0.3 秒 | 无 | ✅（需关闭 B 帧） | ❌ | ❌ | 媒体透传不转码；浏览器只收 H.264，且流里不能有 B 帧 |

\* H.265/AV1 的 HLS 播放依赖系统解码能力：Chrome/Edge（Windows、macOS）与 Safari 正常，
Linux 下的 Chrome 与 Firefox 通常不行。页面会检测编码并给出“换通道”的提示。

WebRTC 通道没有声音是 MediaMTX 的设计取舍：它不做转码，AAC 无法变成浏览器能协商的编码，
于是补一条静音的 PCMU 轨让画面能显示。需要声音就用默认 HLS。

**WebRTC 通道还要求 H.264 不带 B 帧**：MediaMTX 检测到 B 帧会直接断开会话（日志里是
`doesn't support H264 streams with B-frames`）。OBS 里把 输出 → 高级 →「最大 B 帧数」设为 0，
x264 则在自定义选项里写 `bframes=0`。HLS 没有这个限制，实测 720p60 + 默认 B 帧仍能完整解出 60fps。

如果 WebRTC 通道连上 6 秒还一帧都没有（典型就是这条 B 帧拒绝），播放器会**自动切回 LL-HLS** 并在画面上说明原因，
不需要观众手动点。改好 OBS 设置后重新推流，再点一次「WebRTC」即可。

### HLS 延迟怎么压到 1 秒

MediaMTX 给的是 **LL-HLS**（低延迟 HLS）：清单里有

```
#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES,PART-HOLD-BACK=0.50000
#EXT-X-PART-INF:PART-TARGET=0.20000
#EXT-X-PART:DURATION=0.20000,...,INDEPENDENT=YES
```

也就是每 0.2 秒推一个分片、播放器只需停在直播边缘后 0.5 秒。实测（经本项目的 `/hls/` 反代）
清单里最新媒资只比当前时间早 **0.18～0.23 秒**，说明服务端没有在攒数据。

所以延迟只取决于播放器怎么起播、怎么追：

- **Chromium 一律用 hls.js**：Chrome / Edge 的 `canPlayType('application/vnd.apple.mpegurl')` 会返回
  `"maybe"`，但它的原生 HLS 只会整段下载、延迟十几秒，也拿不到分片。只有 Safari / iOS 走原生 HLS；
- `liveSyncDuration` + `lowLatencyMode`：按**秒**定目标，不受分片长度影响
  （换成 `liveSyncDurationCount: 1` 就会跟着 OBS 的关键帧间隔退成 4 秒）；「低延时」档设 1.2 秒，「稳定」档设 3 秒；
- 每秒量一次「直播边缘 − 当前播放位置」，低延时档**落后超过 1.8 秒就自动跳到边缘**（6 秒内最多跳一次，避免频繁跳）；
- `maxLiveSyncPlaybackRate: 1.5`：差得不多时用 1.5 倍速悄悄追上，不跳画面；
- 控制栏的「延迟 X s · 追平」按钮可以手动追，🩺 面板里的「直播延迟」是同一个数；
- 「缓冲余量」是 MSE 里排在播放位置后面的数据量，它比延迟大一点是正常的，别把它当延迟。

实测（Edge + 本项目 `/hls/` 反代，1280×720@60，低延时档）：OBS 默认关键帧 4 秒时直播延迟 **1.2 秒**，
关键帧 1 秒时 **0.6 秒**。把 OBS 的**关键帧间隔**设成 1 秒（输出 → 高级）可以再稳一点，
分片本身在内存里，不落盘。

### 播放参数卡片：延迟档位与播放方式

控制栏的 **⚙** 打开「播放参数」卡片（收在播放器下方，不遮挡画面）。这里只放两件与观看体验有关的事：
延迟档位和播放方式；通道切换仍然在播放器控制栏上。

| 档位 | 目标延迟 | 前向缓冲 | 主动追平 | 适合 |
| --- | --- | --- | --- | --- |
| 低延时 | 1.2 秒 | 4 秒 | 落后 >1.8 秒跳边缘，差得少时 1.5 倍速追 | 网络好、一直盯着看 |
| 稳定（默认） | 3 秒 | 6 秒 | 不主动追 | 弱网、投屏、公共 Wi-Fi |

- 两档走的都是 LL-HLS，区别只在「画面落后直播边缘多远」；默认是 **LL-HLS + 稳定档**，弱网下不容易卡。
- **缓冲余量本身就是延迟的一部分**：直播延迟 = 直播边缘 − 播放位置，而缓冲余量永远小于等于它。
  想多 1.8 秒缓冲，画面就必然多落后 1.8 秒，没有「只加缓冲不提延迟」的选项。
- 换档位会重建一次播放器，画面会黑一下（0.3~1 秒），因为目标延迟只能在起播时定；
  换播放方式同样重建一次。选择分别记在 `localStorage` 的 `tinycast.playTier` 与 `tinycast.hlsEngine`。
- **通道**：控制栏上是 LL-HLS / HLS / WebRTC 三选一。选 HLS 就是关掉 LL-HLS，按整段分片缓冲 3 个，
  延迟升到十几秒，但每秒请求数少约 20 倍，弱网下最稳；此时档位按钮置灰——分片粒度太粗，档位没有意义。
- 原生播放器与 WebRTC 通道下档位按钮也是灰的：前者由系统决定缓冲，后者不解析分片。

### 播放方式：自动 / HLS.js / 原生

「播放参数」卡片里可以选择播放方式，选择记在浏览器里（`localStorage` 的 `tinycast.hlsEngine`）：

| 选项 | 行为 | 什么时候用 |
| --- | --- | --- |
| 自动（默认） | Safari / iOS 用浏览器原生播放器，其它浏览器一律 HLS.js | 日常使用 |
| HLS.js | 强制 MSE + hls.js，指标最全，延迟能压到 1 秒 | Safari 上想要低延迟、想看完整统计 |
| 原生 | `<video src>` 直接播；只有 Safari / iOS 可选，其它浏览器置灰 | Safari 上看 H.265、要 AirPlay / 接力 |

- Chrome / Edge 的 `canPlayType('application/vnd.apple.mpegurl')` 会返回 `"maybe"`，但它只会整段下载，
  所以「原生」在 Chromium 上是灰的，鼠标悬停能看到原因；
- 强制 HLS.js 的代价：Safari 的 MSE 只解 H.264（H.265 / AV1 会黑屏）、失去 AirPlay、iPhone 画中画可能失效、
  更耗电，所以默认不让 Safari 强上；
- HLS.js 解不了当前编码时会**自动改回原生一次**并在面板里说明，不用观众自己猜；
- 原生播放器不把分片信息交给页面，所以码率、下载速率、延迟只能靠额外拉一次清单估算，面板的说明文字会写明。

面板顶部三行就是「现在到底走哪条路」：

- **播放通道**：LL-HLS、HLS 或 WebRTC，对应控制栏上的切换按钮；
- **播放方式**：HLS.js 还是原生播放器（WebRTC 通道显示 WebRTC）；
- **分发协议**：`LL-HLS（分片 0.2s）`、`HLS（整段分片）`、`WHEP（WebRTC）`。
  hls.js 走的是清单里的 `#EXT-X-PART` 与 `details.partTarget`；原生播放器没有这些回调，只能拉一次清单看
  `PART-HOLD-BACK`，看到 `HLS（整段分片）` 就说明当前是按 TARGETDURATION 整段播，延迟会高。

### 统计面板里哪些指标是「真的」

| 指标 | LL-HLS / HLS 通道 | WebRTC 通道 |
| --- | --- | --- |
| 分辨率、帧率、解码帧 | ✅ 从 video 元素读 | ✅ 从 getStats() 读 |
| 视频 / 音频码率 | ✅ 最近几个分片的字节数 ÷ 时长 | ✅ RTP 字节差 |
| 下载速率 | ✅ 分片请求字节 ÷ HTTP 耗时 | ✅ 等于码率之和 |
| 累计接收 | ✅ hls.js 加载过的分片字节累加 | ✅ RTP 字节 |
| 直播延迟 | ✅ 清单边缘 − 播放位置 | ⚠️ 没有清单，只能看观感 |
| RTT、抖动、丢包 | ❌ HTTP 分片没有这些概念，面板不显示 | ✅ candidate-pair / inbound-rtp |
| 播放方式、分发协议 | ✅ hls.js 事件（原生播放器靠拉清单判断） | ✅ 固定为 WHEP |

## HTTP 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/healthz` | 健康检查 |
| GET | `/api/config` | 前端启动配置（app、RTMP 地址、保留秒数、可用通道） |
| GET | `/api/rooms` | 房间列表（只含 LIVE 与 GRACE） |
| GET | `/api/rooms/{username}` | 房间详情 |
| GET | `/api/rooms/{username}/play` | 播放信息：`hlsUrl` 与 `whepUrl`（仅 LIVE） |
| POST | `/api/rooms/{username}/watch` | 观看心跳，`{"viewerId":"..."}` |
| DELETE | `/api/rooms/{username}/watch?viewerId=...` | 离开 |
| GET | `/api/events` | SSE 快照流（`event: rooms`） |
| POST | `/internal/mtx/auth` | MediaMTX 回调，**仅内网** |
| * | `/hls/*`、`/rtc/*` | 反代到 MediaMTX 的 HLS / WHEP（含 302 改写） |

在线人数 = 该房间在 `VIEWER_TIMEOUT` 内有过心跳的 `viewerId` 数量。
重复心跳天然幂等，关闭页面时前端会主动 `DELETE`。

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `HTTP_ADDR` | `:8080` | 监听地址 |
| `PUBLIC_BASE_URL` | 空 | 对外地址，留空按浏览器地址推断 |
| `LOG_LEVEL` / `LOG_FILE` / `LOG_MAX_MB` / `LOG_KEEP` | `info` / 空 / `16` / `3` | 日志与滚动 |
| `ROOM_GRACE` | `5m` | 断流保留时长 |
| `MONITOR_INTERVAL` | `1s` | 轮询 MediaMTX 路径的间隔 |
| `VIEWER_TIMEOUT` | `30s` | 心跳超时 |
| `PENDING_TIMEOUT` | `60s` | 鉴权通过但未真正推流的绑定保留时长 |
| `MEDIAMTX_APP` | `live` | 推流 app 名（RTMP 地址里的 `/live`） |
| `MEDIAMTX_API_URL` | `http://127.0.0.1:9997` | 路径状态 API |
| `MEDIAMTX_AUTH_SECRET` | 空 | 回调共享密钥，需与 mediamtx.yml 的 `?secret=` 一致 |
| `MEDIAMTX_AUTH_ALLOWED_CIDRS` | 本机 + 内网 | 允许回调的来源网段 |
| `MEDIAMTX_HLS_PROXY_TARGET` | `http://127.0.0.1:8888` | `/hls/` 上游 |
| `MEDIAMTX_WHEP_PROXY_TARGET` | `http://127.0.0.1:8889` | `/rtc/` 上游 |
| `MEDIAMTX_RTMP_PUBLIC_BASE` | `rtmp://localhost:1935/live` | 展示给主播的服务器地址 |
| `CORS_ALLOWED_ORIGINS` | 空 | 前后端分离时才需要 |

时长支持 `90s`、`5m`、`2m30s` 或纯秒数 `300`。

## 本地开发

```bash
source scripts/dev-env.sh          # 把工具链指到仓库内的 .tools
./scripts/dev.sh                   # 同时启动 go run 与 pnpm dev

# 单独跑
go run ./cmd/tinycast -open        # 后端 :8080，-open 自动开浏览器
cd web && pnpm dev                 # 前端 :5173，代理 /api 与 /rtc 到后端

# MediaMTX（本地验证推流链路）
.tools/mtx/mediamtx .tools/mtx/mtx-e2e.yml
```

测试：

```bash
go test -race ./...                # 后端
go vet ./... && gofmt -l ./cmd ./internal
cd web && pnpm test                # 前端（vitest）
```

## 目录结构

```
cmd/tinycast/            入口：配置、日志、HTTP 服务、-open
internal/config/         环境变量与 .env 解析
internal/hub/            内存状态：身份绑定、房间、观看者、SSE 订阅
internal/monitor/        巡检循环：轮询 MediaMTX、推进房间状态、回收超时
internal/mtx/            MediaMTX API 客户端（paths、query 解析）
internal/httpapi/        REST、SSE、鉴权回调、HLS/WHEP 反向代理
internal/webui/          go:embed 内嵌前端
web/                     Vue 3 + TS + Vite 前端
deploy/mediamtx/         MediaMTX 配置（宿主机 / compose 两份）
deploy/systemd/          mediamtx.service、tinycast.service、env 样例
deploy/windows/          Windows 启动脚本与说明
scripts/                 构建、开发、打包脚本
```

## 设计取舍

- **内存存储**：不引入数据库，进程重启状态清空。轻量优先，符合“单实例 + 可接受丢失”的定位。
- **鉴权放在回调里**：MediaMTX 只在推流开始时问一次后端，绑定关系与“是否已有一路推流”的判断在同一把锁内完成。
- **状态靠轮询**：没有 unpublish 回调可用，用 1 秒轮询换简单与可靠；MediaMTX 短暂故障不清空房间。
- **人数靠心跳**：HLS 会话按请求创建、数量不可靠，因此用稳定 `viewerId` + 心跳，两种通道统一计数。
- **媒体反代**：浏览器只和 8080 同源交互，HLS/WebRTC 信令由 Go 转发，避免跨域与额外暴露端口。
- **令牌哈希**：服务端只存 SHA-256，房间列表与日志都不出现明文。

## 故障排查

| 现象 | 排查方向 |
| --- | --- |
| 首页没有房间 | 看日志有无“推流鉴权通过”；没有就是推流码不对，有则是媒体未 ready（看 `/v3/paths/list`） |
| OBS 提示连接失败 | 1935 是否放通；推流码是否整段复制；同一身份是否已在推流 |
| 房间在但画面黑 | 🩺 面板看下载速率与帧率；H.265 在 Linux 浏览器需换 H.264；局域网看 UDP 8000 |
| WebRTC 通道没声音 | 正常现象，MediaMTX 不转码；改用 LL-HLS 或 HLS 通道 |
| WebRTC 通道连上但马上断开 / 没画面 | OBS 的 H.264 默认带 B 帧，MediaMTX 会直接断开会话；播放器会自动回退 LL-HLS，把「最大 B 帧数」设为 0 后可再用 WebRTC |
| HLS 延迟好几秒 | 点控制栏的「延迟 X s · 追平」；长期偏高就把 OBS 关键帧间隔改成 1 秒，或检查上传带宽是否触顶 |
| 统计面板里 RTT / 抖动 / 丢包只有「—」 | LL-HLS / HLS 是 HTTP 分片，没有这些 RTP 指标；切到「WebRTC」才显示 |
| 统计面板里码率、累计接收只有「—」 | 前端是旧版本（面板只拿到带宽估值）；换成当前版本的包，HLS 会读 hls.js 的分片加载事件 |
| 面板显示「原生播放器」且延迟好几秒 | Safari 的原生 HLS 不读 LL-HLS 分片，改成「HLS.js」；H.265 就别改 |
| Safari 上选 HLS.js 后 H.265 黑屏 | Safari 的 MSE 只解 H.264，改回「原生」 |
| 控制条一会儿就消失了 | 播放中鼠标静止 2.5 秒自动收起，动一下鼠标或点画面就会回来 |
| 人数一直是 0 | 播放器心跳失败：看 `/api/rooms/{u}/watch` 是否 4xx，`VIEWER_TIMEOUT` 是否设得太小 |
| 断流后房间不消失 | 日志有无“保留期结束，已释放房间”；`ROOM_GRACE` 是否被改大 |
| 启动报 `mediamtx_unreachable` | `MEDIAMTX_*_PROXY_TARGET` 指向的端口没起，或 MediaMTX 未启动 |

## 许可

MIT，见 [LICENSE](LICENSE)。
