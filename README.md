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
cmd/tinysim/             联调工具：用 HTTP 请求模拟 SRS 的四个回调（不含业务逻辑）
internal/config/         配置加载（环境变量 + KEY=VALUE 配置文件）
internal/logfile/        按大小滚动的日志写入器
internal/hub/            内存状态中心：推流鉴权、房间生命周期、观看会话、SSE 广播
internal/httpapi/        HTTP 路由、SSE、SRS 回调、中间件
internal/srsapi/         SRS HTTP API 客户端（客户端列表核对）
internal/janitor/        定时任务：超时释放 + 僵尸会话核对
internal/webui/          前端产物嵌入与 SPA 回退
web/                     Vue 3 + TypeScript + Vite 前端
deploy/srs/srs.conf      SRS 6 配置（RTMP + WebRTC + 回调）
deploy/systemd/          Linux systemd 单元与环境变量样例
deploy/windows/          Windows 生产运行包：env 配置、启动脚本、部署说明
scripts/                 构建、开发与 SRS 下载脚本
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

Windows 侧推荐直接使用下面「Windows 生产运行包」，它把二进制、配置、启动脚本和 SRS 放在一起，
复制到目标机器就能跑。如果机器上已经装好 Go 与 Node，也可以就地构建：

```powershell
powershell -ExecutionPolicy Bypass -File deploy\windows\build.ps1   # 构建 bin\tinycast.exe
powershell -ExecutionPolicy Bypass -File deploy\windows\run.ps1     # 读取 tinycast.env 启动
```

### Windows 生产运行包（自带 SRS 6）

Windows 机器上不需要 Go / Node / Docker / SRS：在 Linux/WSL 侧交叉编译，产出一个可整目录拷走的运行包——前端已内嵌进 `tinycast.exe`，SRS 6 以官方 Windows 构建的形式放在 `srs/` 子目录里。

```bash
./scripts/package-windows.sh     # 一条命令：构建 tinycast.exe + 准备 SRS 6 + 打成 zip
```

也可以分步执行：

```bash
./scripts/build.sh --windows     # 交叉编译 tinycast.exe，并生成包内配置与启动脚本
./scripts/fetch-srs-windows.sh   # 放入 SRS 6 官方 Windows 构建（约 42MB，不入库）
```

产物 `bin/windows-amd64/`：

| 文件 | 作用 |
| --- | --- |
| `tinycast.exe` | 业务后端，前端页面已内嵌，单文件即可运行 |
| `tinycast.env` | 全部运行配置（KEY=VALUE，逐项带注释），由 `-config` 读取 |
| `start-all.cmd` | 生产启动：先拉起 SRS，再启动 tinycast 并打开首页 |
| `start.cmd` | 只启动 tinycast（SRS 在别处时用），可传参如 `start.cmd -open` |
| `open-firewall.cmd` | 右键「以管理员身份运行」，一次性放通 TCP 1935/8080/1985 与 UDP 8000 |
| `srs/objs/srs.exe` | SRS 6.0.172 官方 Cygwin 构建，附 5 个 Cygwin 运行库（含官方包缺失的 cygiconv-2.dll、cygintl-8.dll） |
| `srs/conf/srs.conf` | 由 [deploy/srs/srs.windows.conf](deploy/srs/srs.windows.conf) 复制，四个回调指向 `http://127.0.0.1:8080/internal/srs/*` |
| `logs/`、`srs/logs/` | 运行日志目录，tinycast 日志默认 16MB 滚动、保留 3 份 |
| `部署说明.txt` | 目录说明、上机步骤、局域网/HTTPS 改造、排查表 |

上机步骤：

1. 把整个 `bin/windows-amd64/` 目录拷到 Windows 机器（例如 `D:\tinycast`）；
2. 双击 `start-all.cmd`：弹出 SRS 窗口，当前窗口运行 tinycast，浏览器自动打开 http://127.0.0.1:8080 ；
3. 首页「创建推流配置」→ 得到服务器地址 `rtmp://127.0.0.1:1935/live` 与推流码 `alex?room=房间名&token=<令牌>`；
4. OBS → 设置 → 直播 → 服务选「自定义」，填入两项后开始推流；
5. 首页立刻出现 LIVE 房间，点进去看 WebRTC 画面，在线人数随观看者增减；OBS 停止推流后房间进入 GRACE 并倒计时，5 分钟内重推恢复 LIVE，超时释放。

