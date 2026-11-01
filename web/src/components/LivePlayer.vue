<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { fetchPlay } from '../api'
import { collectSample, emptySample, sparklinePath } from '../lib/stats'
import type { StatsSample } from '../lib/stats'
import { codecLabel, describeMedia, mediaIssue, noFrameHint } from '../lib/playback'
import type { MediaInfo, Playback, RoomView } from '../types'

/**
 * 双通道播放器。
 *
 * 为什么不只是 WebRTC：MediaMTX 不做转码，浏览器通过 WebRTC 基本只能收 H.264，
 * 而且是照原样转封装——AAC 音频在 WebRTC 里没有对应编码可协商，所以没声音。
 * HLS 走 fMP4 分片，H.264 / H.265 / AV1 都能带，声音也正常，代价是延迟约 1 秒。
 *
 * 因此默认用 HLS（能看、有声音、支持更多编码），需要压延迟时再切到 WebRTC。
 */

type PlayMode = 'hls' | 'webrtc'
type Phase = 'connecting' | 'playing' | 'error'

const props = defineProps<{ room: RoomView; media?: MediaInfo | null }>()

const videoEl = ref<HTMLVideoElement | null>(null)
const stageEl = ref<HTMLDivElement | null>(null)

const phase = ref<Phase>('connecting')
const errorText = ref('')
const overlayNote = ref('')
const stallNote = ref('')
const connState = ref('')
const MODE_KEY = 'tinycast.playMode'

/** 记住用户上次选的通道；没选过就用默认的 HLS（有声音、支持 H.265 与 B 帧）。 */
function loadMode(): PlayMode {
  try {
    return localStorage.getItem(MODE_KEY) === 'webrtc' ? 'webrtc' : 'hls'
  } catch {
    return 'hls'
  }
}

function saveMode(next: PlayMode): void {
  try {
    localStorage.setItem(MODE_KEY, next)
  } catch {
    // 隐私模式下 localStorage 不可写，忽略即可
  }
}

const mode = ref<PlayMode>(loadMode())
const playback = ref<Playback | null>(null)

const muted = ref(true)
const theater = ref(false)
const showStats = ref(false)
/** 距离直播边缘的秒数：hls.js 直接给出，原生 HLS 用 seekable 末端估算。 */
const latencySec = ref(0)
/** 从低延迟通道自动回退到 HLS 时的说明条。 */
const autoSwitchNote = ref('')

const HISTORY_LIMIT = 60

const METRICS = [
  { key: 'kbps', label: '下载速率' },
  { key: 'fps', label: '帧率' },
  { key: 'bufferMs', label: '缓冲延迟' },
  { key: 'rttMs', label: 'RTT' },
  { key: 'lossRate', label: '丢包率' },
] as const

type MetricKey = (typeof METRICS)[number]['key']

const metric = ref<MetricKey>('kbps')
const sample = ref<StatsSample | null>(null)
const history = ref<StatsSample[]>([])

let generation = 0
let pc: RTCPeerConnection | null = null
interface HlsHandle {
  destroy: () => void
  startLoad: () => void
  recoverMediaError: () => void
  /** hls.js 给出的“当前播放位置距直播边缘”的秒数 */
  latency?: number
  /** hls.js 认为应该起播/追平的位置 */
  liveSyncPosition?: number
  bandwidthEstimate?: number
  levels?: { bitrate?: number }[]
}

let hls: HlsHandle | null = null
let monitorTimer: ReturnType<typeof setInterval> | null = null
let watchdogTimer: ReturnType<typeof setTimeout> | null = null
let retryTimer: ReturnType<typeof setTimeout> | null = null
let lastFrameAt = 0
let lastFrameCount = 0
/** HLS 侧的累计丢帧数：getVideoPlaybackQuality 给的是累计值，需要自己差分。 */
let droppedTotal = 0
/** WebRTC 起播时刻，用来判断“连上很久却一帧都没有”。 */
let webrtcSince = 0
/** 同一次播放里只自动回退一次，避免通道来回跳。 */
let fallbackTried = false

/** 后端至少给出一个通道时，才显示通道切换（只有一侧可用时另一个按钮置灰）。 */
const hasModes = computed(() => {
  const p = playback.value
  return Boolean(p?.hlsUrl || p?.whepUrl)
})

const canSwitch = computed(() => {
  const p = playback.value
  return Boolean(p?.hlsUrl && p?.whepUrl)
})

const issue = computed(() => mediaIssue(props.media, mode.value))

const chartPath = computed(() => {
  const values = history.value.map((item) => valueOf(item, metric.value))
  return sparklinePath(values, 260, 64)
})

