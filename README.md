# TinyCast

>**前言**  
>一个几年前的小想法，通过Vibe Coding的形式创作出来  
>你可以像使用直播平台一样使用这个项目在本地搭建临时直播间用于本地测试或是分享    

轻量直播推流平台：**OBS 推流 → 浏览器直接观看**，双击就能跑。

- 无需注册登录、无需数据库，业务状态全在 Go 进程内存里，进程退出即清空
- **MediaMTX** 负责 RTMP 接入与 HLS / WebRTC 分发，Go 只做推流鉴权、房间生命周期与在线人数
- 前端 Vue 3 + TypeScript + Vite，播放器默认 LL-HLS（有声音、支持 H.265），可在 LL-HLS / HLS / WebRTC 三个通道间切换
- 推流身份（用户名 + 令牌）由后端严格校验，令牌只存 SHA-256 哈希
- 单个 Go 二进制内嵌全部前端资源，部署只有两个进程：mediamtx + tinycast

## 快速开始

三种方式任选一种，Windows 最省事。

### 1. Windows（推荐）

1. 拿到 `tinycast-<版本>-windows-amd64.zip`（发布包，见下文「发布与版本」），解压到本地磁盘；
2. 双击 `start-all.cmd`，浏览器自动打开 <http://127.0.0.1:18855>。

要从其它设备（局域网或公网）观看时，再右键 `open-firewall.cmd` → 以管理员身份运行放通端口；
只在本机看不需要这一步。

接着在首页点「创建推流配置」，把生成的服务器地址与推流码整段粘进 OBS 就能开播。

### 2. Linux

```bash
tar -xzf tinycast-*-linux-amd64.tar.gz
cd tinycast-*-linux-amd64
./start.sh          # MediaMTX 与 TinyCast 一起跑，Ctrl+C 退出
```

### 3. Docker

```bash
cp .env.example .env
# 必填：生成共享密钥写入 .env（先删旧行，重复执行不会堆两行）
sed -i '/^MEDIAMTX_AUTH_SECRET=/d' .env
printf 'MEDIAMTX_AUTH_SECRET=%s\n' "$(openssl rand -hex 32)" >> .env
docker compose up -d --build
```

浏览器访问 `http://<服务器地址>:18855`。

> `MEDIAMTX_AUTH_SECRET` 是 compose 的必填变量（`${MEDIAMTX_AUTH_SECRET:?...}`）：`.env` 缺失或该值为空时，
> `docker compose` 的任何子命令（含 `down` / `logs`）都会先报错退出——先补 `.env` 再操作。
>
> 对外提供服务时，把 `webrtcAdditionalHosts` 与 `MEDIAMTX_RTMP_PUBLIC_BASE` 改成真实地址，
> 并放通 TCP 1935 / 18855 与 UDP 8000。

