<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { fetchPlay } from '../api'
import { describeMedia, noFrameHint } from '../lib/playback'
import { collectSample, emptySample, sparklinePath } from '../lib/stats'
import type { StatsSample, StatEntry } from '../lib/stats'
import type { MediaInfo, RoomView } from '../types'

const props = defineProps<{ room: RoomView; media?: MediaInfo | null }>()

type Phase = 'connecting' | 'playing' | 'error'

/** 折线图可切换的指标；默认是"下载速率"，也就是观众端实际拉到的码流。 */
type MetricKey = 'kbps' | 'fps' | 'bufferMs' | 'rttMs' | 'lossRate'

const METRICS: { key: MetricKey; label: string; unit: string }[] = [
  { key: 'kbps', label: '下载速率', unit: 'kbps' },
  { key: 'fps', label: '帧率', unit: 'fps' },
  { key: 'bufferMs', label: '缓冲延迟', unit: 'ms' },
  { key: 'rttMs', label: 'RTT', unit: 'ms' },
  { key: 'lossRate', label: '丢包率', unit: '%' },
]

/** 折线保留的采样点数：1 秒 1 点，60 点即最近一分钟。 */
const HISTORY_LIMIT = 60

const stageEl = ref<HTMLElement | null>(null)
const videoEl = ref<HTMLVideoElement | null>(null)
const phase = ref<Phase>('connecting')
const message = ref('正在建立连接…')
const muted = ref(true)
const showStats = ref(false)
const media = ref<MediaInfo | null>(props.media ?? null)

/** 最近一次采样，界面上的所有数字都来自它。 */
const sample = ref<StatsSample>(emptySample())
/** 采样历史，供折线图使用。 */
const history = ref<StatsSample[]>([])
const metric = ref<MetricKey>('kbps')

const iceState = ref('-')
const connectionState = ref('-')
/** 播放中突然没有新帧时的提示（例如推流端停采、网络中断）。 */
const stallNote = ref('')

const theater = ref(false)
const fullscreen = ref(false)

const pipSupported = typeof document !== 'undefined' && 'pictureInPictureEnabled' in document

let pc: RTCPeerConnection | null = null
let attempt = 0
let retryTimer: number | null = null
let monitorTimer: number | null = null
let iceTimer: number | null = null

function clearRetry(): void {
  if (retryTimer !== null) {
    window.clearTimeout(retryTimer)
    retryTimer = null
  }
}

function stopMonitor(): void {
  if (monitorTimer !== null) {
    window.clearInterval(monitorTimer)
    monitorTimer = null
  }
}

function stopIceTimer(): void {
  if (iceTimer !== null) {
    window.clearTimeout(iceTimer)
    iceTimer = null
  }
}

function teardown(): void {
  clearRetry()
  stopMonitor()
  stopIceTimer()
  if (pc) {
    pc.ontrack = null
    pc.onconnectionstatechange = null
    pc.close()
    pc = null
  }
  if (videoEl.value) {
    videoEl.value.srcObject = null
  }
  sample.value = emptySample()
  history.value = []
  iceState.value = '-'
  connectionState.value = '-'
  stallNote.value = ''
}

/** 等 ICE 收集完成再发 offer：SRS 的 WHEP 需要 offer 里已带候选地址。 */
function waitForIce(connection: RTCPeerConnection, timeoutMs = 2500): Promise<void> {
  if (connection.iceGatheringState === 'complete') return Promise.resolve()
  return new Promise((resolve) => {
    let timer = 0
    const finish = () => {
      window.clearTimeout(timer)
      connection.removeEventListener('icegatheringstatechange', onChange)
      resolve()
    }
    const onChange = () => {
      if (connection.iceGatheringState === 'complete') finish()
    }
    connection.addEventListener('icegatheringstatechange', onChange)
    timer = window.setTimeout(finish, timeoutMs)
  })
}

function retryLater(reason: string, delay: number): void {
  phase.value = 'error'
  message.value = reason
  clearRetry()
  retryTimer = window.setTimeout(() => {
    void start()
  }, delay)
}