const currentValue = computed(() => {
  const last = sample.value
  return last ? formatMetric(metric.value, valueOf(last, metric.value)) : '—'
})

const peakValue = computed(() => {
  if (!history.value.length) return '—'
  const values = history.value.map((item) => valueOf(item, metric.value))
  return formatMetric(metric.value, Math.max(...values))
})

const grid = computed(() => {
  const s = sample.value
  const media = props.media
  return [
    { label: '播放通道', value: mode.value === 'hls' ? 'HLS' : 'WebRTC' },
    { label: '视频编码', value: labelOrDash(media?.videoCodec) },
    { label: '音频编码', value: labelOrDash(media?.audioCodec) },
    { label: '分辨率', value: resolution(s) },
    { label: '播放帧率', value: s && s.fps > 0 ? s.fps.toFixed(1) + ' fps' : '—' },
    { label: '视频码率', value: s && s.videoKbps > 0 ? formatKbps(s.videoKbps) : '—' },
    { label: '音频码率', value: s && s.audioKbps > 0 ? formatKbps(s.audioKbps) : '—' },
    { label: '丢包/丢帧', value: s ? (s.lossRate > 0 ? (s.lossRate * 100).toFixed(2) + '%' : '0%') : '—' },
    { label: '抖动', value: s && s.jitterMs > 0 ? s.jitterMs + ' ms' : '—' },
    { label: '缓冲延迟', value: s && s.bufferMs > 0 ? s.bufferMs + ' ms' : '—' },
    { label: '直播延迟', value: latencySec.value > 0 ? latencySec.value.toFixed(1) + ' s' : '—' },
    { label: '往返时延', value: s && s.rttMs > 0 ? s.rttMs + ' ms' : '—' },
    { label: '累计接收', value: s && s.receivedBytes > 0 ? formatBytes(s.receivedBytes) : '—' },
    { label: '解码帧', value: s && s.framesDecoded > 0 ? formatNumber(s.framesDecoded) : '—' },
    { label: '连接状态', value: mode.value === 'hls' ? (phase.value === 'playing' ? '播放中' : '缓冲中') : labelOrDash(connState.value) },
  ]
})

function resolution(s: StatsSample | null): string {
  if (s && s.width && s.height) return s.width + '×' + s.height
  const media = props.media
  if (media?.width && media.height) return media.width + '×' + media.height
  return '—'
}

function labelOrDash(value?: string | null): string {
  return value ? codecLabel(value) || value : '—'
}

function valueOf(item: StatsSample, key: MetricKey): number {
  switch (key) {
    case 'kbps':
      return item.videoKbps + item.audioKbps
    case 'fps':
      return item.fps
    case 'bufferMs':
      return item.bufferMs
    case 'rttMs':
      return item.rttMs
    case 'lossRate':
      return item.lossRate * 100
    default:
      return 0
  }
}

function formatMetric(key: MetricKey, value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '—'
  if (key === 'kbps') return formatKbps(value)
  if (key === 'fps') return value.toFixed(1) + ' fps'
  if (key === 'lossRate') return value.toFixed(2) + '%'
  return Math.round(value) + ' ms'
}

function formatKbps(kbps: number): string {
  if (kbps >= 1000) return (kbps / 1000).toFixed(2) + ' Mbps'
  return Math.round(kbps) + ' kbps'
}

function formatBytes(bytes: number): string {
  if (bytes >= 1024 * 1024) return (bytes / 1024 / 1024).toFixed(1) + ' MB'
  if (bytes >= 1024) return (bytes / 1024).toFixed(0) + ' KB'
  return bytes + ' B'
}

function formatNumber(value: number): string {
  const rounded = Math.round(value * 100) / 100
  return String(rounded)
}

function teardown(): void {
  if (monitorTimer !== null) {
    clearInterval(monitorTimer)
    monitorTimer = null
  }
  if (watchdogTimer !== null) {
    clearTimeout(watchdogTimer)
    watchdogTimer = null
  }
  if (retryTimer !== null) {
    clearTimeout(retryTimer)
    retryTimer = null
  }
  if (hls) {
    try {
      hls.destroy()
    } catch {
      // 忽略销毁失败
    }
    hls = null
  }
  if (pc) {
    try {
      pc.onconnectionstatechange = null
      pc.ontrack = null
      pc.close()
    } catch {
      // 忽略关闭失败
    }
    pc = null
  }
  const video = videoEl.value
  if (video) {
    video.pause()
    video.removeAttribute('src')
    video.srcObject = null
    video.load()
  }
  connState.value = ''
  overlayNote.value = ''
  stallNote.value = ''
}