> `docker-compose.yml` 固定了 `HTTP_ADDR`、`INTERNAL_ADDR`、`MEDIAMTX_APP`、`MEDIAMTX_API_URL`、HLS/WHEP 上游，
> `.env` 里的同名键不会覆盖它们；需要改这些值时直接改 compose 文件。

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
| 播放器控制 | 控制栏右侧依次是刷新、设置、统计、音量条、小窗、网页全屏、全屏（双击画面亦可全屏）；音量条可拖动，喇叭按钮静音 / 恢复，Esc 退出网页全屏；鼠标静止 2.5 秒控制条自动收起 |
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
                      Go 后端 tinycast :18855
                      ├── /internal/mtx/auth  推流鉴权（仅本机）
                      ├── 轮询 /v3/paths/list 判断推流开/停 → 房间状态
                      ├── /api/*              房间、配置、心跳
                      ├── /api/events         SSE 快照推送
                      ├── /hls/*  /rtc/*      反代到 MediaMTX（浏览器只跟 Go 同源交互）
                      └── /                   内嵌前端（go:embed）
```

两个进程可以同机也可以容器编排。浏览器全程只访问 18855，媒体端口无需对外暴露（WebRTC 的 UDP 8000 除外）。

## 发布与版本

版本号只有一个来源：git tag（`v0.1.0` 这种形式）。构建脚本用 `git describe` 取值，再通过
`-ldflags -X` 写进二进制，所以每个产物都能自己报出版本。

```bash
./scripts/package-release.sh      # Linux 包 + Windows 包 + 校验和，全部落在 dist/
```

| 产物 | 说明 |
| --- | --- |
| `dist/tinycast-<版本>-linux-amd64.tar.gz` | tinycast + MediaMTX + 配置样例 + systemd 单元与 env 样例 + `start.sh` |
| `dist/tinycast-<版本>-windows-amd64.zip` | 双击 `start-all.cmd` 即用，见包内 `部署说明.txt` |
| `dist/SHA256SUMS` | 只包含上面两个产物的校验和，`sha256sum -c SHA256SUMS` 验证 |

两个包内都带 `LICENSE`、`README.md` 与 `THIRD-PARTY-NOTICES.md`，MediaMTX 的 MIT 许可副本在 `mediamtx/LICENSE`。

查询版本：

```bash
./tinycast -version         # 发布包解压后；例如 v0.1.1 (rev …, <构建时间>, go1.27.3)
./bin/tinycast -version     # 从源码构建后；非 tag 提交会带 -N-g<sha> 后缀
curl -s localhost:18855/api/version
```

工作区有未提交改动时版本会带 `-dirty` 后缀，发布前请确认 `git status` 干净。
本机发布包用本机的 Go 构建（示例里是 go1.27.3），Docker 镜像固定用 `golang:1.24`，两者版本字符串会不同，功能一致。
Docker 镜像里没有 `.git`，版本由 compose 的 `TINYCAST_VERSION` / `TINYCAST_COMMIT` / `TINYCAST_BUILD_TIME` 注入。

## 手动部署（Linux）

发布包里已经带了 MediaMTX、配置样例、systemd 单元与 env 样例（解压后 `./start.sh` 可直接跑）。
`start.sh` 用的是根目录 `tinycast.env`；装成 systemd 服务时请用 `systemd/tinycast.env.example`，
它把 `LOG_FILE` 写成绝对路径 `/var/log/tinycast/tinycast.log`。下面是从源码手工部署的步骤。

```bash
# MediaMTX：官方单文件程序
curl -L -o /tmp/mtx.tar.gz https://github.com/bluenviron/mediamtx/releases/download/v1.21.3/mediamtx_v1.21.3_linux_amd64.tar.gz
sudo tar -xzf /tmp/mtx.tar.gz -C /usr/local/bin mediamtx
sudo install -d /etc/mediamtx && sudo install -m 644 deploy/mediamtx/mediamtx.yml /etc/mediamtx/mediamtx.yml

# TinyCast：自带前端
./scripts/build.sh
sudo install -m 755 bin/tinycast /usr/local/bin/tinycast
sudo install -d /etc/tinycast && sudo install -m 640 deploy/systemd/tinycast.env.example /etc/tinycast/tinycast.env

# systemd
sudo install -m 644 deploy/systemd/mediamtx.service deploy/systemd/tinycast.service /etc/systemd/system/
sudo useradd -r -s /usr/sbin/nologin mediamtx -d /var/lib/mediamtx   # 首次
sudo useradd -r -s /usr/sbin/nologin tinycast
# 日志目录由 unit 的 LogsDirectory=tinycast 自动创建，无需手工 mkdir
sudo systemctl daemon-reload && sudo systemctl enable --now mediamtx tinycast
```

改 `/etc/tinycast/tinycast.env` 后 `sudo systemctl restart tinycast` 生效。
`tinycast.service` 的 `WorkingDirectory` 是 `/var/lib/tinycast`，`LogsDirectory=tinycast` 会创建并授权 `/var/log/tinycast`；
env 样例里的 `LOG_FILE` 就是绝对路径。若改成相对路径，日志会落在工作目录下（同样可写）。

## 打包 Windows 运行包

```bash
./scripts/package-windows.sh     # 产出 dist/tinycast-<版本>-windows-amd64.zip（含 mediamtx.exe）
```

包内是免安装的完整运行环境（TinyCast + MediaMTX），用法见上面的快速开始；
详细步骤与排查见包内 `部署说明.txt`。

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

回调默认只接受本机来源（`MEDIAMTX_AUTH_ALLOWED_CIDRS`），并可加 `?secret=` 共享密钥，
因此外网无法直接调用它伪造身份。如果 tinycast 前面还有 nginx / Caddy 之类的反向代理，
请在代理上拒绝 `/internal/` 路径并配置 `MEDIAMTX_AUTH_SECRET`；`TRUSTED_PROXIES` 只用来
采信 `X-Forwarded-For`，不能替代这两步。XFF 必须由代理覆写（nginx 用 `$remote_addr`），
不能追加客户端自带的值，否则来源 IP 可伪造，限流与每 IP 配额会失效。

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
- 每秒量一次「直播边缘 − 当前播放位置」，低延时档**落后超过 1.8 秒就自动跳到当前档位的目标延迟位置**（约 1.2 秒，6 秒内最多跳一次，避免频繁跳）；
- `maxLiveSyncPlaybackRate: 1.5`：差得不多时用 1.5 倍速悄悄追上，不跳画面；
- 需要立刻回到当前档位的目标延迟时，点控制栏的 ↻（刷新播放）重建播放器：HLS.js 下它会回到「直播边缘 − 当前档位目标延迟」的位置
 （稳定档约 3 秒、低延时档约 1.2 秒），不是严格贴着边缘；🩺 面板里的「直播延迟」显示当前落后多少；
- 「缓冲余量」是 MSE 里排在播放位置后面的数据量，它比延迟大一点是正常的，别把它当延迟。

实测（Edge + 本项目 `/hls/` 反代，1280×720@60，低延时档）：OBS 默认关键帧 4 秒时直播延迟 **1.2 秒**，
关键帧 1 秒时 **0.6 秒**。把 OBS 的**关键帧间隔**设成 1 秒（输出 → 高级）可以再稳一点，
分片本身在内存里，不落盘。

### 播放参数卡片：延迟档位与播放方式

控制栏的 **⚙** 打开「播放参数」卡片（收在播放器下方，不遮挡画面）。这里只放两件与观看体验有关的事：
延迟档位和播放方式；通道切换仍然在播放器控制栏上。

| 档位 | 目标延迟 | 前向缓冲 | 主动追平 | 适合 |
| --- | --- | --- | --- | --- |
| 低延时 | 1.2 秒 | 4 秒 | 落后 >1.8 秒跳到目标延迟位置，差得少时 1.5 倍速追 | 网络好、一直盯着看 |
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
| GET | `/api/version` | 版本信息（版本、提交、构建时间、Go 版本） |
| GET | `/api/config` | 前端启动配置（app、RTMP 地址、保留秒数、可用通道） |
| GET | `/api/rooms` | 房间列表（只含 LIVE 与 GRACE） |
| GET | `/api/rooms/{username}` | 房间详情 |
| GET | `/api/rooms/{username}/play` | 播放信息：`hlsUrl` 与 `whepUrl`（仅 LIVE） |
| POST | `/api/rooms/{username}/watch` | 观看心跳，`{"viewerId":"..."}` |
| DELETE | `/api/rooms/{username}/watch?viewerId=...` | 离开 |
| GET | `/api/events` | SSE 快照流（`event: rooms`） |
| POST | `/internal/mtx/auth` | MediaMTX 回调，只注册在 `INTERNAL_ADDR`（Windows/Linux 发布包默认 `127.0.0.1:18856`；compose 里容器内是 `0.0.0.0:18856`，不映射到宿主机；未配置时与 `HTTP_ADDR` 共用端口） |
| * | `/hls/*`、`/rtc/*` | 反代到 MediaMTX 的 HLS / WHEP（含 302 改写） |

在线人数 = 该房间在 `VIEWER_TIMEOUT` 内有过心跳的 `viewerId` 数量。
重复心跳天然幂等，关闭页面时前端会主动 `DELETE`。断流进入 GRACE 时人数立即清零，
此时心跳返回 409 `room_not_live`，前端停止上报并在恢复 LIVE 后自动重新开始。

同一房间、同一客户端最多保留 16 个 `viewerId`；超过时心跳返回 429 `viewer_limit`，
房间总人数达到上限时返回 429 `room_full`。两者都只暂停当前标签页的人数统计，
播放不受影响，前端会按 `Retry-After` 自动重试。

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `HTTP_ADDR` | `:18855` | 公网监听地址 |
| `INTERNAL_ADDR` | 空 | 只给 MediaMTX 回调用的内部监听地址。设置后公网端口不再注册 `/internal/` 路由；留空表示与 `HTTP_ADDR` 共用。显式配成与 `HTTP_ADDR` 相同的监听地址会拒绝启动。Docker 与两个发布包默认启用 `18856` |
| `PUBLIC_BASE_URL` | 空 | 对外地址，用于播放地址前缀；留空按浏览器地址推断 |
| `TRUSTED_PROXIES` | 127.0.0.0/8,::1/128 | 只有这些来源的 X-Forwarded-For 才被采信；反代在别的网段时按实际填写。反向代理必须**覆写** XFF，不能把客户端自带的值追加进去 |
| `LOG_LEVEL` / `LOG_CONSOLE_LEVEL` / `LOG_FILE` / `LOG_MAX_MB` / `LOG_KEEP` | `error` / 跟随 `LOG_LEVEL` / 空 / `16` / `3` | 默认只记录 error；排查时把 `LOG_LEVEL` 设为 info/debug。`LOG_CONSOLE_LEVEL` 留空跟随 `LOG_LEVEL`。Windows/Linux 的启动脚本会在每次启动时检查 MediaMTX 日志，超过 16 MB 就滚动并保留三份 |
| `ROOM_GRACE` | `5m` | 断流保留时长 |
| `MONITOR_INTERVAL` | `1s` | 轮询 MediaMTX 路径的间隔 |
| `VIEWER_TIMEOUT` | `30s` | 心跳超时 |
| `PENDING_TIMEOUT` | `60s` | 鉴权通过但未真正推流的绑定保留时长 |
| `MEDIAMTX_APP` | `live` | 推流 app 名（RTMP 地址里的 `/live`） |
| `MEDIAMTX_API_URL` | `http://127.0.0.1:9997` | 路径状态 API |
| `MEDIAMTX_API_USER` / `MEDIAMTX_API_PASSWORD` | 空 | 仅当 MediaMTX 通过 `authInternalUsers` 保护 API 时填写；默认配置未启用 |
| `MEDIAMTX_AUTH_SECRET` | 空（docker compose 必填） | 回调共享密钥，需与 mediamtx.yml 的 `?secret=` 一致 |
| `MEDIAMTX_AUTH_ALLOWED_CIDRS` | 127.0.0.0/8,::1/128 | 允许回调的来源网段；Docker 需显式放开容器网段 |
| `MEDIAMTX_HLS_PROXY_TARGET` | `http://127.0.0.1:8888` | `/hls/` 上游 |
| `MEDIAMTX_WHEP_PROXY_TARGET` | `http://127.0.0.1:8889` | `/rtc/` 上游 |
| `MEDIAMTX_RTMP_PUBLIC_BASE` | `rtmp://localhost:1935/live` | 展示给主播的服务器地址 |
| `CORS_ALLOWED_ORIGINS` | 空 | 前后端分离时才需要 |
| `TINYCAST_ENV` | 空 | 默认读取的环境变量文件；`-config` 优先于它，`-addr` 优先于 `HTTP_ADDR` |

时长支持 `90s`、`5m`、`2m30s` 或纯秒数 `300`。

`INTERNAL_ADDR` 若配成非回环地址（例如容器里的 `0.0.0.0:18856`），必须同时设置 `MEDIAMTX_AUTH_SECRET`；
否则同网段任何人都能伪造推流鉴权，这种组合会让启动直接失败并提示改用回环地址或补上密钥。

显式把 `INTERNAL_ADDR` 配成与 `HTTP_ADDR` 相同的监听地址（包括 `:18855`、`0.0.0.0:18855`、`[::]:18855` 这类等价写法）
会把 `/internal/` 回调重新挂回公网端口，启动会直接失败；要共用端口只能留空 `INTERNAL_ADDR`。

回调用独立端口时，`mediamtx.yml` 的 `authHTTPAddress` 必须指向 `INTERNAL_ADDR`（默认 `18856`）；
两边不一致时 tinycast 启动会打 ERROR 提示。用同机反向代理把整站转给 tinycast 时，
请设置 `TRUSTED_PROXIES` 并配置 `MEDIAMTX_AUTH_SECRET`，同时在代理上拒绝 `/internal/`，否则公网请求可能落到回调上。

反向代理示例（nginx）：`proxy_set_header X-Forwarded-For $remote_addr;`，不要用 `$proxy_add_x_forwarded_for`——
后者会把客户端自带的 XFF 追加进来，攻击者可以伪造来源 IP 绕过限流与每 IP 配额。

## 本地开发

```bash
source scripts/dev-env.sh          # 把工具链指到仓库内的 .tools
./scripts/dev.sh                   # 同时启动 go run 与 pnpm dev

# 构建与版本
./scripts/build.sh                 # 构建到 bin/tinycast，版本取自 git describe
./bin/tinycast -version

# 单独跑（回调端口要与 mediamtx.yml 的 authHTTPAddress 一致）
INTERNAL_ADDR=127.0.0.1:18856 go run ./cmd/tinycast -open
cd web && pnpm dev                 # 前端 :5173，代理 /api 与 /rtc 到后端

# MediaMTX（本地验证推流链路）
./bin/linux-amd64/mediamtx/mediamtx deploy/mediamtx/mediamtx.yml   # 需先放好前端产物；先运行 scripts/fetch-mediamtx-linux.sh
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
internal/version/        版本信息（构建期注入 + VCS 兜底）
internal/webui/          go:embed 内嵌前端
web/                     Vue 3 + TS + Vite 前端
deploy/mediamtx/         MediaMTX 配置（宿主机 / compose 两份）
deploy/systemd/          mediamtx.service、tinycast.service、env 样例
deploy/windows/          Windows 启动脚本与说明
deploy/linux/            Linux 发布包的 start.sh 与 env 样例
scripts/                 构建、开发、打包脚本（package-release.sh 一键出包）
THIRD-PARTY-NOTICES.md   第三方组件许可与版权（随发布包分发）
dist/                    发布产物（不入库）
```

## 设计取舍

- **内存存储**：不引入数据库，进程重启状态清空。轻量优先，符合“单实例 + 可接受丢失”的定位。
- **鉴权放在回调里**：MediaMTX 只在推流开始时问一次后端，绑定关系与“是否已有一路推流”的判断在同一把锁内完成。
- **状态靠轮询**：没有 unpublish 回调可用，用 1 秒轮询换简单与可靠；MediaMTX 短暂故障保持现状，持续失败才降级为断流保留，降级期间不回收绑定。
- **人数靠心跳**：HLS 会话按请求创建、数量不可靠，因此用稳定 `viewerId` + 心跳，两种通道统一计数。
- **媒体反代**：浏览器只和 18855 同源交互，HLS/WebRTC 信令由 Go 转发，避免跨域与额外暴露端口；全局 512 并发之外还有单 IP 32 条的上限。
- **令牌哈希**：服务端只存 SHA-256，房间列表与日志都不出现明文。
- **默认安静**：终端与 `LOG_FILE` 默认只记录 error，正常运行时不会刷屏；排查时把 `LOG_LEVEL` 改成 info/debug，`LOG_CONSOLE_LEVEL` 留空会跟着变。MediaMTX 的 `logLevel` 也默认 `error`，需要看媒体细节时改 `deploy/mediamtx/mediamtx.yml`。

## 故障排查

| 现象 | 排查方向 |
| --- | --- |
| 首页没有房间 | 看日志有无“推流鉴权通过”；没有就是推流码不对，有则是媒体未 ready（看 `/v3/paths/list`） |
| OBS 提示连接失败 | 1935 是否放通；推流码是否整段复制；同一身份是否已在推流 |
| 房间在但画面黑 | 🩺 面板看下载速率与帧率；H.265 在 Linux 浏览器需换 H.264；局域网看 UDP 8000 |
| WebRTC 通道没声音 | 正常现象，MediaMTX 不转码；改用 LL-HLS 或 HLS 通道 |
| WebRTC 通道连上但马上断开 / 没画面 | OBS 的 H.264 默认带 B 帧，MediaMTX 会直接断开会话；播放器会自动回退 LL-HLS，把「最大 B 帧数」设为 0 后可再用 WebRTC |
| HLS 延迟好几秒 | 点控制栏的 ↻（刷新播放）重建播放器，回到当前档位的目标延迟（稳定 3 秒 / 低延时 1.2 秒）；长期偏高就把 OBS 关键帧间隔改成 1 秒，或检查上传带宽是否触顶 |
| 统计面板里 RTT / 抖动 / 丢包只有「—」 | LL-HLS / HLS 是 HTTP 分片，没有这些 RTP 指标；切到「WebRTC」才显示 |
| 统计面板里码率、累计接收只有「—」 | 前端是旧版本（面板只拿到带宽估值）；换成当前版本的包，HLS 会读 hls.js 的分片加载事件 |
| 面板显示「原生播放器」且延迟好几秒 | Safari 的原生 HLS 不读 LL-HLS 分片，改成「HLS.js」；H.265 就别改 |
| Safari 上选 HLS.js 后 H.265 黑屏 | Safari 的 MSE 只解 H.264，改回「原生」 |
| 控制条一会儿就消失了 | 播放中鼠标静止 2.5 秒自动收起，动一下鼠标或点画面就会回来 |
| 人数一直是 0 | 播放器心跳失败：看 `/api/rooms/{u}/watch` 是否 4xx，`VIEWER_TIMEOUT` 是否设得太小 |
| 断流后房间不消失 | 日志有无“保留期结束，已释放房间”；`ROOM_GRACE` 是否被改大 |
| 启动报 `mediamtx_unreachable` | `MEDIAMTX_*_PROXY_TARGET` 指向的端口没起，或 MediaMTX 未启动 |

## 许可

TinyCast 本身以 MIT 许可发布，见 [LICENSE](LICENSE)。

发布包与 Docker 镜像里还带着第三方组件，各自的版权与许可见 [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md)：

- **MediaMTX**（MIT）：随包分发的 `mediamtx` 二进制，许可副本在包内 `mediamtx/LICENSE`；
- **hls.js**（Apache-2.0）：打包进前端 bundle，bundle 顶部保留了归属声明；
- **Vue / vue-router**（MIT）：同上；
- **Go 标准库**（BSD-3-Clause）：静态链接进后端二进制。

Docker 镜像内的许可文件位于 `/usr/share/doc/tinycast/`。
