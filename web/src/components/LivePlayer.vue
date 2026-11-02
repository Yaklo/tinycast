<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { fetchPlay } from '../api'
import { collectSample, emptySample, sparklinePath } from '../lib/stats'
import type { StatsSample } from '../lib/stats'
import { codecLabel, describeMedia, mediaIssue, noFrameHint } from '../lib/playback'
import { ENGINE_OPTIONS, engineLabel, isSafariLike, mseSupported, resolveEngine } from '../lib/engine'
import type { Engine, EnginePref } from '../lib/engine'
import { deliveryLabel, firstVariantUrl, hasLowLatencyParts } from '../lib/playlist'
import type { Delivery } from '../lib/playlist'
import { TIER_OPTIONS, hlsTuning, tierLabel } from '../lib/tuning'
import type { Tier } from '../lib/tuning'
import type { MediaInfo, Playback, RoomView } from '../types'

/**
 * 三通道播放器（LL-HLS / HLS / WebRTC）。
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

const ENGINE_KEY = 'tinycast.hlsEngine'

/** 记住用户选的播放引擎偏好；没选过就用「自动」（Safari 原生、其余 HLS.js）。 */
function loadEnginePref(): EnginePref {
  try {
    const stored = localStorage.getItem(ENGINE_KEY)
    return stored === 'js' || stored === 'native' ? stored : 'auto'
  } catch {
    return 'auto'
  }
}

function saveEnginePref(next: EnginePref): void {
  try {
    localStorage.setItem(ENGINE_KEY, next)
  } catch {
    // 隐私模式下 localStorage 不可写，忽略即可
  }
}

const enginePref = ref<EnginePref>(loadEnginePref())
/** 这一次实际生效的引擎，由 resolveEngine 决定，可能和偏好不一致。 */
const engine = ref<Engine | ''>('')
/** 引擎降级 / 编码不匹配时的说明，显示在统计面板里。 */
const engineNotice = ref('')
/** 播放时真正在用的分发协议：LL-HLS、普通 HLS，还是 WebRTC。 */
const delivery = ref<Delivery>('unknown')

/** 最近一次决策时这个浏览器有没有原生 HLS 管线，模板用它把「原生」按钮置灰。 */
const nativeReady = ref(false)

/** 这个浏览器能不能用原生 HLS 管线（只有 Safari 家族算）。 */
function canUseNative(video?: HTMLVideoElement | null): boolean {
  if (typeof navigator === 'undefined' || !isSafariLike(navigator.userAgent)) return false
  const el = video ?? videoEl.value
  if (!el) return false
  return el.canPlayType('application/vnd.apple.mpegurl') !== ''
}

const TIER_KEY = 'tinycast.playTier'
const LLHLS_KEY = 'tinycast.llhls'

/** 记住用户选的延迟档位；没选过就用「稳定」（默认通道是 LL-HLS）。 */
function loadTier(): Tier {
  try {
    const stored = localStorage.getItem(TIER_KEY)
    return stored === 'low' ? 'low' : 'stable'
  } catch {
    return 'stable'
  }
}

function saveTier(next: Tier): void {
  try {
    localStorage.setItem(TIER_KEY, next)
  } catch {
    // 隐私模式下不可写，忽略即可
  }
}

/** LL-HLS 开关：关掉就退回普通 HLS（分片粒度粗、弱网更稳，代价是十几秒延迟）。 */
function loadLlhls(): boolean {
  try {
    return localStorage.getItem(LLHLS_KEY) !== 'off'
  } catch {
    return true
  }
}

function saveLlhls(next: boolean): void {
  try {
    localStorage.setItem(LLHLS_KEY, next ? 'on' : 'off')
  } catch {
    // 隐私模式下不可写，忽略即可
  }
}

const tier = ref<Tier>(loadTier())
const llhls = ref<boolean>(loadLlhls())
/** 播放参数卡片是否展开。 */
const showSettings = ref(false)
/** 当前参数表允许的追平阈值（秒），0 表示不再主动追。 */
let catchUpAfter = 1.8

/** 现在真正生效的参数：档位 + LL-HLS 开关。 */
function currentTuning() {
  return hlsTuning(tier.value, llhls.value)
}

/** 面板里展示的档位文案。 */
const tierText = computed(() => tierLabel(tier.value, llhls.value))

/** 档位按钮的悬停说明：为什么有时候是灰的。 */
function tierTitle(value: Tier): string {
  if (mode.value === 'webrtc') return 'WebRTC 通道不解析分片，延迟档位只在 LL-HLS 下生效'
  if (engine.value === 'native') return '原生播放器自己决定缓冲，档位只在 HLS.js 下生效'
  if (!llhls.value) return '当前是 HLS 通道（整段分片），延迟档位只在 LL-HLS 下生效'
  return TIER_OPTIONS.find((item) => item.value === value)?.hint ?? ''
}

/**
 * 控制栏上的三个通道。
 *
 * LL-HLS 与 HLS 都是用分片播放，区别只在「要不要解析 0.2 秒的 LL-HLS 分片」，
 * 所以它们共用一个 mode='hls'，由 llhls 这个布尔区分；WebRTC 走另一条链路。
 */
type Channel = 'llhls' | 'hls' | 'webrtc'

const channel = computed<Channel>(() => {
  if (mode.value === 'webrtc') return 'webrtc'
  return llhls.value ? 'llhls' : 'hls'
})

const CHANNEL_HINTS: Record<Channel, string> = {
  llhls: 'LL-HLS：0.2 秒一片，延迟 1~3 秒（看档位），有声音，支持 H.264 / H.265',
  hls: 'HLS：按整段分片缓冲，延迟十秒级，弱网最稳，兼容性最好',
  webrtc: 'WebRTC：延迟 0.3 秒，但 AAC 音频播不出来，且推流不能带 B 帧',
}