局域网/公网观看：包内 `candidate *` 会由 SRS 自动探测本机网卡地址，本机浏览器与局域网内其它设备一般无需改配置；云主机在 NAT 后面时要把它改成公网 IP 或域名。`tinycast.env` 里同步 `SRS_RTMP_PUBLIC_BASE`、`SRS_WEBRTC_PUBLIC_BASE`、`PUBLIC_BASE_URL`；端口用 `open-firewall.cmd`（管理员运行）一键放通 TCP 1935、TCP 8080（或 80/443）、UDP 8000。端口分工：1935/tcp RTMP、1985/tcp SRS HTTP API（仅本机）、8000/udp WebRTC、8080/tcp 页面与接口。

长期运行：`tinycast.exe` 是控制台程序，Ctrl+C 即优雅停止；需要开机自启/崩溃重启时用「任务计划程序」触发 `start-all.cmd`，或用 NSSM / WinSW 把 `tinycast.exe` 注册成服务。

SRS 的 Windows 构建取自官方 Release `v6.0-b0` 的 NSIS 安装包（sha256 固定在 [scripts/fetch-srs-windows.sh](scripts/fetch-srs-windows.sh) 中，脚本只解包，不运行安装程序、不写注册表）。官方安装包的 `objs` 目录只带三个 Cygwin 运行库，而 `cygstdc++-6.dll` 还依赖 `cygiconv-2.dll`、`cygintl-8.dll`（缺了会在 Windows 上报「由于找不到 cygiconv-2.dll，无法继续执行代码」），脚本会从 Cygwin 官方软件源补齐这两个 DLL 并做依赖闭环校验；`start-all.cmd` 启动前也会先检查这 5 个 DLL。SRS 官方把 Windows 版定位为测试用途，长期高负载生产建议用 Linux 版与 [deploy/srs/srs.conf](deploy/srs/srs.conf)。

## OBS 推流配置

在首页点「创建推流配置」，填写用户名与房间名称，浏览器本地生成令牌后展示：

| 项 | 值 |
| --- | --- |
| 服务器 | rtmp://live.example.com/live |
| 推流码 | username?room=房间名称&token=随机令牌 |

OBS「设置 → 直播 → 服务：自定义」中填入服务器与推流码即可。最终推流地址等价于 rtmp://live.example.com/live/username?room=...&token=...，SRS 会把查询串原样放进回调的 param 字段。

> ⚠️ **请整段复制页面生成的推流码，不要手改房间名。**
> 房间名里的中文会被 OBS 先解码一次，所以页面在推流码里做了双重百分号编码（OBS 解一次后线上仍是纯 ASCII）。
> 如果手工输入未编码的中文房间名，SRS 会解析失败（code=3007 HttpParseUrl）并把整段查询串并进流名，
> 结果是推流"看起来成功"、观众端却永久黑屏（页面会显示"推流码没有被服务器正确解析"）。

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
| LOG_FILE | 空 | 日志文件路径，设置后按大小滚动落盘（目录自动创建） |
| LOG_MAX_MB | 16 | 单个日志文件上限（MB） |
| LOG_KEEP | 3 | 保留的历史日志份数 |
| TINYCAST_ENV | 空 | 默认读取的配置文件路径，等价于 `-config` |
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
| SRS_STREAMS_API_URL | 由 SRS_CLIENTS_API_URL 推导 | SRS 流信息地址，用于读取推流编码与分辨率，在页面提前提示"编码放不了" |
| SRS_CLIENTS_API_USER / SRS_CLIENTS_API_PASSWORD | 空 | 访问 SRS HTTP API 的 BasicAuth |
| CORS_ALLOWED_ORIGINS | 空 | 前后端不同源时的允许来源 |
| PUBLIC_BASE_URL | 空 | 对外访问地址（用于生成绝对链接） |

后端支持用配置文件代替环境变量，方便 Windows 与系统服务部署：