/**
 * 连接建立后每秒采集一次统计，并顺带盯着"有没有画面"。
 *
 * WebRTC 的坑在于"连上了"不等于"有画面"：编码不受支持、防火墙挡了媒体口、
 * 候选地址不可达时 connectionState 照样是 connected，只是永远没有 RTP。
 * 连续 6 秒拿不到解码帧就明确报出来，而不是留一块黑屏。
 */
function startMonitor(generation: number, connection: RTCPeerConnection): void {
  stopMonitor()
  const startedAt = Date.now()
  let lastFrameAt = 0

  monitorTimer = window.setInterval(() => {
    if (generation !== attempt || connection !== pc) {
      stopMonitor()
      return
    }
    void connection
      .getStats()
      .then((report) => {
        if (generation !== attempt || connection !== pc) return
        const items: StatEntry[] = []
        report.forEach((entry) => items.push(entry as unknown as StatEntry))

        const now = Date.now()
        const next = collectSample(items, now, sample.value)
        sample.value = next
        history.value = [...history.value, next].slice(-HISTORY_LIMIT)

        iceState.value = connection.iceConnectionState
        connectionState.value = connection.connectionState

        if (next.framesDecoded > 0) {
          lastFrameAt = now
          if (phase.value !== 'playing') {
            phase.value = 'playing'
            message.value = ''
          }
        }

        if (phase.value === 'playing') {
          stallNote.value = lastFrameAt > 0 && now - lastFrameAt > 8000 ? '已 8 秒没有收到新的视频帧' : ''
          return
        }
        if (now - startedAt > 6000) {
          phase.value = 'error'
          message.value = noFrameHint(media.value)
        }
      })
      .catch(() => {
        // getStats 在连接关闭时会抛，忽略即可
      })
  }, 1000)
}

/** 把一条 stats 记录里的"视频码率 + 音频码率"作为下载速率。 */
function metricValue(value: StatsSample, key: MetricKey): number {
  if (key === 'kbps') return value.videoKbps + value.audioKbps
  return value[key]
}

const series = computed(() => history.value.map((item) => metricValue(item, metric.value)))
const chartPath = computed(() => sparklinePath(series.value, 260, 64))
const currentMetric = computed(() => METRICS.find((item) => item.key === metric.value) ?? METRICS[0])

function formatNumber(value: number, digits = 0): string {
  if (!Number.isFinite(value)) return '-'
  const fixed = value.toFixed(digits)
  // 去掉无意义的尾零：1.00 → 1、2.50 → 2.5，整数值保持整数
  return fixed.includes('.') ? fixed.replace(/\.?0+$/, '') : fixed
}

/** 码率习惯用 Mbps 展示大数，其余保持 kbps。 */
function formatKbps(kbps: number): string {
  if (!kbps) return '-'
  if (kbps >= 1000) return formatNumber(kbps / 1000, 2) + ' Mbps'
  return formatNumber(kbps) + ' kbps'
}

function formatMs(ms: number): string {
  return ms > 0 ? formatNumber(ms) + ' ms' : '-'
}

function formatRate(rate: number): string {
  return rate > 0 ? formatNumber(rate * 100, 1) + '%' : '0%'
}

function formatBytes(bytes: number): string {
  if (!bytes) return '-'
  const mb = bytes / (1024 * 1024)
  if (mb >= 1024) return formatNumber(mb / 1024, 2) + ' GB'
  if (mb >= 1) return formatNumber(mb, 1) + ' MB'
  return formatNumber(bytes / 1024) + ' KB'
}

const currentValue = computed(() => metricValue(sample.value, metric.value))
const peakValue = computed(() => (series.value.length ? Math.max(0, ...series.value) : 0))

/** 按当前选中的指标决定单位，图表上的当前值与峰值都走它。 */
function formatMetric(value: number): string {
  switch (metric.value) {
    case 'kbps':
      return formatKbps(value)
    case 'fps':
      return formatNumber(value, 1) + ' fps'
    case 'lossRate':
      return formatRate(value)
    default:
      return formatMs(value)
  }
}

/** 视频编码优先用播放器实际协商到的，其次才是后端从 SRS 读到的。 */
const videoCodec = computed(() => sample.value.videoCodec || media.value?.videoCodec || '未知')
const audioCodec = computed(() => sample.value.audioCodec || media.value?.audioCodec || '未知')
const resolution = computed(() => {
  const { width, height } = sample.value
  if (width && height) return width + '×' + height
  const m = media.value
  return m?.width && m?.height ? m.width + '×' + m.height : '-'
})

