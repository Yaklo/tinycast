/**
 * 从 RTCPeerConnection.getStats() 的报告里抽出界面上要展示的实时指标。
 *
 * 拆成纯函数的原因：浏览器里拿不到可复现的 stats 报告，而这套换算（码率要走差分、
 * 丢包率要按窗口算、编码名要回查 codec 表）恰恰是最容易算错的地方，需要单测覆盖。
 */

export interface StatsSample {
  /** 采样时间（Unix 毫秒）。 */
  at: number
  /** 视频码率（kbps），按相邻两次采样的字节差换算。 */
  videoKbps: number
  /** 音频码率（kbps）。 */
  audioKbps: number
  /** 播放帧率（fps）。 */
  fps: number
  width: number
  height: number
  /** 视频编码，例如 H264；尚未协商出编码时为空串。 */
  videoCodec: string
  audioCodec: string
  /** 当前候选对的往返时延（毫秒），拿不到时为 0。 */
  rttMs: number
  /** RTP 抖动（毫秒）。 */
  jitterMs: number
  /** 抖动缓冲的平均停留时间（毫秒），可近似看作播放端额外引入的延迟。 */
  bufferMs: number
  /** 距直播边缘的延迟（毫秒）；HLS 由分片清单推算，WebRTC 拿不到时为 0。 */
  latencyMs: number
  /** 实测下载速率（kbps）；HLS 按分片请求耗时算，WebRTC 等于音视频码率之和。 */
  downloadKbps: number
  /** 窗口内丢失的包数。 */
  lost: number
  /** 窗口内丢包率（0~1）。 */
  lossRate: number
  /** 累计解码帧数，用于判断"连上了但一直没有画面"。 */
  framesDecoded: number
  /** 累计收到的字节数（视频 + 音频）。 */
  receivedBytes: number
  /** 累计收到的视频字节数，仅供下一次差分。 */
  videoBytes: number
  /** 累计收到的音频字节数，仅供下一次差分。 */
  audioBytes: number
  /** 累计收到的 RTP 包数（视频 + 音频），仅供下一次差分。 */
  receivedPackets: number
  /** 累计丢失的 RTP 包数（视频 + 音频），仅供下一次差分。 */
  lostTotal: number
}

/** stats 报告里的一条记录；字段名随浏览器略有差异，因此统一按 map 处理。 */
export type StatEntry = Record<string, unknown>

export function emptySample(at = 0): StatsSample {
  return {
    at,
    videoKbps: 0,
    audioKbps: 0,
    fps: 0,
    width: 0,
    height: 0,
    videoCodec: '',
    audioCodec: '',
    rttMs: 0,
    jitterMs: 0,
    bufferMs: 0,
    latencyMs: 0,
    downloadKbps: 0,
    lost: 0,
    lossRate: 0,
    framesDecoded: 0,
    receivedBytes: 0,
    videoBytes: 0,
    audioBytes: 0,
    receivedPackets: 0,
    lostTotal: 0,
  }
}

function num(value: unknown): number {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    if (Number.isFinite(parsed)) return parsed
  }
  return 0
}

/** "video/H264" → "H264"；"audio/opus" → "OPUS"；rtx/red/ulpfec 这类辅助编码返回空串。 */
export function codecLabel(mimeType?: unknown): string {
  if (typeof mimeType !== 'string' || !mimeType) return ''
  const base = mimeType.split('/')[1] ?? ''
  const name = base.trim().toUpperCase()
  if (!name || name === 'RTX' || name === 'RED' || name === 'ULPFEC') return ''
  return name
}

function trackOf(items: StatEntry[], kind: string): StatEntry | undefined {
  return items.find((item) => {
    if (item.type !== 'inbound-rtp') return false
    return String(item.kind ?? item.mediaType ?? '') === kind
  })
}

/** 优先取浏览器标记为 selected 的候选对，退而取已成功且已测出 RTT 的那一对。 */
function selectedPairOf(items: StatEntry[]): StatEntry | undefined {
  const pairs = items.filter((item) => item.type === 'candidate-pair')
  return (
    pairs.find((item) => item.selected === true) ??
    pairs.find((item) => item.state === 'succeeded' && num(item.currentRoundTripTime) > 0) ??
    pairs.find((item) => item.state === 'succeeded')
  )
}