```bash
tinycast -config tinycast.env   # KEY=VALUE，已存在的环境变量优先
tinycast -version               # 打印版本与提交号
```

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

更省事的方式是用 [cmd/tinysim](cmd/tinysim)（仅开发用，不进生产包，构建产物在 `bin/dev/`）：它把 SRS 会发的回调手工发一遍，不参与业务逻辑。

```bash
go run ./cmd/tinysim demo -step 0            # 推流 → 重复推流被拒 → 观众进出 → 断流保留 → 重新推流
go run ./cmd/tinysim token -user alex        # 打印一份可直接粘贴到 OBS 的推流配置
```

| 子命令 | 作用 |
| --- | --- |
| `demo` | 完整走一遍生命周期（默认每步停留 4s，`-step 0` 关闭等待） |
| `publish` / `unpublish` | 模拟 OBS 开始 / 停止推流 |
| `play` / `stop` | 模拟一个观众进入 / 离开 |
| `state` | 查询房间当前状态 |
| `token` | 打印一份可直接粘贴到 OBS 的推流配置 |

常用参数：`-base http://127.0.0.1:8080`、`-user alex`、`-room 演示直播间`、`-token <令牌>`、`-secret <回调密钥>`、`-client <客户端ID>`。这种方式没有媒体流，播放器会显示连接失败，属预期现象。

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
| 页面能打开但一直黑屏 | 见下方「画面黑屏怎么排查」 |
| 房间旁提示"推流码异常" | 推流码里的房间名被手改过或未编码，SRS 无法解析查询串；重新复制页面生成的推流码 |
| 在线人数不准 | 确认 SRS 已开启 on_play / on_stop 回调；异常断开由定时任务核对 SRS 客户端列表兜底，可配置 SRS_CLIENTS_API_URL |
| 播放 404 / CORS 报错 | 同源部署时配置 SRS_RTC_PROXY_TARGET=http://127.0.0.1:1985，由后端反代 /rtc/* |

### 画面黑屏怎么排查

SRS 6 的 `rtmp_to_rtc` **只做封装转换，不做视频转码**，浏览器能放的只有 H.264。所以"连接成功但没有画面"绝大多数是编码或 UDP 通路问题，按下面顺序看：

1. **OBS 是否用了 H.265 / HEVC**：SDP 照常协商成功，SRS 却一个视频包都不发，页面于是静默黑屏。后端会通过 SRS 的 `/api/v1/streams/` 读到实际编码，页面直接显示"主播使用 H.265 推流，浏览器无法解码"。
2. **H.264 的档次是否超前**：4:4:4、4:2:2、10bit（High 4:4:4 Predictive / High 10）浏览器同样解不了，要在 OBS 里改回 High/Main + 4:2:0 8bit。
3. **UDP 8000 是否放通**：防火墙、云安全组、Docker 的 `8000-udp` 端口映射缺一不可；HTTPS 页面配 HTTP 的 WHEP 地址会被混合内容策略拦截。
4. **candidate 是否可达**：默认 `candidate *` 由 SRS 自动探测网卡地址，本机与局域网都能连；云主机在 NAT 后面时要写公网 IP/域名，否则浏览器只会拿到内网候选地址。
5. **用播放器的 🩺 诊断面板确认**：可展开看 ICE 状态、已解码帧数、分辨率与码率。帧数一直为 0 说明是上面 1~4 之一；帧数在涨却仍然黑屏，通常是浏览器硬件解码或截图工具的问题。
6. **推流码里的房间名是否手改过**：SRS 解析不了含未编码中文的查询串（`Ignore parse url=... err code=3007(HttpParseUrl)`），会把查询串并进流名，播放端按干净名字请求永远拿不到媒体。这类房间会带 `warning=stream_unparsed`，页面顶部与房间卡片都会给出提示，让主播重新复制推流码即可。

页面上的提示来自后端的 `media` 字段（`videoCodec`/`videoProfile`/`width`/`height` 等）与播放器 `getStats()` 的实时统计，后者在连续 6 秒收不到任何视频帧时触发，不必等页面报错。

## 许可

见 [LICENSE](./LICENSE)。