async function start(): Promise<void> {
  const generation = ++attempt
  clearRetry()
  teardown()

  if (props.room.state !== 'LIVE') {
    phase.value = 'error'
    message.value = '推流已断开'
    return
  }

  phase.value = 'connecting'
  message.value = '正在建立连接…'

  try {
    const play = await fetchPlay(props.room.username)
    if (generation !== attempt) return

    if (play.media) media.value = play.media

    if (!play.playback) {
      phase.value = 'error'
      message.value = '推流已断开'
      return
    }

    const connection = new RTCPeerConnection({ iceServers: [], bundlePolicy: 'max-bundle' })
    pc = connection

    connection.addTransceiver('video', { direction: 'recvonly' })
    connection.addTransceiver('audio', { direction: 'recvonly' })

    connection.ontrack = (event: RTCTrackEvent) => {
      const el = videoEl.value
      if (!el) return
      const stream = event.streams[0]
      if (stream && el.srcObject !== stream) {
        el.srcObject = stream
      }
      void el.play().catch(() => {
        // 自动播放被拦截时，由用户点击画面/重连按钮兜底
      })
    }

    connection.onconnectionstatechange = () => {
      if (connection !== pc) return
      connectionState.value = connection.connectionState
      const state = connection.connectionState
      if (state === 'connected') {
        // 连上只代表信令与 ICE 通了，要有解码帧才算真正播起来。
        stopIceTimer()
        message.value = '已连接，正在接收画面…'
        startMonitor(generation, connection)
      } else if (state === 'failed') {
        retryLater('连接中断，正在重试…', 1500)
      } else if (state === 'disconnected') {
        message.value = '网络抖动，正在恢复…'
      }
    }

    // 9 秒还没连上，基本可以断定 UDP 8000 不通（防火墙/安全组/candidate 配错）。
    stopIceTimer()
    iceTimer = window.setTimeout(() => {
      if (generation !== attempt || connection !== pc) return
      if (connection.connectionState === 'connected') return
      phase.value = 'error'
      message.value =
        '无法建立 WebRTC 连接（ICE ' +
        connection.iceConnectionState +
        '）：服务器的 UDP 8000 很可能被防火墙/安全组拦截。Windows 请以管理员身份运行 open-firewall.cmd；云服务器要在安全组放行 UDP 8000。'
    }, 9000)

    const offer = await connection.createOffer()
    await connection.setLocalDescription(offer)
    await waitForIce(connection)
    if (generation !== attempt) {
      connection.close()
      return
    }

    const sdp = connection.localDescription?.sdp ?? offer.sdp ?? ''
    const res = await fetch(play.playback.whepUrl, {
      method: 'POST',
      headers: { 'Content-Type': 'application/sdp' },
      body: sdp,
    })
    if (generation !== attempt) return
    if (!res.ok) {
      retryLater('信令失败（HTTP ' + res.status + '），正在重试…', 2500)
      return
    }

    // 没配 SRS_RTC_PROXY_TARGET 时 /rtc/ 会被前端页面兜底，返回的是 index.html，
    // 直接 setRemoteDescription 只会得到一句看不懂的 SDP 解析错误。
    const contentType = res.headers.get('content-type') ?? ''
    if (!contentType.includes('sdp')) {
      phase.value = 'error'
      message.value =
        '信令地址返回了网页而不是 SDP：后端没有配置 SRS_RTC_PROXY_TARGET，/rtc/ 请求被前端页面兜底了。请在环境变量里补上 SRS_RTC_PROXY_TARGET=http://127.0.0.1:1985 后重启。'
      return
    }

    const answer = await res.text()
    await connection.setRemoteDescription({ type: 'answer', sdp: answer })
  } catch (err) {
    if (generation !== attempt) return
    const reason = err instanceof Error ? err.message : '播放失败'
    retryLater('连接失败：' + reason, 2500)
  }
}

function toggleMute(): void {
  const el = videoEl.value
  if (!el) return
  el.muted = !el.muted
  muted.value = el.muted
}