function fail(message: string): void {
  phase.value = 'error'
  errorText.value = message
}

async function start(): Promise<void> {
  teardown()
  const token = ++generation
  phase.value = 'connecting'
  errorText.value = ''
  sample.value = null
  history.value = []
  lastFrameAt = 0
  lastFrameCount = 0
  droppedTotal = 0
  latencySec.value = 0
  webrtcSince = 0

  try {
    const data = await fetchPlay(props.room.username)
    if (token !== generation) return
    playback.value = data.playback ?? null

    const p = data.playback
    if (!p || p.protocol === 'none') {
      fail('服务器没有配置播放代理：后端需要设置 MEDIAMTX_HLS_PROXY_TARGET 或 MEDIAMTX_WHEP_PROXY_TARGET。')
      return
    }

    if (mode.value === 'hls' && !p.hlsUrl && p.whepUrl) mode.value = 'webrtc'
    if (mode.value === 'webrtc' && !p.whepUrl && p.hlsUrl) mode.value = 'hls'
    if (mode.value === 'hls' && !p.hlsUrl && !p.whepUrl) mode.value = 'webrtc'

    if (mode.value === 'hls' && !p.hlsUrl) mode.value = 'webrtc'
    if (mode.value === 'webrtc' && !p.whepUrl) mode.value = 'hls'

    await load(token)
  } catch (err) {
    if (token !== generation) return
    fail(err instanceof Error ? err.message : '获取播放地址失败')
  }
}

async function load(token: number): Promise<void> {
  if (mode.value === 'hls') await startHLS(token)
  else await startWebRTC(token)
  if (token !== generation) return
  startMonitor(token)
  armWatchdog(token)
}

async function startHLS(token: number): Promise<void> {
  const url = playback.value?.hlsUrl
  const video = videoEl.value
  if (!url || !video) {
    fail('服务器没有给出 HLS 播放地址，请检查后端的 MEDIAMTX_HLS_PROXY_TARGET。')
    return
  }

  video.muted = muted.value

  // Safari / iOS 原生支持 HLS，直接用 <video>，省掉 hls.js 与 MSE。
  if (video.canPlayType('application/vnd.apple.mpegurl')) {
    video.src = url
    video.addEventListener(
      'loadedmetadata',
      () => {
        seekLive()
        void video.play().catch(() => {})
      },
      { once: true },
    )
    phase.value = 'playing'
    return
  }

  let Hls: typeof import('hls.js').default | null = null
  try {
    const mod = await import('hls.js')
    Hls = (mod.default ?? mod) as typeof import('hls.js').default
  } catch {
    fail('HLS 播放库加载失败，请刷新页面重试。')
    return
  }
  if (token !== generation) return

  if (!Hls || !Hls.isSupported()) {
    fail('当前浏览器不支持 HLS 播放（缺少 MSE），请换用 Chrome / Edge，或切换到 WebRTC。')
    return
  }

  // hls.js 默认落后 3 个分片起播；MediaMTX 的分片对齐 OBS 关键帧（常见 2 秒），
  // 于是观众一开始就落后 6 秒。这里压到 1 片、放宽追平速率，让它贴着直播边缘播。
  const instance = new Hls({
    lowLatencyMode: true,
    liveSyncDurationCount: 1,
    liveMaxLatencyDurationCount: 4,
    maxLiveSyncPlaybackRate: 1.5,
    backBufferLength: 10,
    maxBufferLength: 6,
    enableWorker: true,
  })
  hls = instance as unknown as HlsHandle

  instance.on(Hls.Events.MANIFEST_PARSED, () => {
    seekLive()
    void video.play().catch(() => {})
    phase.value = 'playing'
  })

  instance.on(Hls.Events.ERROR, (_event, data) => {
    if (!data.fatal) return
    if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
      instance.startLoad()
      return
    }
    if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
      instance.recoverMediaError()
      return
    }
    fail('HLS 播放失败：' + data.details + '。' + noFrameHint(props.media, 'hls'))
  })

  instance.loadSource(url)
  instance.attachMedia(video)
}