/** 切通道：LL-HLS 与 HLS 之间只重建一次，避免连撞两次 start()。 */
function setChannel(next: Channel): void {
  if (next === channel.value) return
  if (next === 'webrtc') {
    switchMode('webrtc')
    return
  }
  const wantLlhls = next === 'llhls'
  const needMode = mode.value !== 'hls'
  const needLlhls = llhls.value !== wantLlhls
  if (!needMode && !needLlhls) return
  mode.value = 'hls'
  saveMode('hls')
  fallbackTried = false
  autoSwitchNote.value = ''
  if (needLlhls) {
    llhls.value = wantLlhls
    saveLlhls(wantLlhls)
  }
  if (props.room.state === 'LIVE') void start()
  else retry()
}

/** 播放参数卡片：档位与播放方式都收在这里，不占画面。 */
function toggleSettings(): void {
  showSettings.value = !showSettings.value
}

/** 引擎说明：两种引擎的死角不一样，写清楚比让人猜好。 */
const engineHint = computed(() =>
  engine.value === 'native'
    ? '原生播放器：省电、支持 AirPlay，但拿不到分片指标，延迟由系统决定。'
    : 'HLS.js：指标全、延迟可控；Safari 上只能解 H.264。',
)

const muted = ref(true)
const theater = ref(false)
const showStats = ref(false)
/** 控制条是否显示：播放中鼠标静止 2.5 秒自动收起，动一下鼠标再出来。 */
const controlsVisible = ref(true)
let controlsTimer: ReturnType<typeof setTimeout> | null = null
/** 距离直播边缘的秒数：hls.js 直接给出，原生 HLS 用 seekable 末端估算。 */
const latencySec = ref(0)
/** 从 WebRTC 通道自动回退到 LL-HLS 时的说明条。 */
const autoSwitchNote = ref('')
/** 全屏 / 画中画不可用等一次性操作提示。 */
const controlNote = ref('')

const HISTORY_LIMIT = 60

const METRICS = [
  { key: 'kbps', label: '下载速率' },
  { key: 'fps', label: '帧率' },
  { key: 'bufferMs', label: '缓冲余量' },
  { key: 'latency', label: '直播延迟' },
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
  /** hls.js 的实时配置：延迟控制器每轮采样都会重新读，改它等于热调参 */
  config?: Record<string, unknown>
}

let hls: HlsHandle | null = null
let monitorTimer: ReturnType<typeof setInterval> | null = null
let watchdogTimer: ReturnType<typeof setTimeout> | null = null
let retryTimer: ReturnType<typeof setTimeout> | null = null
/** 网络错误重试计数与定时器：断流时 hls.js 会立刻失败，必须自己退避。 */
let networkRetryTimer: ReturnType<typeof setTimeout> | null = null
let networkRetries = 0
/** 原生 <video> 的监听器：切档/重连必须摘掉，否则旧会话的错误会打到新会话上。 */
let nativeErrorHandler: (() => void) | null = null
let nativeMetaHandler: (() => void) | null = null
/** 解码错误重试次数：编码确实不支持时 recoverMediaError 只会无限黑屏。 */
let mediaRetries = 0
let lastFrameAt = 0
let lastFrameCount = 0
/** HLS 侧的累计丢帧数：getVideoPlaybackQuality 给的是累计值，需要自己差分。 */
let droppedTotal = 0
/** HLS 累计下载字节（视频 + 音频 + 音频轨）。 */
let hlsBytes = 0
/** 最近几个分片的 字节数 / 媒体时长，用来算视频、音频码率。 */
let hlsVideoWindow: { bytes: number; duration: number }[] = []
let hlsAudioWindow: { bytes: number; duration: number }[] = []
/** 最近几次分片请求的 字节数 / 耗时，用来算真实下载速率。 */
let hlsLoads: { bytes: number; ms: number }[] = []
/** 自动追平直播边缘的节流时间戳。 */
let lastCatchUp = 0

/** 清单给出的直播边缘（媒体时间）与记录时刻，用来算真实延迟。 */
let edgePos = 0
let edgeAt = 0
/** WebRTC 起播时刻，用来判断“连上很久却一帧都没有”。 */
let webrtcSince = 0
/** 同一次播放里只自动回退一次，避免通道来回跳。 */
let fallbackTried = false
/** hls.js 解不了这个编码时，只自动降级到原生一次。 */
let engineFallbackTried = false
/** 自动降级的原因。start() 会清空面板状态，这条要留着让用户知道发生了什么。 */
let fallbackNotice = ''

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

/** HLS 走 HTTP 分片，没有 RTP 统计：RTT / 丢包不展示；WebRTC 拿不到分片延迟。 */
const metrics = computed(() =>
  METRICS.filter((tab) => {
    if (mode.value === 'hls' && (tab.key === 'rttMs' || tab.key === 'lossRate')) return false
    if (mode.value !== 'hls' && tab.key === 'latency') return false
    return true
  }),
)