function manualRetry(): void {
  void start()
}

/** 网页全屏：让播放器铺满视口，同时锁住页面滚动，Esc 退出。 */
function toggleTheater(): void {
  theater.value = !theater.value
  document.body.style.overflow = theater.value ? 'hidden' : ''
}

/** 浏览器原生全屏，按 F11 之外的"真全屏"体验来用。 */
function toggleFullscreen(): void {
  const el = stageEl.value
  if (!el) return
  if (document.fullscreenElement) {
    void document.exitFullscreen().catch(() => {})
    return
  }
  if (typeof el.requestFullscreen !== 'function') return
  void el.requestFullscreen().catch(() => {})
}

function onFullscreenChange(): void {
  fullscreen.value = document.fullscreenElement === stageEl.value
}

async function togglePip(): Promise<void> {
  const el = videoEl.value
  if (!el || !pipSupported) return
  try {
    if (document.pictureInPictureElement) {
      await document.exitPictureInPicture()
    } else {
      await el.requestPictureInPicture()
    }
  } catch {
    // 用户拒绝或浏览器限制（例如视频未播放）时静默失败
  }
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key !== 'Escape') return
  if (theater.value) toggleTheater()
}

/** 双击画面在全屏与普通之间切换，和大多数播放器一致。 */
function onStageDoubleClick(): void {
  if (theater.value) {
    toggleTheater()
    return
  }
  toggleFullscreen()
}

/** 只在身份或状态真正变化时重连；观看人数等字段变化不应重建连接。 */
watch(() => props.room.username + ':' + props.room.state, () => void start(), { immediate: true })

watch(
  () => props.media,
  (value) => {
    media.value = value ?? null
  },
)

window.addEventListener('keydown', onKeydown)
document.addEventListener('fullscreenchange', onFullscreenChange)

onBeforeUnmount(() => {
  attempt += 1
  teardown()
  window.removeEventListener('keydown', onKeydown)
  document.removeEventListener('fullscreenchange', onFullscreenChange)
  document.body.style.overflow = ''
})
</script>