async function startWebRTC(token: number): Promise<void> {
  const url = playback.value?.whepUrl
  if (!url) {
    fail('服务器没有给出 WebRTC 信令地址，请检查后端的 MEDIAMTX_WHEP_PROXY_TARGET。')
    return
  }

  const Ctor =
    window.RTCPeerConnection ??
    (window as unknown as { webkitRTCPeerConnection?: typeof RTCPeerConnection }).webkitRTCPeerConnection
  if (!Ctor) {
    fail('当前浏览器不支持 WebRTC，请切换到 HLS 播放。')
    return
  }

  const conn = new Ctor({ bundlePolicy: 'max-bundle' })
  pc = conn
  webrtcSince = Date.now()

  conn.addTransceiver('video', { direction: 'recvonly' })
  // 音频轨也留一条：MediaMTX 在没有音频时会补一条 PCMU 假轨，
  // 客户端 offer 里完全没有 audio 段时协商会失败。
  conn.addTransceiver('audio', { direction: 'recvonly' })

  conn.ontrack = (event) => {
    const video = videoEl.value
    if (!video) return
    const stream = event.streams[0] ?? new MediaStream([event.track])
    video.srcObject = stream
    video.muted = muted.value
    void video.play().catch(() => {})
  }

  conn.onconnectionstatechange = () => {
    const state = conn.connectionState
    connState.value = state
    if (state === 'connected') {
      if (phase.value !== 'error') phase.value = 'playing'
    } else if (state === 'failed' || state === 'closed') {
      if (autoFallbackFromWebRTC('WebRTC 会话被媒体服务关闭')) return
      fail('WebRTC 连接失败，通常是 UDP 8000 被防火墙拦住，或 MediaMTX 的 webrtcAdditionalHosts 配置不对。')
    } else if (state === 'disconnected') {
      stallNote.value = 'WebRTC 连接中断，正在尝试恢复…'
    }
  }

  try {
    const offer = await conn.createOffer()
    await conn.setLocalDescription(offer)
    if (token !== generation) return

    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/sdp' },
      body: offer.sdp ?? '',
    })
    const body = await res.text()
    if (token !== generation) return

    if (!res.ok) {
      fail('信令服务返回 ' + res.status + '：' + (body.slice(0, 200) || '无响应内容'))
      return
    }
    if (!body.includes('m=')) {
      fail('信令地址返回的不是 SDP，后端可能没有配置 MEDIAMTX_WHEP_PROXY_TARGET。')
      return
    }

    await conn.setRemoteDescription({ type: 'answer', sdp: body })
  } catch (err) {
    if (token !== generation) return
    fail(err instanceof Error ? err.message : 'WebRTC 协商失败')
  }
}

/** 采样一次“落后直播边缘多少秒”，供统计面板与追平按钮使用。 */
function syncLatency(): void {
  if (hls && typeof hls.latency === 'number' && Number.isFinite(hls.latency)) {
    latencySec.value = Math.max(0, hls.latency)
    return
  }
  const video = videoEl.value
  if (!video || !video.seekable || video.seekable.length === 0) return
  const edge = video.seekable.end(video.seekable.length - 1)
  const value = edge - video.currentTime
  if (Number.isFinite(value) && value >= 0) latencySec.value = value
}

/** 跳到直播边缘：hls.js 用它的 liveSyncPosition，原生播放器用 seekable 末端。 */
function seekLive(): void {
  const video = videoEl.value
  if (!video) return
  const target = hls && typeof hls.liveSyncPosition === 'number' ? hls.liveSyncPosition : 0
  if (target > 0) {
    video.currentTime = target
    return
  }
  if (video.seekable && video.seekable.length > 0) {
    const edge = video.seekable.end(video.seekable.length - 1)
    if (Number.isFinite(edge) && edge > 0) video.currentTime = Math.max(0, edge - 0.3)
  }
}

function bufferedAhead(video: HTMLVideoElement | null): number {
  if (!video || !video.buffered || video.buffered.length === 0) return 0
  const index = video.buffered.length - 1
  return Math.max(0, video.buffered.end(index) - video.currentTime)
}

function startMonitor(token: number): void {
  if (monitorTimer !== null) clearInterval(monitorTimer)
  monitorTimer = setInterval(() => {
    if (token !== generation) return
    void sampleOnce(token)
  }, 1000)
  void sampleOnce(token)
}

async function sampleOnce(token: number): Promise<void> {
  const now = Date.now()
  let next: StatsSample | null = null

  if (mode.value === 'webrtc' && pc) {
    const conn = pc
    try {
      const report = await conn.getStats()
      if (token !== generation) return
      const items: Record<string, unknown>[] = []
      report.forEach((entry) => items.push(entry as unknown as Record<string, unknown>))
      next = collectSample(items, now, sample.value)
    } catch {
      return
    }
  } else if (mode.value === 'hls') {
    syncLatency()
    next = hlsSample(now, sample.value)
  }

  if (!next) return

  sample.value = next
  history.value = [...history.value, next].slice(-HISTORY_LIMIT)
  trackProgress(next, now)
}