/** codecId → mimeType，用来把 inbound-rtp 的 codecId 还原成 H264 / opus 这样的名字。 */
function codecTable(items: StatEntry[]): Map<string, string> {
  const table = new Map<string, string>()
  for (const item of items) {
    if (item.type !== 'codec') continue
    const id = typeof item.id === 'string' ? item.id : ''
    const mime = typeof item.mimeType === 'string' ? item.mimeType : ''
    if (id && mime) table.set(id, mime)
  }
  return table
}

/**
 * 生成一次采样。
 *
 * prev 为上一次采样：码率、丢包率、帧率都要两次采样的差分才能算出，
 * 因此第一次调用（prev 为空）时刻率类指标一律为 0，不拿累计值冒充实时值。
 */
export function collectSample(items: StatEntry[], at: number, prev?: StatsSample | null): StatsSample {
  const codecs = codecTable(items)
  const video = trackOf(items, 'video')
  const audio = trackOf(items, 'audio')
  const pair = selectedPairOf(items)

  const sample = emptySample(at)

  const videoBytes = num(video?.bytesReceived)
  const audioBytes = num(audio?.bytesReceived)
  const packetsReceived = num(video?.packetsReceived) + num(audio?.packetsReceived)
  const packetsLost = Math.max(0, num(video?.packetsLost)) + Math.max(0, num(audio?.packetsLost))

  sample.receivedBytes = videoBytes + audioBytes
  sample.videoBytes = videoBytes
  sample.audioBytes = audioBytes
  sample.receivedPackets = packetsReceived
  sample.lostTotal = packetsLost
  sample.framesDecoded = num(video?.framesDecoded)
  sample.width = num(video?.frameWidth)
  sample.height = num(video?.frameHeight)
  sample.videoCodec = codecLabel(codecs.get(String(video?.codecId ?? '')))
  sample.audioCodec = codecLabel(codecs.get(String(audio?.codecId ?? '')))

  const jitterBufferDelay = num(video?.jitterBufferDelay) || num(audio?.jitterBufferDelay)
  const jitterEmitted = num(video?.jitterBufferEmittedCount) || num(audio?.jitterBufferEmittedCount)
  if (jitterBufferDelay > 0 && jitterEmitted > 0) {
    sample.bufferMs = Math.round((jitterBufferDelay / jitterEmitted) * 1000)
  }

  sample.jitterMs = Math.round((num(video?.jitter) || num(audio?.jitter)) * 1000)
  sample.rttMs = Math.round(num(pair?.currentRoundTripTime) * 1000)

  const reportedFps = num(video?.framesPerSecond)
  if (reportedFps > 0) sample.fps = Math.round(reportedFps * 10) / 10

  if (prev && prev.at > 0 && at > prev.at) {
    const seconds = (at - prev.at) / 1000
    // 累计量在重连后会归零，负数一律夹到 0。
    sample.videoKbps = Math.max(0, Math.round(((videoBytes - prev.videoBytes) * 8) / 1000 / seconds))
    sample.audioKbps = Math.max(0, Math.round(((audioBytes - prev.audioBytes) * 8) / 1000 / seconds))
    if (!reportedFps) {
      sample.fps = Math.max(0, Math.round(((sample.framesDecoded - prev.framesDecoded) / seconds) * 10) / 10)
    }
    sample.lost = Math.max(0, packetsLost - prev.lostTotal)
    sample.downloadKbps = sample.videoKbps + sample.audioKbps
    const receivedDelta = Math.max(0, packetsReceived - prev.receivedPackets)
    const total = receivedDelta + sample.lost
    sample.lossRate = total > 0 ? sample.lost / total : 0
  }

  return sample
}

/** 折线在纵向留 10% 余量，免得峰值贴着边框被裁掉一半描边。 */
const CHART_HEADROOM = 1.1

/**
 * 把一串采样值画成折线路径（SVG path 的 d 属性）。
 *
 * 归一化上限取序列最大值再乘 1.1 留出余量，且下限为 1 以免全 0 时除零；
 * 少于两个点时返回空串，界面上改显示当前值。
 */
export function sparklinePath(values: number[], width: number, height: number): string {
  if (values.length < 2 || width <= 0 || height <= 0) return ''
  const peak = Math.max(1, ...values.map((v) => (Number.isFinite(v) ? v : 0))) * CHART_HEADROOM
  const stepX = width / (values.length - 1)
  return values
    .map((value, index) => {
      const safe = Number.isFinite(value) ? Math.max(0, value) : 0
      const x = Math.round(index * stepX * 100) / 100
      const y = Math.round((height - (safe / peak) * height) * 100) / 100
      return (index === 0 ? 'M' : 'L') + x + ' ' + y
    })
    .join(' ')
}