// 切换通道后，当前选中的指标可能在新通道里不存在（例如 HLS 没有 RTT），回落到下载速率。
watch(metrics, (list) => {
  if (!list.some((item) => item.key === metric.value)) metric.value = 'kbps'
})

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
  const rows: { label: string; value: string }[] = [
    { label: '播放通道', value: channel.value === 'llhls' ? 'LL-HLS' : channel.value === 'hls' ? 'HLS' : 'WebRTC' },
    { label: '播放方式', value: mode.value === 'hls' ? engineLabel(engine.value) : 'WebRTC' },
    { label: '分发协议', value: deliveryLabel(delivery.value) },
    { label: '延迟档位', value: mode.value === 'hls' ? tierText.value : '—' },
    { label: '视频编码', value: labelOrDash(media?.videoCodec) },
    { label: '音频编码', value: labelOrDash(media?.audioCodec) },
    { label: '分辨率', value: resolution(s) },
    { label: '播放帧率', value: s && s.fps > 0 ? s.fps.toFixed(1) + ' fps' : '—' },
    { label: '视频码率', value: s && s.videoKbps > 0 ? formatKbps(s.videoKbps) : '—' },
    { label: '音频码率', value: s && s.audioKbps > 0 ? formatKbps(s.audioKbps) : '—' },
    { label: '下载速率', value: s && s.downloadKbps > 0 ? formatKbps(s.downloadKbps) : '—' },
    { label: '直播延迟', value: latencyText(s) },
    { label: '缓冲余量', value: s && s.bufferMs > 0 ? s.bufferMs + ' ms' : '—' },
  ]
  // RTT / 抖动 / 丢包是 RTP 层面的指标，HLS 通道下根本拿不到，硬显示成「—」只会让人以为坏了。
  if (mode.value === 'webrtc') {
    rows.push(
      { label: '往返时延', value: s && s.rttMs > 0 ? s.rttMs + ' ms' : '—' },
      { label: '抖动', value: s && s.jitterMs > 0 ? s.jitterMs + ' ms' : '—' },
      {
        label: '丢包/丢帧',
        value: s ? (s.lossRate > 0 ? (s.lossRate * 100).toFixed(2) + '%' : '0%') : '—',
      },
    )
  }
  rows.push(
    { label: '累计接收', value: s && s.receivedBytes > 0 ? formatBytes(s.receivedBytes) : '—' },
    { label: '解码帧', value: s && s.framesDecoded > 0 ? formatNumber(s.framesDecoded) : '—' },
    {
      label: '连接状态',
      value:
        mode.value === 'hls'
          ? phase.value === 'playing'
            ? '播放中'
            : phase.value === 'error'
              ? '播放出错'
              : '缓冲中'
          : labelOrDash(connState.value),
    },
  )
  return rows
})

/** 直播延迟优先用采样值，其次用实时量到的值。 */
function latencyText(s: StatsSample | null): string {
  const ms = s?.latencyMs ?? 0
  if (ms > 0) return (ms / 1000).toFixed(1) + ' s'
  if (latencySec.value > 0) return latencySec.value.toFixed(1) + ' s'
  return '—'
}

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
      return item.downloadKbps > 0 ? item.downloadKbps : item.videoKbps + item.audioKbps
    case 'latency':
      return item.latencyMs
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
  if (key === 'latency') return (value / 1000).toFixed(1) + ' s'
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

function clearNativeHandlers(video?: HTMLVideoElement | null): void {
  const el = video ?? videoEl.value
  if (el) {
    if (nativeErrorHandler) el.removeEventListener('error', nativeErrorHandler)
    if (nativeMetaHandler) el.removeEventListener('loadedmetadata', nativeMetaHandler)
  }
  nativeErrorHandler = null
  nativeMetaHandler = null
}

function teardown(): void {
  if (controlsTimer !== null) {
    clearTimeout(controlsTimer)
    controlsTimer = null
  }
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
  if (networkRetryTimer !== null) {
    clearTimeout(networkRetryTimer)
    networkRetryTimer = null
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
    clearNativeHandlers(video)
    video.pause()
    video.removeAttribute('src')
    video.srcObject = null
    // 走过 HLS.js 会把 disableRemotePlayback 置为 true；切回原生时要还回 AirPlay。
    ;(video as HTMLVideoElement & { disableRemotePlayback?: boolean }).disableRemotePlayback = false
    video.load()
  }
  connState.value = ''
  overlayNote.value = ''
  stallNote.value = ''
}

function fail(message: string): void {
  phase.value = 'error'
  errorText.value = message
  // 进入错误态后不再采样：播放器可能已经被销毁，继续轮询只会读到空数据。
  if (monitorTimer !== null) {
    clearInterval(monitorTimer)
    monitorTimer = null
  }
  // 面板里的码率、帧率都是死数据了，清空比让用户盯着冻结的数字好。
  sample.value = null
  history.value = []
  latencySec.value = 0
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
  hlsBytes = 0
  hlsVideoWindow = []
  hlsAudioWindow = []
  hlsLoads = []
  lastCatchUp = 0
  edgePos = 0
  edgeAt = 0
  engine.value = ''
  engineNotice.value = fallbackNotice
  delivery.value = 'unknown'
  engineFallbackTried = false
  networkRetries = 0
  mediaRetries = 0

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
  if (token !== generation || phase.value === 'error') return
  startMonitor(token)
  armWatchdog(token)
}

/** 原生播放器不给分片信息，只能读一次清单，判断是 LL-HLS 还是普通 HLS。 */
async function probeDelivery(url: string, token: number): Promise<void> {
  // 关掉 LL-HLS 时我们根本不看分片，没必要再去读清单判断。
  if (!llhls.value) {
    delivery.value = 'hls'
    return
  }
  try {
    const masterUrl = new URL(url, location.href).toString()
    const master = await fetch(masterUrl, { cache: 'no-store' })
    if (token !== generation || !master.ok) return
    const masterText = await master.text()
    if (token !== generation) return
    if (hasLowLatencyParts(masterText)) {
      delivery.value = 'll-hls'
      return
    }
    const variant = firstVariantUrl(masterText, masterUrl)
    if (!variant) {
      delivery.value = 'hls'
      return
    }
    const child = await fetch(variant, { cache: 'no-store' })
    if (token !== generation || !child.ok) return
    delivery.value = hasLowLatencyParts(await child.text()) ? 'll-hls' : 'hls'
  } catch {
    // 探测失败不影响播放，面板显示「—」即可
  }
}

/** 原生播放器报错时把话说明白：还能换回 HLS.js 再试一次。 */
function onNativeError(): void {
  const err = videoEl.value?.error
  const detail = err ? err.message || 'code ' + err.code : '未知原因'
  fail('原生播放器无法播放这个流（' + detail + '）。可在 🩺 面板把播放方式改成「HLS.js」再试。')
}