/**
 * HLS 没有 RTP 统计可读，只能从播放器侧推算：
 * - 下载速率取 hls.js 的带宽估算（也就是它实际测到的下载速度）；
 * - 帧率与丢帧取 <video> 的 getVideoPlaybackQuality；
 * - 缓冲延迟取已缓冲时长，它近似等于"落后直播头多少"。
 */
function hlsSample(now: number, prev: StatsSample | null): StatsSample {
  const s = emptySample(now)
  const video = videoEl.value
  const media = props.media

  s.videoCodec = media?.videoCodec ? codecLabel(media.videoCodec) : ''
  s.audioCodec = media?.audioCodec ? codecLabel(media.audioCodec) : ''
  s.width = video?.videoWidth ?? 0
  s.height = video?.videoHeight ?? 0
  s.bufferMs = Math.round(bufferedAhead(video) * 1000)

  const estimate = hlsBandwidthKbps()
  s.videoKbps = estimate

  const quality = video?.getVideoPlaybackQuality?.()
  const total = Number(quality?.totalVideoFrames ?? 0)
  const dropped = Number(quality?.droppedVideoFrames ?? 0)
  s.framesDecoded = Math.max(0, total - dropped)

  const deltaDropped = Math.max(0, dropped - droppedTotal)
  droppedTotal = dropped

  if (prev && prev.at > 0 && now > prev.at) {
    const seconds = (now - prev.at) / 1000
    const deltaFrames = Math.max(0, s.framesDecoded - prev.framesDecoded)
    s.fps = Math.round((deltaFrames / seconds) * 10) / 10
    s.lost = deltaDropped
    const seen = deltaFrames + deltaDropped
    s.lossRate = seen > 0 ? Math.min(1, deltaDropped / seen) : 0
    // 没有 RTP 统计时，用带宽估算反推这段时间大致下载了多少字节。
    s.receivedBytes = prev.receivedBytes + Math.round(((s.videoKbps * 1000) / 8) * seconds)
  }

  return s
}

function hlsBandwidthKbps(): number {
  const instance = hls
  if (!instance) return 0
  if (typeof instance.bandwidthEstimate === 'number' && instance.bandwidthEstimate > 0) {
    return Math.round(instance.bandwidthEstimate / 1000)
  }
  const level = instance.levels?.[0]
  if (level?.bitrate) return Math.round(level.bitrate / 1000)
  return 0
}

function trackProgress(next: StatsSample, now: number): void {
  const frames = next.framesDecoded
  if (frames > lastFrameCount) {
    lastFrameCount = frames
    lastFrameAt = now
    stallNote.value = ''
  }

  if (phase.value === 'connecting' && frames > 0) {
    phase.value = 'playing'
    errorText.value = ''
  }

  if (lastFrameAt > 0 && now - lastFrameAt > 8000 && phase.value === 'playing') {
    stallNote.value = '画面停住了：已经超过 8 秒没有新的视频帧。' + noFrameHint(props.media, mode.value)
  }

  // WebRTC 连上 6 秒还一帧都没有：MediaMTX 多半已经因为 B 帧把会话关掉了。
  // 与其让观众盯着黑屏，不如自动切回 HLS。
  if (mode.value === 'webrtc' && webrtcSince > 0 && frames === 0 && now - webrtcSince > 6000) {
    autoFallbackFromWebRTC('低延迟通道 6 秒内没有收到画面')
  }
}

/**
 * 低延迟通道取不到画面时自动回退到 HLS。
 * MediaMTX 检测到 H.264 带 B 帧会直接断开会话，这是最常见的原因。
 * 不写回 localStorage：用户改好 OBS 设置后，下次仍然能一键回到低延迟。
 */
function autoFallbackFromWebRTC(reason: string): boolean {
  if (mode.value !== 'webrtc' || fallbackTried) return false
  if (!playback.value?.hlsUrl) return false
  fallbackTried = true
  autoSwitchNote.value =
    reason +
    '，已自动切回 HLS。最常见的原因是 OBS 的 H.264 开着 B 帧：把 输出 → 高级 →「最大 B 帧数」设为 0' +
    '（x264 自定义选项写 bframes=0）后重新推流，再点「低延迟」即可。'
  mode.value = 'hls'
  if (props.room.state === 'LIVE') void start()
  return true
}

function armWatchdog(token: number): void {
  if (watchdogTimer !== null) clearTimeout(watchdogTimer)
  watchdogTimer = setTimeout(() => {
    if (token !== generation) return
    if (phase.value === 'playing' && lastFrameCount > 0) return
    overlayNote.value = noFrameHint(props.media, mode.value)
  }, 9000)
}

function switchMode(next: PlayMode): void {
  if (next === mode.value) return
  mode.value = next
  saveMode(next)
  fallbackTried = false
  autoSwitchNote.value = ''
  if (props.room.state === 'LIVE') void start()
}