<template>
  <div
    ref="stageEl"
    class="player"
    :class="{ 'is-theater': theater }"
    @dblclick="onStageDoubleClick"
  >
    <video ref="videoEl" class="video" playsinline autoplay muted></video>

    <div v-if="phase !== 'playing'" class="overlay">
      <span v-if="phase === 'connecting'" class="spinner" aria-hidden="true"></span>
      <span class="overlay-text">{{ message }}</span>
      <button v-if="phase === 'error'" type="button" class="btn btn-ghost btn-sm" @click="manualRetry">重新连接</button>
    </div>

    <p v-if="stallNote" class="stall-chip">{{ stallNote }}，正在等待恢复…</p>

    <div class="controls">
      <span class="live-chip" aria-hidden="true">
        <span class="live-chip-dot"></span>
        LIVE
      </span>
      <span class="stream-url">{{ room.username }}</span>
      <span v-if="sample.videoKbps" class="rate-chip">{{ formatKbps(sample.videoKbps + sample.audioKbps) }}</span>
      <span v-if="describeMedia(media)" class="media-chip">{{ videoCodec }} · {{ resolution }}</span>

      <button
        type="button"
        class="icon-btn"
        :class="{ active: showStats }"
        :title="showStats ? '隐藏播放统计' : '显示播放统计'"
        @click="showStats = !showStats"
      >
        🩺
      </button>
      <button type="button" class="icon-btn" :title="muted ? '开启声音' : '静音'" @click="toggleMute">
        <template v-if="muted">🔇</template>
        <template v-else>🔊</template>
      </button>
      <button
        type="button"
        class="icon-btn"
        :title="theater ? '退出网页全屏（Esc）' : '网页全屏'"
        @click="toggleTheater"
      >
        {{ theater ? '🗗' : '⛶' }}
      </button>
      <button
        type="button"
        class="icon-btn"
        :title="fullscreen ? '退出全屏' : '全屏'"
        @click="toggleFullscreen"
      >
        {{ fullscreen ? '🗙' : '⤢' }}
      </button>
      <button
        v-if="pipSupported"
        type="button"
        class="icon-btn"
        title="画中画"
        @click="togglePip"
      >
        ⧉
      </button>
    </div>

    <section v-if="showStats" class="stats">
      <header class="stats-head">
        <h3>播放统计</h3>
        <button type="button" class="stats-close" aria-label="关闭播放统计" @click="showStats = false">×</button>
      </header>

      <div class="chart-block">
        <div class="chart-top">
          <span class="chart-value">{{ formatMetric(currentValue) }}</span>
          <span class="chart-peak">峰值 {{ formatMetric(peakValue) }}</span>
        </div>
        <div class="chart-tabs">
          <button
            v-for="item in METRICS"
            :key="item.key"
            type="button"
            class="chart-tab"
            :class="{ active: metric === item.key }"
            @click="metric = item.key"
          >
            {{ item.label }}
          </button>
        </div>
        <svg class="chart" viewBox="0 0 260 64" preserveAspectRatio="none" role="img" :aria-label="currentMetric.label + ' 曲线'">
          <path v-if="chartPath" :d="chartPath" class="chart-line" />
          <text v-else x="130" y="36" class="chart-empty" text-anchor="middle">正在采样…</text>
        </svg>
      </div>

      <dl class="stats-grid">
        <div><dt>视频编码</dt><dd>{{ videoCodec }}</dd></div>
        <div><dt>音频编码</dt><dd>{{ audioCodec }}</dd></div>
        <div><dt>分辨率</dt><dd>{{ resolution }}</dd></div>
        <div><dt>播放帧率</dt><dd>{{ sample.fps ? formatNumber(sample.fps, 1) + ' fps' : '-' }}</dd></div>
        <div><dt>视频码率</dt><dd>{{ formatKbps(sample.videoKbps) }}</dd></div>
        <div><dt>音频码率</dt><dd>{{ formatKbps(sample.audioKbps) }}</dd></div>
        <div><dt>丢包</dt><dd>{{ sample.lost }} 包 · {{ formatRate(sample.lossRate) }}</dd></div>
        <div><dt>抖动</dt><dd>{{ formatMs(sample.jitterMs) }}</dd></div>
        <div><dt>缓冲延迟</dt><dd>{{ formatMs(sample.bufferMs) }}</dd></div>
        <div><dt>往返时延</dt><dd>{{ formatMs(sample.rttMs) }}</dd></div>
        <div><dt>累计接收</dt><dd>{{ formatBytes(sample.receivedBytes) }}</dd></div>
        <div><dt>解码帧</dt><dd>{{ sample.framesDecoded }}</dd></div>
        <div><dt>ICE</dt><dd>{{ iceState }}</dd></div>
        <div><dt>连接</dt><dd>{{ connectionState }}</dd></div>
      </dl>
    </section>
  </div>
</template>

<style scoped>
.player {
  position: relative;
  aspect-ratio: 16 / 9;
  width: 100%;
  border-radius: 16px;
  overflow: hidden;
  background: #05080d;
  border: 1px solid var(--line);
}

/* 网页全屏：铺满视口，锁定页面滚动（由脚本控制 body overflow）。 */
.player.is-theater,
.player:fullscreen {
  position: fixed;
  inset: 0;
  z-index: 80;
  width: 100vw;
  height: 100vh;
  aspect-ratio: auto;
  border-radius: 0;
  border: none;
  background: #000;
}

.player:fullscreen {
  position: fixed;
}

.video {
  width: 100%;
  height: 100%;
  object-fit: contain;
  background: #05080d;
  display: block;
}

.overlay {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 14px;
  background: radial-gradient(circle at center, rgba(15, 23, 42, 0.75), rgba(5, 8, 13, 0.92));
  color: var(--text-dim);
  font-size: 13.5px;
  text-align: center;
  padding: 20px;
}

.overlay-text {
  max-width: 80%;
  line-height: 1.6;
}

.spinner {
  width: 26px;
  height: 26px;
  border-radius: 50%;
  border: 2px solid rgba(255, 255, 255, 0.16);
  border-top-color: var(--accent-2);
  animation: spin 0.9s linear infinite;
}

.stall-chip {
  position: absolute;
  top: 12px;
  left: 12px;
  margin: 0;
  padding: 5px 10px;
  border-radius: 999px;
  background: rgba(245, 158, 11, 0.16);
  border: 1px solid rgba(245, 158, 11, 0.4);
  color: #fcd34d;
  font-size: 11.5px;
}