async function startHLS(token: number): Promise<void> {
  const url = playback.value?.hlsUrl
  const video = videoEl.value
  if (!url || !video) {
    fail('服务器没有给出 HLS 播放地址，请检查后端的 MEDIAMTX_HLS_PROXY_TARGET。')
    return
  }

  video.muted = muted.value

  // 引擎选择：Chromium 的 canPlayType 对 HLS 也返回 "maybe"，但它没有真正的 HLS
  // 管线（会退化成整段下载、延迟十几秒），所以把 Chromium 排除在「原生」之外。
  // 具体落在哪个引擎由 lib/engine.ts 的 resolveEngine 决定，用户在 🩺 面板里可以改。
  nativeReady.value = canUseNative(video)
  const decision = resolveEngine({
    pref: enginePref.value,
    safariLike: typeof navigator === 'undefined' ? false : isSafariLike(navigator.userAgent),
    canPlayNative: nativeReady.value,
    mseSupported: mseSupported(),
    videoCodec: props.media?.videoCodec,
  })
  engine.value = decision.engine
  engineNotice.value = decision.notice || fallbackNotice

  if (decision.engine === 'native') {
    // 原生播放器自己决定缓冲，我们的追平逻辑只会捣乱。
    catchUpAfter = 0
    video.src = url
    // 原生管线不给分片信息，只能自己去读清单区分 LL-HLS 与普通 HLS。
    void probeDelivery(url, token)
    clearNativeHandlers(video)
    nativeErrorHandler = () => {
      if (token !== generation) return
      onNativeError()
    }
    nativeMetaHandler = () => {
      if (token !== generation) return
      seekLive()
      void video.play().catch(() => {})
    }
    video.addEventListener('error', nativeErrorHandler, { once: true })
    video.addEventListener('loadedmetadata', nativeMetaHandler, { once: true })
    // 不要在这里把 phase 置成 playing：原生播放器同样可能只拉到清单却一帧都解不出来。
    // 真正的 playing 由 trackProgress 在 framesDecoded 增长时推进。
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

  // hls.js 默认落后 3 个分片起播，而分片长度跟着主播的关键帧间隔走（OBS 默认 2~4 秒），
  // 于是观众一开始就落后 6~12 秒。这里按秒定目标、放宽追平速率，让它贴着直播边缘播。
  // 用 liveSyncDuration 而不是 liveSyncDurationCount：前者按秒算，不受分片长度影响；
  // 两个都给会被 hls.js 判成非法配置直接抛错，所以参数表里同一时刻只会有一种。
  const tuning = currentTuning()
  const { catchUpAfter: chaseAfter, ...hlsConfig } = tuning
  catchUpAfter = chaseAfter
  let instance: import('hls.js').default
  try {
    instance = new Hls({ ...hlsConfig, enableWorker: true })
  } catch (err) {
    fail('HLS 播放器初始化失败：' + (err instanceof Error ? err.message : String(err)))
    return
  }
  hls = instance as unknown as HlsHandle
  // 强制走 hls.js 时（尤其是 Safari 上的 ManagedMediaSource），必须关掉远程播放，
  // 否则浏览器不会解锁 MSE 的媒体源扩展。
  ;(video as HTMLVideoElement & { disableRemotePlayback?: boolean }).disableRemotePlayback = true
  delivery.value = 'hls'

  instance.on(Hls.Events.MANIFEST_PARSED, () => {
    seekLive()
    void video.play().catch(() => {})
    // 清单解析只代表「地址通了」，不代表能解码出画面；phase 交给 trackProgress。
    networkRetries = 0
    overlayNote.value = ''
  })

  instance.on(Hls.Events.ERROR, (_event, data) => {
    if (!data.fatal) return
    const failure = data as unknown as { response?: { code?: number }; details?: string }
    const details = String(failure.details ?? data.details ?? '')

    if (data.type === Hls.ErrorTypes.NETWORK_ERROR) {
      // 只有清单 404 才说明流真的没了；LL-HLS 分片被淘汰时也会 404，不能直接判死。
      if (
        failure.response?.code === 404 &&
        (details === 'manifestLoadError' || details === 'levelLoadError')
      ) {
        fail('直播已经结束：服务器上找不到这个流了。')
        return
      }
      if (networkRetries >= 8) {
        fail('直播已结束或网络异常，重试多次仍未恢复。请稍后重新连接。')
        return
      }
      networkRetries++
      const delay = Math.min(8000, 500 * 2 ** (networkRetries - 1))
      const note =
        '网络中断，' + Math.round((delay / 1000) * 10) / 10 + ' 秒后重试（' + networkRetries + '/8）'
      // 播放中覆盖层是隐藏的，把提示放到画面上的 stall-chip；否则写得进覆盖层。
      if (phase.value === 'playing') stallNote.value = note
      else overlayNote.value = note
      if (networkRetryTimer !== null) clearTimeout(networkRetryTimer)
      networkRetryTimer = setTimeout(() => {
        networkRetryTimer = null
        if (token !== generation) return
        if (phase.value === 'playing') stallNote.value = ''
        else overlayNote.value = ''
        instance.startLoad()
      }, delay)
      return
    }

    if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
      // 编码不被 MSE 支持（Safari 上常见于 H.265 / AV1）：换原生播放器还有救，
      // 反复 recoverMediaError 只会黑屏转圈，所以先降级一次。
      const codecProblem = /addCodecError|appendError|incompatibleCodecs/i.test(details)
      if (codecProblem) {
        if (fallbackToNative()) return
        fail('当前浏览器无法解码这个流。' + noFrameHint(props.media, 'hls'))
        return
      }
      if (mediaRetries >= 2) {
        fail('播放器连续解码失败。' + noFrameHint(props.media, 'hls'))
        return
      }
      mediaRetries++
      instance.recoverMediaError()
      return
    }
    fail('HLS 播放失败：' + details + '。' + noFrameHint(props.media, 'hls'))
  })

  // 分片下载完成才知道真实码率与下载速率：HLS 没有 RTCP，这些只能自己量。
  // 注意 hls.js 把字节数记在 frag.stats 上（事件本身没有 stats 字段），
  // 而且视频分片的 type 是 'main' 不是 'video'。
  instance.on(Hls.Events.FRAG_LOADED, (_event, data) => {
    const frag = (data as unknown as {
      frag?: {
        type?: string
        duration?: number
        stats?: { loaded?: number; total?: number; loading?: { start?: number; end?: number } }
      }
    }).frag
    const stats = frag?.stats
    const loaded = Number(stats?.total ?? 0) || Number(stats?.loaded ?? 0)
    const duration = Number(frag?.duration ?? 0)
    if (loaded <= 0) return
    // 已经恢复出数据就别再触发退避重连，否则定时器到点会白闪一下。
    if (networkRetryTimer !== null) {
      clearTimeout(networkRetryTimer)
      networkRetryTimer = null
    }
    networkRetries = 0
    hlsBytes += loaded
    const start = Number(stats?.loading?.start ?? 0)
    const stop = Number(stats?.loading?.end ?? 0)
    if (stop > start && stop > 0) {
      hlsLoads = [...hlsLoads, { bytes: loaded, ms: stop - start }].slice(-10)
    }
    if (duration <= 0) return
    const entry = { bytes: loaded, duration }
    if (frag?.type === 'audio') hlsAudioWindow = [...hlsAudioWindow, entry].slice(-8)
    else hlsVideoWindow = [...hlsVideoWindow, entry].slice(-8)
  })

  // 清单每次刷新都记下直播边缘位置，用来算「画面离直播头还有多远」。
  instance.on(Hls.Events.LEVEL_UPDATED, (_event, data) => {
    const details = (data as unknown as {
      details?: { fragments?: { end?: number }[]; partTarget?: number }
    }).details
    const fragments = details?.fragments ?? []
    const last = fragments[fragments.length - 1]
    const edge = Number(last?.end ?? 0)
    if (edge > 0) {
      edgePos = edge
      edgeAt = Date.now()
    }
    // partTarget 只有清单里带 #EXT-X-PART-INF 时才有值，也就是 LL-HLS。
    // 关了 LL-HLS 时服务端照样发分片，但我们按整段播，这里就别再标成 LL-HLS 了。
    if (llhls.value && Number(details?.partTarget ?? 0) > 0) delivery.value = 'll-hls'
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
  delivery.value = 'whep'

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
      // 信令连上只说明 ICE/DTLS 通了；没有视频帧时仍然停在「等待画面」，由 watchdog 提示。
      if (phase.value !== 'error') overlayNote.value = '已连上媒体服务器，正在等待视频帧…'
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
  const video = videoEl.value
  // 清单记录的直播边缘位置 + 经过的时间 = 此刻的直播头，减去播放位置就是真实延迟。
  if (edgePos > 0 && edgeAt > 0 && video) {
    const live = edgePos + (Date.now() - edgeAt) / 1000
    const value = live - video.currentTime
    if (Number.isFinite(value) && value >= 0 && value < 120) {
      latencySec.value = value
      return
    }
  }
  if (hls && typeof hls.latency === 'number' && Number.isFinite(hls.latency)) {
    latencySec.value = Math.max(0, hls.latency)
    return
  }
  if (!video || !video.seekable || video.seekable.length === 0) return
  const edge = video.seekable.end(video.seekable.length - 1)
  const fallbackValue = edge - video.currentTime
  if (Number.isFinite(fallbackValue) && fallbackValue >= 0) latencySec.value = fallbackValue
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

/** 落后直播边缘超过 1.8 秒就悄悄追上去（6 秒内最多一次，避免频繁跳导致卡顿）。 */
function catchUp(): void {
  if (catchUpAfter <= 0) return
  if (phase.value !== 'playing' || latencySec.value <= catchUpAfter) return
  const now = Date.now()
  if (now - lastCatchUp < 6000) return
  lastCatchUp = now
  seekLive()
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
    catchUp()
    next = hlsSample(now, sample.value)
  }

  if (!next) return

  sample.value = next
  history.value = [...history.value, next].slice(-HISTORY_LIMIT)
  trackProgress(next, now)
}


/** 最近几个分片的 字节数 / 媒体时长 → 码率（kbps）。 */
function windowBitrate(window: { bytes: number; duration: number }[]): number {
  let bytes = 0
  let duration = 0
  for (const item of window) {
    bytes += item.bytes
    duration += item.duration
  }
  if (bytes <= 0 || duration <= 0) return 0
  return Math.round((bytes * 8) / 1000 / duration)
}

/** 最近几次分片请求的 字节 / 耗时 → 真实下载速率（kbps）。 */
function measuredDownloadKbps(): number {
  let bytes = 0
  let ms = 0
  for (const item of hlsLoads) {
    bytes += item.bytes
    ms += item.ms
  }
  if (bytes <= 0 || ms <= 0) return 0
  return Math.round((bytes * 8) / ms)
}

/**
 * HLS 没有 RTP 统计可读，指标全部靠播放器侧推算：
 * - 视频 / 音频码率：最近几个分片的字节数除以媒体时长；
 * - 下载速率：分片请求的字节数除以 HTTP 加载耗时（与码率不是一回事）；
 * - 累计接收：hls.js 加载过的分片字节数累加；
 * - 帧率 / 丢帧：<video> 的 getVideoPlaybackQuality；
 * - 直播延迟：清单里的直播边缘位置减去当前播放位置。
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
  s.latencyMs = Math.round(latencySec.value * 1000)
  s.videoKbps = windowBitrate(hlsVideoWindow)
  s.audioKbps = windowBitrate(hlsAudioWindow)
  s.downloadKbps = measuredDownloadKbps() || hlsBandwidthKbps()
  s.receivedBytes = hlsBytes

  const quality = video?.getVideoPlaybackQuality?.()
  let dropped = 0
  if (quality) {
    const total = Number(quality.totalVideoFrames ?? 0)
    dropped = Number(quality.droppedVideoFrames ?? 0)
    s.framesDecoded = Math.max(0, total - dropped)
  } else {
    // 少数环境没有 getVideoPlaybackQuality；用 readyState 判断“确实出画了”。
    s.framesDecoded = video && video.readyState >= 3 && video.currentTime > 0 ? 1 : 0
  }

  const deltaDropped = Math.max(0, dropped - droppedTotal)
  droppedTotal = dropped

  if (prev && prev.at > 0 && now > prev.at) {
    const seconds = (now - prev.at) / 1000
    const deltaFrames = Math.max(0, s.framesDecoded - prev.framesDecoded)
    s.fps = Math.round((deltaFrames / seconds) * 10) / 10
    s.lost = deltaDropped
    const seen = deltaFrames + deltaDropped
    s.lossRate = seen > 0 ? Math.min(1, deltaDropped / seen) : 0
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
    overlayNote.value = ''
    mediaRetries = 0
  }

  if (lastFrameAt > 0 && now - lastFrameAt > 8000 && phase.value === 'playing') {
    stallNote.value = '画面停住了：已经超过 8 秒没有新的视频帧。' + noFrameHint(props.media, mode.value)
  }

  // WebRTC 连上 6 秒还一帧都没有：MediaMTX 多半已经因为 B 帧把会话关掉了。
  // 与其让观众盯着黑屏，不如自动切回 LL-HLS。
  if (mode.value === 'webrtc' && phase.value !== 'error' && webrtcSince > 0 && frames === 0 && now - webrtcSince > 6000) {
    autoFallbackFromWebRTC('WebRTC 通道 6 秒内没有收到画面')
  }
}

/**
 * WebRTC 通道取不到画面时自动回退到 LL-HLS。
 * MediaMTX 检测到 H.264 带 B 帧会直接断开会话，这是最常见的原因。
 * 不写回 localStorage：用户改好 OBS 设置后，下次仍然能一键回到 WebRTC。
 */
function autoFallbackFromWebRTC(reason: string): boolean {
  if (mode.value !== 'webrtc' || fallbackTried) return false
  if (!playback.value?.hlsUrl) return false
  fallbackTried = true
  autoSwitchNote.value =
    reason +
    '，已自动切回 LL-HLS。最常见的原因是 OBS 的 H.264 开着 B 帧：把 输出 → 高级 →「最大 B 帧数」设为 0' +
    '（x264 自定义选项写 bframes=0）后重新推流，再点「WebRTC」即可。'
  mode.value = 'hls'
  if (props.room.state === 'LIVE') void start()
  return true
}

function armWatchdog(token: number): void {
  if (watchdogTimer !== null) clearTimeout(watchdogTimer)
  watchdogTimer = setTimeout(() => {
    if (token !== generation) return
    if (lastFrameCount > 0) return
    const hint = noFrameHint(props.media, mode.value)
    if (phase.value === 'playing') {
      // WebRTC 可能已经 connected、但一帧都没收到；此时覆盖层是隐藏的。
      stallNote.value = '已经连上，但一直没有画面。' + hint
    } else {
      overlayNote.value = hint
    }
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

function restartPlayback(): void {
  if (props.room.state === 'LIVE') void start()
  else retry()
}

/**
 * 换延迟档位。
 *
 * 档位决定目标延迟（1.2 秒 / 3 秒），必须重建一次播放器才能立刻落到新目标上，
 * 代价是黑屏 0.3~1 秒；只是想换引擎（HLS.js / 原生）不用管这里。
 */
function setTier(next: Tier): void {
  if (tier.value === next) return
  tier.value = next
  saveTier(next)
  restartPlayback()
}

/**
 * 换 HLS 的播放引擎。
 *
 * 两种引擎各有死角：HLS.js 指标全、延迟低，但 Safari 的 MSE 只解 H.264；原生播放器
 * 省电、支持 AirPlay 与 H.265，却可能把延迟拖到十几秒。所以把选择权交给观众。
 */
function setEngine(next: EnginePref): void {
  if (enginePref.value === next || mode.value !== 'hls') return
  enginePref.value = next
  saveEnginePref(next)
  engineNotice.value = ''
  fallbackNotice = ''
  engineFallbackTried = false
  if (props.room.state === 'LIVE') void start()
  else retry()
}

/**
 * hls.js 解不了当前编码时自动换成原生播放器。
 * 只做一次：如果原生也不行，就让用户看到报错，别在两个引擎之间反复跳。
 */
function fallbackToNative(): boolean {
  if (engineFallbackTried || enginePref.value === 'native' || !canUseNative()) return false
  engineFallbackTried = true
  // 只对本次会话生效：下次仍然先按用户保存的偏好（通常还是自动）来。
  enginePref.value = 'native'
  fallbackNotice = 'HLS.js 解不了当前编码，已自动改用浏览器原生播放器。'
  engineNotice.value = fallbackNotice
  retry()
  return true
}

function retry(): void {
  if (retryTimer !== null) clearTimeout(retryTimer)
  retryTimer = setTimeout(() => {
    retryTimer = null
    void start()
  }, 300)
}

/**
 * 刷新播放：丢掉当前的 HLS / WebRTC 会话，重新向后端要一次播放地址。
 * 只重建播放器，不刷新整个页面，所以统计面板与全屏状态都还在。
 */
function refreshPlayback(): void {
  autoSwitchNote.value = ''
  controlNote.value = ''
  fallbackNotice = ''
  engineNotice.value = ''
  if (props.room.state === 'LIVE') void start()
  else retry()
}

/** 鼠标 / 手指一动就重新计时；播放中静止 2.5 秒收起控制条。 */
function pokeControls(): void {
  controlsVisible.value = true
  if (controlsTimer !== null) clearTimeout(controlsTimer)
  controlsTimer = setTimeout(() => {
    controlsTimer = null
    if (phase.value === 'playing') controlsVisible.value = false
  }, 2500)
}

function hideControls(): void {
  if (controlsTimer !== null) clearTimeout(controlsTimer)
  controlsTimer = null
  if (phase.value === 'playing') controlsVisible.value = false
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
  const video = videoEl.value as
    | (HTMLVideoElement & { webkitEnterFullscreen?: () => void })
    | null
  if (!stage) return
  controlNote.value = ''
  try {
    if (document.fullscreenElement) {
      await document.exitFullscreen()
      return
    }
    if (typeof stage.requestFullscreen === 'function') {
      await stage.requestFullscreen()
      return
    }
    // iPhone 的 Safari 没有 Element.requestFullscreen，只能走视频自身的全屏。
    if (video && typeof video.webkitEnterFullscreen === 'function') {
      video.webkitEnterFullscreen()
      return
    }
    controlNote.value = '当前浏览器不支持网页全屏，可以试试双击画面或旋转屏幕。'
  } catch {
    controlNote.value = '浏览器拒绝进入全屏（可能是在 iframe 里或权限被限制）。'
  }
}

async function togglePip(): Promise<void> {
  const video = videoEl.value as
    | (HTMLVideoElement & {
        webkitSetPresentationMode?: (mode: 'picture-in-picture' | 'inline') => void
        webkitPresentationMode?: string
      })
    | null
  if (!video) return
  controlNote.value = ''
  try {
    if (document.pictureInPictureElement) {
      await document.exitPictureInPicture()
      return
    }
    if (typeof video.requestPictureInPicture === 'function') {
      await video.requestPictureInPicture()
      return
    }
    // Safari 走私有 API；没有 API 时明确告诉用户，而不是静默失败。
    if (typeof video.webkitSetPresentationMode === 'function') {
      video.webkitSetPresentationMode(
        video.webkitPresentationMode === 'picture-in-picture' ? 'inline' : 'picture-in-picture',
      )
      return
    }
    controlNote.value = '当前浏览器不支持画中画。'
  } catch {
    controlNote.value = '当前浏览器不支持画中画。'
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

// 播放开始就启动“静止 2.5 秒收控制条”的计时；一旦离开播放态，控制条常显。
watch(phase, (value) => {
  if (value === 'playing') {
    pokeControls()
    return
  }
  controlsVisible.value = true
  if (controlsTimer !== null) {
    clearTimeout(controlsTimer)
    controlsTimer = null
  }
})

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
  <div class="player-layout">
    <div
      ref="stageEl"
      class="player"
      :class="['mode-' + mode, { 'is-theater': theater }]"
      @dblclick.self="onStageDoubleClick"
      @mousemove="pokeControls"
      @touchstart.passive="pokeControls"
      @mouseleave="hideControls"
    >
      <!-- 不写 controls：原生控件会和下面自绘的控制条叠在一起 -->
      <video ref="videoEl" class="video" playsinline autoplay :muted="muted" />

      <div v-if="phase !== 'playing'" class="overlay">
        <template v-if="phase === 'connecting'">
          <span class="spinner" aria-hidden="true"></span>
          <p class="overlay-title">{{ mode === 'hls' ? '正在连接 HLS 播放…' : '正在建立 WebRTC 连接…' }}</p>
          <p v-if="overlayNote" class="overlay-note">{{ overlayNote }}</p>
          <div v-if="overlayNote" class="overlay-actions">
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

      <p v-if="controlNote" class="stall-chip control-chip">
        <span>{{ controlNote }}</span>
        <button type="button" class="chip-btn" @click="controlNote = ''">知道了</button>
      </p>

      <p v-if="issue && issue.level === 'error'" class="codec-hint">
        <span>{{ issue.title }}</span>
        <button
          v-if="issue.suggestMode"
          type="button"
          class="overlay-btn"
          @click="switchMode(issue.suggestMode!)"
        >
          换到 {{ issue.suggestMode === 'hls' ? 'HLS' : 'WebRTC' }}
        </button>
      </p>

      <div v-show="controlsVisible || phase !== 'playing'" class="controls">
        <span class="live-chip"><i aria-hidden="true"></i>LIVE</span>

        <div v-if="hasModes" class="mode-switch" role="group" aria-label="播放通道">
          <button
            type="button"
            class="mode-btn"
            :class="{ active: channel === 'llhls' }"
            :disabled="!playback?.hlsUrl"
            :title="CHANNEL_HINTS.llhls"
            @click="setChannel('llhls')"
          >
            LL-HLS
          </button>
          <button
            type="button"
            class="mode-btn"
            :class="{ active: channel === 'hls' }"
            :disabled="!playback?.hlsUrl"
            :title="CHANNEL_HINTS.hls"
            @click="setChannel('hls')"
          >
            HLS
          </button>
          <button
            type="button"
            class="mode-btn"
            :class="{ active: channel === 'webrtc' }"
            :disabled="!playback?.whepUrl"
            :title="CHANNEL_HINTS.webrtc"
            @click="setChannel('webrtc')"
          >
            WebRTC
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
          :class="{ on: showSettings }"
          title="播放参数"
          @click="toggleSettings"
        >
          ⚙
        </button>
        <button
          type="button"
          class="ctrl"
          :class="{ on: showStats }"
          title="显示播放统计"
          @click="toggleStats"
        >
          🩺
        </button>
        <button type="button" class="ctrl" title="刷新播放" aria-label="刷新播放" @click="refreshPlayback">
          ↻
        </button>
        <button type="button" class="ctrl" :class="{ on: muted }" :title="muted ? '取消静音' : '静音'" @click="toggleMute">
          {{ muted ? '🔇' : '🔊' }}
        </button>
        <button type="button" class="ctrl" :class="{ on: theater }" title="网页全屏" @click="toggleTheater">⛶</button>
        <button type="button" class="ctrl" title="全屏" @click="toggleFullscreen">⤢</button>
        <button type="button" class="ctrl" title="画中画" @click="togglePip">⧉</button>
      </div>

    </div>

    <aside v-if="showSettings && !theater" class="settings">
      <div class="settings-head">
        <p class="settings-title">播放参数</p>
        <p class="settings-hint">换档位或换播放方式会重建一次播放器（黑屏 0.3~1 秒），选择记在本机。</p>
      </div>
      <div v-if="mode === 'hls'" class="settings-body">
        <div class="settings-row">
          <span class="settings-label">延迟档位</span>
          <div class="seg" role="group" aria-label="延迟档位">
            <button
              v-for="option in TIER_OPTIONS"
              :key="option.value"
              type="button"
              class="tier-btn"
              :class="{ active: tier === option.value }"
              :disabled="!llhls || engine === 'native'"
              :title="tierTitle(option.value)"
              @click="setTier(option.value)"
            >
              {{ option.label }}
            </button>
          </div>
          <p class="settings-note">{{ llhls ? tierText : '当前是 HLS 通道（整段分片），档位不生效。' }}</p>
        </div>
        <div class="settings-row">
          <span class="settings-label">播放方式</span>
          <div class="seg" role="group" aria-label="播放方式">
            <button
              v-for="option in ENGINE_OPTIONS"
              :key="option.value"
              type="button"
              class="engine-btn"
              :class="{ active: enginePref === option.value }"
              :disabled="option.value === 'native' && !nativeReady"
              :title="
                option.value === 'native' && !nativeReady
                  ? '这个浏览器没有原生 HLS 播放能力（只有 Safari / iOS 有）'
                  : option.hint
              "
              @click="setEngine(option.value)"
            >
              {{ option.label }}
            </button>
          </div>
          <p class="settings-note">{{ engineHint }}</p>
        </div>
      </div>
      <p v-else class="settings-note">WebRTC 通道既不解析分片、也不经过 HLS.js，所以档位与播放方式在这里不适用。</p>
    </aside>

    <aside v-if="showStats && !theater" class="stats">
      <div class="stats-chart">
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
            v-for="tab in metrics"
            :key="tab.key"
            type="button"
            class="chart-tab"
            :class="{ active: metric === tab.key }"
            @click="metric = tab.key"
          >
            {{ tab.label }}
          </button>
        </div>
      </div>

      <div class="stats-facts">
        <p v-if="engineNotice" class="stats-note engine-notice">{{ engineNotice }}</p>
        <dl class="stats-grid">
          <template v-for="row in grid" :key="row.label">
            <dt>{{ row.label }}</dt>
            <dd>{{ row.value }}</dd>
          </template>
        </dl>
        <p v-if="mode === 'hls'" class="stats-note">
          HLS 走 HTTP 分片，拿不到 RTT / 抖动 / 丢包；要看这些请切到「WebRTC」通道。
          <template v-if="engine === 'native'">
            原生播放器不把分片信息交给页面，所以码率、下载速率这类指标为空；它也可能按整段分片播放，延迟由系统决定。
          </template>
          <template v-else-if="delivery === 'll-hls'">
            当前清单带 LL-HLS 分片（0.2 秒一片），播放器贴着直播边缘起播，延迟通常在 1 秒内。
          </template>
          <template v-else-if="!llhls">
            已关闭 LL-HLS：按整段分片缓冲，延迟升到十几秒，换来弱网下不卡。
          </template>
        </p>
        <p v-else class="stats-note">
          WebRTC 直接收 RTP：这里的「缓冲余量」实际是接收端的抖动缓冲，和 HLS 的分片缓冲不是一回事。
        </p>
      </div>
    </aside>
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
  top: 52px;
  bottom: auto;
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

.control-chip {
  top: 96px;
  bottom: auto;
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
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding: 10px 12px;
  background: linear-gradient(0deg, rgba(3, 6, 12, 0.9), rgba(3, 6, 12, 0));
  transition: opacity 0.18s ease;
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

.mode-btn,
.tier-btn {
  padding: 3px 9px;
  border-radius: 6px;
  border: none;
  background: transparent;
  color: var(--text-dim);
  font-size: 11.5px;
  cursor: pointer;
}

.mode-btn.active,
.tier-btn.active {
  background: rgba(99, 102, 241, 0.35);
  color: #e0e7ff;
}

.mode-btn:disabled,
.tier-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

/* 「播放参数」卡片里的分段按钮：和播放器上的通道开关同一套观感。 */
.seg {
  display: inline-flex;
  padding: 2px;
  border-radius: 8px;
  background: rgba(15, 23, 42, 0.8);
  border: 1px solid var(--line);
  justify-self: start;
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

.player-layout {
  display: block;
}

/* 统计面板收在播放器下方，不再盖住画面：宽屏左右分栏，窄屏上下叠放。 */
.stats {
  display: grid;
  gap: 16px;
  margin-top: 12px;
  padding: 16px 18px;
  border-radius: 16px;
  border: 1px solid var(--line);
  background: rgba(12, 18, 28, 0.92);
}

.stats-chart,
.stats-facts {
  min-width: 0;
}

@media (min-width: 880px) {
  .stats {
    grid-template-columns: minmax(0, 1.1fr) minmax(0, 0.9fr);
    align-items: start;
  }
}

.stats-note {
  margin: 10px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--muted);
}

/* 播放参数卡片：档位与播放方式各自一块，收在播放器下方，不挡画面。 */
.settings {
  display: grid;
  gap: 14px;
  margin-top: 12px;
  padding: 16px 18px;
  border-radius: 16px;
  border: 1px solid var(--line);
  background: rgba(12, 18, 28, 0.92);
}

.settings-head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 8px;
}

.settings-title {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
}

.settings-hint {
  margin: 0;
  font-size: 11.5px;
  color: var(--muted);
}

.settings-body {
  display: grid;
  gap: 14px;
}

@media (min-width: 720px) {
  .settings-body {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    align-items: start;
  }
}

.settings-row {
  display: grid;
  gap: 8px;
  min-width: 0;
}

.settings-label {
  font-size: 11.5px;
  color: var(--text-faint);
}

.settings-note {
  margin: 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--muted);
}

.engine-btn {
  padding: 3px 10px;
  border-radius: 6px;
  border: none;
  background: transparent;
  color: var(--text-dim);
  font-size: 11.5px;
  cursor: pointer;
}

.engine-btn.active {
  background: rgba(99, 102, 241, 0.35);
  color: #e0e7ff;
}

.engine-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.engine-notice {
  margin: 0 0 12px;
  padding: 8px 10px;
  border-radius: 10px;
  border: 1px solid rgba(251, 191, 36, 0.35);
  background: rgba(251, 191, 36, 0.12);
  color: #fde68a;
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
  grid-template-columns: auto minmax(0, 1fr);
  gap: 5px 12px;
  margin: 0;
  font-size: 12px;
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
    padding: 14px;
  }
}
</style>