function retry(): void {
  if (retryTimer !== null) clearTimeout(retryTimer)
  retryTimer = setTimeout(() => {
    retryTimer = null
    void start()
  }, 300)
}

function toggleMute(): void {
  muted.value = !muted.value
  const video = videoEl.value
  if (video) video.muted = muted.value
}

function toggleStats(): void {
  showStats.value = !showStats.value
}

function toggleTheater(): void {
  theater.value = !theater.value
  if (theater.value) document.body.style.overflow = 'hidden'
  else document.body.style.overflow = ''
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape' && theater.value) toggleTheater()
}

async function toggleFullscreen(): Promise<void> {
  const stage = stageEl.value
  if (!stage) return
  try {
    if (document.fullscreenElement) await document.exitFullscreen()
    else await stage.requestFullscreen()
  } catch {
    // 浏览器拒绝（例如 iframe 未授权）时保持原样
  }
}

async function togglePip(): Promise<void> {
  const video = videoEl.value
  if (!video) return
  try {
    if (document.pictureInPictureElement) await document.exitPictureInPicture()
    else await video.requestPictureInPicture()
  } catch {
    // 不支持画中画时忽略
  }
}

function onStageDoubleClick(): void {
  void toggleFullscreen()
}

watch(
  () => props.room.username + ':' + props.room.state,
  () => {
    if (props.room.state === 'LIVE') void start()
    else teardown()
  },
  { immediate: true },
)

watch(theater, (value) => {
  if (value) window.addEventListener('keydown', onKeydown)
  else window.removeEventListener('keydown', onKeydown)
})

onBeforeUnmount(() => {
  generation++
  teardown()
  window.removeEventListener('keydown', onKeydown)
  if (theater.value) document.body.style.overflow = ''
})
</script>

<template>
  <div
    ref="stageEl"
    class="player"
    :class="['mode-' + mode, { 'is-theater': theater }]"
    @dblclick="onStageDoubleClick"
  >
    <video ref="videoEl" class="video" playsinline autoplay :muted="muted" controls="false" />

    <div v-if="phase !== 'playing'" class="overlay">
      <template v-if="phase === 'connecting'">
        <span class="spinner" aria-hidden="true"></span>
        <p class="overlay-title">{{ mode === 'hls' ? '正在连接 HLS 播放…' : '正在建立 WebRTC 连接…' }}</p>
        <p v-if="overlayNote" class="overlay-note">{{ overlayNote }}</p>
      </template>
      <template v-else>
        <p class="overlay-title">无法播放</p>
        <p class="overlay-note">{{ errorText }}</p>
        <div class="overlay-actions">
          <button class="overlay-btn" type="button" @click="retry">重新连接</button>
          <button
            v-if="canSwitch"
            class="overlay-btn ghost"
            type="button"
            @click="switchMode(mode === 'hls' ? 'webrtc' : 'hls')"
          >
            切换到 {{ mode === 'hls' ? 'WebRTC' : 'HLS' }}
          </button>
        </div>
      </template>
    </div>

    <p v-if="phase === 'playing' && stallNote" class="stall-chip">{{ stallNote }}</p>

    <p v-if="autoSwitchNote" class="stall-chip auto-chip">
      <span>{{ autoSwitchNote }}</span>
      <button type="button" class="chip-btn" @click="autoSwitchNote = ''">知道了</button>
    </p>

    <p v-if="issue && issue.level === 'error' && issue.suggestMode" class="codec-hint">
      <span>{{ issue.title }}</span>
      <button type="button" class="overlay-btn" @click="switchMode(issue.suggestMode!)">
        换到 {{ issue.suggestMode === 'hls' ? 'HLS' : '低延迟' }}
      </button>
    </p>

    <div class="controls">
      <span class="live-chip"><i aria-hidden="true"></i>LIVE</span>

      <div v-if="hasModes" class="mode-switch" role="group" aria-label="播放通道">
        <button
          type="button"
          class="mode-btn"
          :class="{ active: mode === 'hls' }"
          :disabled="!playback?.hlsUrl"
          title="HLS：约 1 秒延迟，有声音，支持 H.264 / H.265"
          @click="switchMode('hls')"
        >
          HLS
        </button>
        <button
          type="button"
          class="mode-btn"
          :class="{ active: mode === 'webrtc' }"
          :disabled="!playback?.whepUrl"
          title="WebRTC：约 0.3 秒延迟，但 AAC 音频播不出来"
          @click="switchMode('webrtc')"
        >
          低延迟
        </button>
      </div>

      <span class="media-chip" :title="describeMedia(props.media)">{{ describeMedia(props.media) || '编码未知' }}</span>

      <button
        v-if="mode === 'hls' && latencySec >= 2"
        type="button"
        class="latency-chip"
        title="画面落后于直播，点一下追到最新"
        @click="seekLive"
      >
        延迟 {{ latencySec.toFixed(1) }}s · 追平
      </button>

      <div class="spacer"></div>

      <button
        type="button"
        class="ctrl"
        :class="{ on: showStats }"
        title="显示播放统计"
        @click="toggleStats"
      >
        🩺
      </button>
      <button type="button" class="ctrl" :class="{ on: muted }" :title="muted ? '取消静音' : '静音'" @click="toggleMute">
        {{ muted ? '🔇' : '🔊' }}
      </button>
      <button type="button" class="ctrl" :class="{ on: theater }" title="网页全屏" @click="toggleTheater">⛶</button>
      <button type="button" class="ctrl" title="全屏" @click="toggleFullscreen">⤢</button>
      <button type="button" class="ctrl" title="画中画" @click="togglePip">⧉</button>
    </div>

    <div v-if="showStats" class="stats">
      <div class="chart-head">
        <div>
          <p class="chart-title">{{ METRICS.find((m) => m.key === metric)?.label }}（最近 60 秒）</p>
          <p class="chart-value">{{ currentValue }}</p>
        </div>
        <p class="chart-peak">峰值 {{ peakValue }}</p>
      </div>

      <svg class="chart" viewBox="0 0 260 64" preserveAspectRatio="none" aria-hidden="true">
        <path v-if="chartPath" class="chart-line" :d="chartPath" />
        <text v-else class="chart-empty" x="130" y="36" text-anchor="middle">采样中…</text>
      </svg>

      <div class="chart-tabs">
        <button
          v-for="tab in METRICS"
          :key="tab.key"
          type="button"
          class="chart-tab"
          :class="{ active: metric === tab.key }"
          @click="metric = tab.key"
        >
          {{ tab.label }}
        </button>
      </div>

      <dl class="stats-grid">
        <template v-for="row in grid" :key="row.label">
          <dt>{{ row.label }}</dt>
          <dd>{{ row.value }}</dd>
        </template>
      </dl>
    </div>
  </div>