.controls {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 14px;
  background: linear-gradient(180deg, transparent, rgba(3, 6, 11, 0.82));
  opacity: 0;
  transition: opacity 0.2s ease;
}

.player:hover .controls,
.player:focus-within .controls {
  opacity: 1;
}

.live-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  letter-spacing: 0.08em;
  color: #fecdd3;
  background: rgba(244, 63, 94, 0.18);
  border: 1px solid rgba(244, 63, 94, 0.32);
  padding: 3px 8px;
  border-radius: 999px;
}

.live-chip-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #fb7185;
  animation: pulse 1.6s ease-in-out infinite;
}

.stream-url {
  font-family: var(--mono);
  font-size: 12px;
  color: var(--text-dim);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.media-chip,
.rate-chip {
  font-family: var(--mono);
  font-size: 11px;
  color: var(--text-faint);
  border: 1px solid var(--line);
  border-radius: 999px;
  padding: 2px 8px;
  white-space: nowrap;
}

.rate-chip {
  color: #bbf7d0;
  border-color: rgba(74, 222, 128, 0.28);
  background: rgba(22, 101, 52, 0.14);
}

.icon-btn {
  border: 1px solid var(--line);
  background: rgba(13, 18, 25, 0.9);
  color: var(--text);
  width: 30px;
  height: 30px;
  border-radius: 9px;
  cursor: pointer;
  font-size: 13px;
  line-height: 1;
  flex: none;
}

.icon-btn:hover,
.icon-btn.active {
  border-color: rgba(129, 140, 248, 0.6);
  color: #c7d2fe;
}

.stats {
  position: absolute;
  top: 12px;
  right: 12px;
  width: min(320px, calc(100% - 24px));
  max-height: calc(100% - 24px);
  overflow-y: auto;
  padding: 12px 14px 14px;
  border-radius: 14px;
  border: 1px solid var(--line-strong);
  background: rgba(6, 10, 16, 0.92);
  backdrop-filter: blur(8px);
  color: var(--text-dim);
  font-size: 11.5px;
}

.stats-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 10px;
}

.stats-head h3 {
  margin: 0;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--text);
}

.stats-close {
  border: none;
  background: transparent;
  color: var(--text-faint);
  font-size: 15px;
  line-height: 1;
  cursor: pointer;
}

.chart-block {
  border: 1px solid var(--line);
  border-radius: 12px;
  padding: 10px 10px 6px;
  background: rgba(255, 255, 255, 0.02);
  margin-bottom: 12px;
}

.chart-top {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 8px;
}

.chart-value {
  font-size: 16px;
  font-weight: 600;
  color: var(--text);
  font-variant-numeric: tabular-nums;
}

.chart-peak {
  font-size: 10.5px;
  color: var(--text-faint);
}

.chart-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-bottom: 8px;
}

.chart-tab {
  border: 1px solid transparent;
  background: transparent;
  color: var(--text-faint);
  font-size: 10.5px;
  padding: 2px 7px;
  border-radius: 999px;
  cursor: pointer;
}

.chart-tab.active {
  color: #c7d2fe;
  border-color: rgba(129, 140, 248, 0.45);
  background: rgba(99, 102, 241, 0.14);
}

.chart {
  width: 100%;
  height: 64px;
  display: block;
}

.chart-line {
  fill: none;
  stroke: var(--accent-2);
  stroke-width: 1.6;
  stroke-linejoin: round;
  stroke-linecap: round;
  vector-effect: non-scaling-stroke;
}

.chart-empty {
  fill: var(--text-faint);
  font-size: 10px;
}

.stats-grid {
  margin: 0;
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 6px 12px;
}

.stats-grid div {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  min-width: 0;
}

.stats-grid dt {
  color: var(--text-faint);
  white-space: nowrap;
}

.stats-grid dd {
  margin: 0;
  font-family: var(--mono);
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.btn-sm {
  padding: 6px 13px;
  font-size: 12.5px;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

@keyframes pulse {
  0%, 100% {
    opacity: 1;
  }
  50% {
    opacity: 0.4;
  }
}
</style>