</template>

<style scoped>
.player {
  position: relative;
  aspect-ratio: 16 / 9;
  border-radius: 16px;
  overflow: hidden;
  background: #05080d;
  border: 1px solid var(--line);
}

.player.is-theater {
  position: fixed;
  inset: 0;
  z-index: 80;
  aspect-ratio: auto;
  border-radius: 0;
  border: none;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #000;
}

.player.is-theater .video {
  width: 100%;
  height: 100%;
  object-fit: contain;
}

.video {
  width: 100%;
  height: 100%;
  display: block;
  background: #000;
  object-fit: contain;
}

.overlay {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 10px;
  padding: 24px;
  text-align: center;
  background: radial-gradient(circle at 50% 40%, rgba(17, 24, 39, 0.7), rgba(5, 8, 13, 0.94));
}

.overlay-title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
}

.overlay-note {
  margin: 0;
  max-width: 520px;
  font-size: 12.5px;
  line-height: 1.75;
  color: var(--text-dim);
}

.overlay-actions {
  display: flex;
  gap: 8px;
  margin-top: 4px;
}

.overlay-btn {
  padding: 7px 14px;
  border-radius: 9px;
  border: 1px solid rgba(129, 140, 248, 0.5);
  background: rgba(99, 102, 241, 0.18);
  color: #e0e7ff;
  font-size: 12.5px;
  cursor: pointer;
}

.overlay-btn.ghost {
  background: transparent;
  border-color: var(--line);
  color: var(--text-dim);
}

.spinner {
  width: 26px;
  height: 26px;
  border-radius: 50%;
  border: 2px solid rgba(129, 140, 248, 0.25);
  border-top-color: #818cf8;
  animation: spin 0.9s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

.codec-hint {
  position: absolute;
  left: 12px;
  top: 12px;
  z-index: 3;
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 0;
  max-width: calc(100% - 24px);
  padding: 7px 10px;
  border-radius: 10px;
  background: rgba(120, 53, 15, 0.9);
  border: 1px solid rgba(245, 158, 11, 0.45);
  color: #fde68a;
  font-size: 12px;
}

.stall-chip {
  position: absolute;
  left: 12px;
  bottom: 56px;
  z-index: 3;
  margin: 0;
  max-width: min(560px, calc(100% - 24px));
  padding: 8px 12px;
  border-radius: 10px;
  background: rgba(120, 53, 15, 0.92);
  border: 1px solid rgba(245, 158, 11, 0.5);
  color: #fde68a;
  font-size: 12px;
  line-height: 1.6;
}

.latency-chip {
  display: inline-flex;
  align-items: center;
  border-radius: 999px;
  border: 1px solid rgba(245, 158, 11, 0.55);
  background: rgba(120, 53, 15, 0.85);
  color: #fde68a;
  font-size: 11.5px;
  padding: 3px 10px;
  cursor: pointer;
  white-space: nowrap;
}

.latency-chip:hover {
  background: rgba(146, 64, 14, 0.95);
}

.auto-chip {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

.chip-btn {
  flex: none;
  border-radius: 6px;
  border: 1px solid rgba(245, 158, 11, 0.55);
  background: transparent;
  color: #fde68a;
  font-size: 11.5px;
  padding: 2px 8px;
  cursor: pointer;
}

.chip-btn:hover {
  background: rgba(245, 158, 11, 0.2);
}

.controls {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: 4;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 12px;
  background: linear-gradient(0deg, rgba(3, 6, 12, 0.9), rgba(3, 6, 12, 0));
}

.live-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.08em;
  color: #fecaca;
}

.live-chip i {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #ef4444;
  box-shadow: 0 0 0 4px rgba(239, 68, 68, 0.18);
}

.mode-switch {
  display: inline-flex;
  padding: 2px;
  border-radius: 8px;
  background: rgba(15, 23, 42, 0.8);
  border: 1px solid var(--line);
}

.mode-btn {
  padding: 3px 9px;
  border-radius: 6px;
  border: none;
  background: transparent;
  color: var(--text-dim);
  font-size: 11.5px;
  cursor: pointer;
}

.mode-btn.active {
  background: rgba(99, 102, 241, 0.35);
  color: #e0e7ff;
}

.mode-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.media-chip {
  font-size: 11.5px;
  color: var(--text-dim);
  font-family: var(--mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 40%;
}

.spacer {
  flex: 1;
}

.ctrl {
  width: 30px;
  height: 30px;
  display: grid;
  place-items: center;
  border-radius: 8px;
  border: 1px solid transparent;
  background: rgba(15, 23, 42, 0.65);
  color: var(--text-dim);
  font-size: 14px;
  cursor: pointer;
  transition: color 0.15s ease, border-color 0.15s ease;
}

.ctrl:hover {
  color: var(--text);
  border-color: var(--line);
}

.ctrl.on {
  color: #c7d2fe;
  border-color: rgba(129, 140, 248, 0.45);
}

.stats {
  position: absolute;
  right: 12px;
  top: 12px;
  z-index: 5;
  width: min(320px, calc(100% - 24px));
  max-height: calc(100% - 76px);
  overflow: auto;
  padding: 14px;
  border-radius: 14px;
  border: 1px solid var(--line);
  background: rgba(6, 10, 16, 0.94);
  backdrop-filter: blur(6px);
}

.chart-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 10px;
}

.chart-title {
  margin: 0;
  font-size: 11.5px;
  color: var(--text-faint);
}

.chart-value {
  margin: 4px 0 0;
  font-size: 19px;
  font-weight: 620;
  font-variant-numeric: tabular-nums;
}

.chart-peak {
  margin: 0;
  font-size: 11px;
  color: var(--text-faint);
}

.chart {
  width: 100%;
  height: 64px;
  margin: 8px 0 4px;
  display: block;
  overflow: visible;
}

.chart-line {
  fill: none;
  stroke: #818cf8;
  stroke-width: 1.6;
  stroke-linejoin: round;
  stroke-linecap: round;
}

.chart-empty {
  fill: var(--text-faint);
  font-size: 11px;
}

.chart-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-bottom: 10px;
}

.chart-tab {
  padding: 3px 8px;
  border-radius: 7px;
  border: 1px solid var(--line);
  background: transparent;
  color: var(--text-faint);
  font-size: 11px;
  cursor: pointer;
}

.chart-tab.active {
  color: #e0e7ff;
  border-color: rgba(129, 140, 248, 0.5);
  background: rgba(99, 102, 241, 0.16);
}

.stats-grid {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 5px 12px;
  margin: 0;
  font-size: 11.5px;
}

.stats-grid dt {
  color: var(--text-faint);
}

.stats-grid dd {
  margin: 0;
  text-align: right;
  font-family: var(--mono);
  color: var(--text);
  overflow-wrap: anywhere;
}

@media (max-width: 640px) {
  .media-chip {
    display: none;
  }

  .stats {
    position: static;
    width: auto;
    max-height: none;
    margin-top: 8px;
  }
}
</style>
