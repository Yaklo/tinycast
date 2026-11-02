import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import LivePlayer from './LivePlayer.vue'
import type { RoomView } from '../types'

const { fetchPlayMock, hlsLog } = vi.hoisted(() => ({
  fetchPlayMock: vi.fn(),
  hlsLog: {
    loadSource: [] as string[],
    attachMedia: 0,
    destroyed: 0,
    instances: [] as unknown[],
    handlers: new Map<string, ((event: string, data?: unknown) => void)[]>(),
    emit(event: string, data?: unknown) {
      for (const handler of hlsLog.handlers.get(event) ?? []) handler(event, data)
    },
  },
}))

vi.mock('../api', () => ({ fetchPlay: fetchPlayMock }))

vi.mock('hls.js', () => {
  class FakeHls {
    static Events = {
      MANIFEST_PARSED: 'hlsManifestParsed',
      ERROR: 'hlsError',
      FRAG_LOADED: 'hlsFragLoaded',
      LEVEL_UPDATED: 'hlsLevelUpdated',
    }
    static ErrorTypes = { NETWORK_ERROR: 'networkError', MEDIA_ERROR: 'mediaError' }
    static isSupported = () => true
    bandwidthEstimate = 800_000
    levels = [{ bitrate: 900_000 }]
    latency = 0
    liveSyncPosition = 0
    config: Record<string, unknown> = {}
    constructor(config: Record<string, unknown> = {}) {
      this.config = { ...config }
      hlsLog.instances.push(this)
    }
    loadSource(url: string) {
      hlsLog.loadSource.push(url)
    }
    attachMedia() {
      hlsLog.attachMedia += 1
    }
    on(event: string, handler: (event: string, data?: unknown) => void) {
      const list = hlsLog.handlers.get(event) ?? []
      hlsLog.handlers.set(event, [...list, handler])
    }
    startLoad() {}
    recoverMediaError() {}
    destroy() {
      hlsLog.destroyed += 1
    }
  }
  return { default: FakeHls }
})

interface FakeDescription {
  type: string
  sdp: string
}

const SDP = [
  'v=0',
  'o=- 1 2 IN IP4 127.0.0.1',
  'm=video 9 UDP/TLS/RTP/SAVPF 96',
  '',
].join('\n')

function statsReport(videoBytes: number): Map<string, Record<string, unknown>> {
  return new Map<string, Record<string, unknown>>([
    ['codec-v', { type: 'codec', id: 'codec-v', mimeType: 'video/H264' }],
    ['codec-a', { type: 'codec', id: 'codec-a', mimeType: 'audio/opus' }],
    [
      'v',
      {
        type: 'inbound-rtp',
        kind: 'video',
        codecId: 'codec-v',
        bytesReceived: videoBytes,
        packetsReceived: videoBytes / 1000,
        framesDecoded: Math.round((videoBytes / 125_000) * 30),
        frameWidth: 1280,
        frameHeight: 720,
        jitter: 0.004,
      },
    ],
    ['a', { type: 'inbound-rtp', kind: 'audio', codecId: 'codec-a', bytesReceived: 0, packetsReceived: 0 }],
    ['pair', { type: 'candidate-pair', selected: true, currentRoundTripTime: 0.021 }],
  ])
}

class FakePeerConnection {
  static instances: FakePeerConnection[] = []

  onconnectionstatechange: (() => void) | null = null
  ontrack: ((event: { streams: MediaStream[]; track: unknown }) => void) | null = null
  connectionState = 'new'
  localDescription: FakeDescription | null = null
  remoteDescription: FakeDescription | null = null
  transceivers: string[] = []
  closed = false
  videoBytes = 0

  constructor() {
    FakePeerConnection.instances.push(this)
  }

  addTransceiver(kind: string) {
    this.transceivers.push(kind)
  }

  async createOffer(): Promise<FakeDescription> {
    return { type: 'offer', sdp: SDP }
  }

  async setLocalDescription(description: FakeDescription) {
    this.localDescription = description
  }

  async setRemoteDescription(description: FakeDescription) {
    this.remoteDescription = description
  }

  async getStats() {
    return statsReport(this.videoBytes)
  }

  close() {
    this.closed = true
  }
}

const room: RoomView = {
  username: 'alex',
  room: '深夜写代码',
  state: 'LIVE',
  viewerCount: 3,
  startedAt: Date.now() - 30_000,
  graceUntil: 0,
  media: { videoCodec: 'H264', videoProfile: 'High', width: 1280, height: 720, audioCodec: 'AAC' },
}

function playResponse(protocol: 'hls' | 'webrtc' | 'none', withBoth = false) {
  return {
    username: 'alex',
    state: 'LIVE',
    serverTime: Date.now(),
    graceSeconds: 300,
    media: room.media,
    playback:
      protocol === 'none'
        ? { protocol: 'none' as const }
        : {
            protocol,
            hlsUrl: protocol === 'hls' || withBoth ? '/hls/live/alex/index.m3u8' : undefined,
            whepUrl: protocol === 'webrtc' || withBoth ? '/rtc/live/alex/whep' : undefined,
          },
  }
}

function mountPlayer() {
  return mount(LivePlayer, { props: { room, media: room.media } })
}

function buttonByTitle(wrapper: ReturnType<typeof mountPlayer>, title: string) {
  return wrapper.findAll('button').find((node) => node.attributes('title') === title)
}

beforeEach(() => {
  fetchPlayMock.mockReset()
  hlsLog.loadSource = []
  hlsLog.attachMedia = 0
  hlsLog.destroyed = 0
  hlsLog.instances = []
  hlsLog.handlers = new Map()
  FakePeerConnection.instances = []
  localStorage.clear()
  vi.stubGlobal('RTCPeerConnection', FakePeerConnection)
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(SDP, { status: 201, headers: { 'Content-Type': 'application/sdp' } })),
  )
  // happy-dom 没有 getVideoPlaybackQuality；用例里补过的要在每个用例前清掉，避免互相污染。
  delete (HTMLMediaElement.prototype as { getVideoPlaybackQuality?: unknown }).getVideoPlaybackQuality
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  delete (HTMLMediaElement.prototype as { getVideoPlaybackQuality?: unknown }).getVideoPlaybackQuality
})

describe('LivePlayer', () => {
  it('WebRTC 通道：协商成功后展示实时统计与下载速率曲线', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('webrtc'))
    const wrapper = mountPlayer()
    await flushPromises()

    const pc = FakePeerConnection.instances[0]
    expect(pc.transceivers).toEqual(['video', 'audio'])
    expect(pc.remoteDescription?.type).toBe('answer')

    pc.connectionState = 'connected'
    pc.onconnectionstatechange?.()
    await flushPromises()

    const statsButton = buttonByTitle(wrapper, '显示播放统计')
    await statsButton?.trigger('click')

    for (let tick = 1; tick <= 3; tick++) {
      pc.videoBytes = tick * 125_000
      await vi.advanceTimersByTimeAsync(1000)
      await flushPromises()
    }

    const tabs = wrapper.findAll('.chart-tab').map((node) => node.text())
    expect(tabs).toEqual(['下载速率', '帧率', '缓冲余量', 'RTT', '丢包率'])
    expect(wrapper.find('.chart-value').text()).toBe('1.00 Mbps')
    expect(wrapper.find('.chart-line').exists()).toBe(true)

    const grid = wrapper.find('.stats-grid').text()
    expect(grid).toContain('H.264')
    expect(grid).toContain('1280×720')
    expect(grid).toContain('21 ms')
  })

  it('HLS 通道：按后端给出的地址加载播放列表', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    expect(hlsLog.loadSource).toEqual(['/hls/live/alex/index.m3u8'])
    expect(hlsLog.attachMedia).toBe(1)
    expect(wrapper.findAll('.mode-btn').map((node) => node.text())).toEqual(['LL-HLS', 'HLS', 'WebRTC'])
    expect(wrapper.findAll('.mode-btn')[0].classes()).toContain('active')
  })

  it('右侧控件按刷新、设置、统计、音量、小窗、网页全屏、全屏排列', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    const titles = wrapper.findAll('.controls .ctrl').map((node) => node.attributes('title'))
    expect(titles).toEqual([
      '刷新播放',
      '播放参数',
      '显示播放统计',
      '取消静音',
      '小窗（画中画）',
      '网页全屏',
      '全屏',
    ])
  })

  it('音量条可以调音量，静音按钮能恢复原音量', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    const video = wrapper.find('video').element as HTMLVideoElement
    const slider = wrapper.find('.volume-slider')
    expect(video.muted).toBe(true)
    expect((slider.element as HTMLInputElement).value).toBe('0')

    await slider.setValue('0.35')
    await flushPromises()

    expect(video.muted).toBe(false)
    expect(video.volume).toBeCloseTo(0.35)
    expect(slider.attributes('title')).toBe('音量 35%')

    await buttonByTitle(wrapper, '静音')?.trigger('click')
    await flushPromises()
    expect(video.muted).toBe(true)
    expect((slider.element as HTMLInputElement).value).toBe('0')

    await buttonByTitle(wrapper, '取消静音')?.trigger('click')
    await flushPromises()
    expect(video.muted).toBe(false)
    expect(video.volume).toBeCloseTo(0.35)
  })

  it('刷新按钮会重建播放器并重新拉取播放地址', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    expect(fetchPlayMock).toHaveBeenCalledTimes(1)
    expect(hlsLog.loadSource).toHaveLength(1)

    const refresh = buttonByTitle(wrapper, '刷新播放')
    expect(refresh).toBeTruthy()
    await refresh?.trigger('click')
    await flushPromises()

    expect(fetchPlayMock).toHaveBeenCalledTimes(2)
    expect(hlsLog.destroyed).toBe(1)
    expect(hlsLog.instances).toHaveLength(2)
    expect(hlsLog.loadSource).toHaveLength(2)
  })

  it('HLS 通道用分片数据算码率，不展示 RTT 与丢包', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    // 原生 controls 会和自绘控制条叠在一起，模板里不能再出现这个属性
    expect(wrapper.find('video').attributes('controls')).toBeUndefined()

    hlsLog.emit('hlsManifestParsed')
    await flushPromises()

    hlsLog.emit('hlsFragLoaded', {
      frag: { type: 'main', duration: 1, stats: { total: 500_000, loading: { start: 0, end: 250 } } },
    })
    hlsLog.emit('hlsFragLoaded', {
      frag: { type: 'audio', duration: 1, stats: { total: 12_000, loading: { start: 0, end: 100 } } },
    })
    hlsLog.emit('hlsLevelUpdated', { details: { fragments: [{ end: 10 }] } })

    await vi.advanceTimersByTimeAsync(1100)
    await flushPromises()

    await buttonByTitle(wrapper, '显示播放统计')?.trigger('click')
    await flushPromises()

    const tabs = wrapper.findAll('.chart-tab').map((node) => node.text())
    expect(tabs).toEqual(['下载速率', '帧率', '缓冲余量', '直播延迟'])

    const grid = wrapper.find('.stats-grid').text()
    expect(grid).toContain('4.00 Mbps')
    expect(grid).toContain('96 kbps')
    expect(grid).toContain('500 KB')
    expect(grid).not.toContain('往返时延')
    expect(grid).not.toContain('抖动')
    expect(wrapper.find('.stats-note').text()).toContain('RTT')
  })

  it('播放中控制条静止 2.5 秒自动收起，动一下鼠标再出来', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    // 只有真的解出视频帧才算进入播放中；happy-dom 没有这个方法，手动补一个。
    const quality = { totalVideoFrames: 30, droppedVideoFrames: 0 }
    Object.defineProperty(HTMLMediaElement.prototype, 'getVideoPlaybackQuality', {
      configurable: true,
      value: () => quality,
    })

    const wrapper = mountPlayer()
    await flushPromises()

    hlsLog.emit('hlsManifestParsed')
    await flushPromises()

    // 第一次采样读到 30 帧，播放器才会从「正在连接」进入播放中。
    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()

    await wrapper.find('.player').trigger('mousemove')
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()
    // v-show 会把 display 写成 none（happy-dom 下 isVisible() 不可靠，直接看 style）
    expect(wrapper.find('.controls').attributes('style') ?? '').toContain('display: none')

    await wrapper.find('.player').trigger('mousemove')
    await flushPromises()
    expect(wrapper.find('.controls').attributes('style') ?? '').not.toContain('display: none')

    delete (HTMLMediaElement.prototype as { getVideoPlaybackQuality?: unknown }).getVideoPlaybackQuality
  })

  it('三个通道都可用时可以切到 WebRTC', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls', true))
    const wrapper = mountPlayer()
    await flushPromises()

    const webrtc = wrapper.findAll('.mode-btn').find((node) => node.text() === 'WebRTC')
    await webrtc?.trigger('click')
    await flushPromises()

    expect(fetchPlayMock).toHaveBeenCalledTimes(2)
    expect(wrapper.findAll('.mode-btn')[2].classes()).toContain('active')
    expect(localStorage.getItem('tinycast.playMode')).toBe('webrtc')
  })

  it('HLS 落后直播边缘时只显示直播延迟，不再给追平按钮', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    const instance = hlsLog.instances[0] as { latency: number; liveSyncPosition: number }
    instance.latency = 3.4

    await vi.advanceTimersByTimeAsync(1100)
    await flushPromises()

    // 追平入口已移除，落后时靠自动追平或控制栏的刷新按钮。
    expect(wrapper.find('.latency-chip').exists()).toBe(false)

    await buttonByTitle(wrapper, '显示播放统计')?.trigger('click')
    await flushPromises()

    const stats = wrapper.find('.stats')
    expect(stats.text()).toContain('直播延迟')
    expect(stats.text()).toContain('3.4')
    expect(stats.find('.latency-chip').exists()).toBe(false)
  })

  it('WebRTC 连上却收不到画面时自动切回 LL-HLS', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls', true))
    const wrapper = mountPlayer()
    await flushPromises()

    const webrtc = wrapper.findAll('.mode-btn').find((node) => node.text() === 'WebRTC')
    await webrtc?.trigger('click')
    await flushPromises()

    for (let tick = 0; tick < 7; tick++) {
      await vi.advanceTimersByTimeAsync(1000)
      await flushPromises()
    }

    expect(wrapper.findAll('.mode-btn')[0].classes()).toContain('active')
    expect(wrapper.text()).toContain('已自动切回 LL-HLS')
    expect(wrapper.text()).toContain('B 帧')
    expect(hlsLog.loadSource.length).toBeGreaterThan(1)
  })

  it('统计面板区分播放方式与 LL-HLS / 普通 HLS', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    hlsLog.emit('hlsManifestParsed')
    hlsLog.emit('hlsLevelUpdated', { details: { fragments: [{ end: 10 }], partTarget: 0.2 } })
    await vi.advanceTimersByTimeAsync(1100)
    await flushPromises()

    await buttonByTitle(wrapper, '显示播放统计')?.trigger('click')
    await flushPromises()

    const grid = wrapper.find('.stats-grid').text()
    expect(grid).toContain('播放通道')
    expect(grid).toContain('播放方式')
    expect(grid).toContain('HLS.js')
    expect(grid).toContain('分发协议')
    expect(grid).toContain('LL-HLS')
  })

  it('播放参数卡片里可以切换播放方式并记住选择', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    await buttonByTitle(wrapper, '播放参数')?.trigger('click')
    await flushPromises()

    const engines = wrapper.findAll('.engine-btn')
    expect(engines.map((node) => node.text())).toEqual(['自动', 'HLS.js', '原生'])
    expect(engines[0].classes()).toContain('active')

    await engines[1].trigger('click')
    await flushPromises()

    expect(localStorage.getItem('tinycast.hlsEngine')).toBe('js')
    expect(fetchPlayMock).toHaveBeenCalledTimes(2)
    expect(wrapper.findAll('.engine-btn')[1].classes()).toContain('active')
  })

  it('HLS.js 解不了编码时自动换成原生播放器', async () => {
    vi.stubGlobal('navigator', { userAgent: 'Mozilla/5.0 (Macintosh) Version/17.4 Safari/605.1.15' })
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    await buttonByTitle(wrapper, '显示播放统计')?.trigger('click')
    await flushPromises()

    // 决策发生在挂载时（此时 canPlayType 还不可用），这里补上原生能力再触发解码错误。
    const video = wrapper.find('video').element as HTMLVideoElement
    video.canPlayType = (() => 'maybe') as HTMLVideoElement['canPlayType']

    hlsLog.emit('hlsError', { fatal: true, type: 'mediaError', details: 'bufferAddCodecError' })
    await vi.advanceTimersByTimeAsync(400)
    await flushPromises()
    // 自动降级只对本次会话生效，不能把用户的偏好永久钉在原生引擎上。
    expect(localStorage.getItem('tinycast.hlsEngine')).toBeNull()
    expect(wrapper.find('.engine-notice').text()).toContain('原生播放器')
  })

  it('记住上次选择的通道', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls', true))
    const wrapper = mountPlayer()
    await flushPromises()

    const webrtc = wrapper.findAll('.mode-btn').find((node) => node.text() === 'WebRTC')
    await webrtc?.trigger('click')
    await flushPromises()

    expect(localStorage.getItem('tinycast.playMode')).toBe('webrtc')

    fetchPlayMock.mockResolvedValue(playResponse('webrtc'))
    const second = mountPlayer()
    await flushPromises()

    expect(second.findAll('.mode-btn')[2].classes()).toContain('active')
  })

  it('网页全屏会锁住页面滚动，按 Esc 恢复', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    await buttonByTitle(wrapper, '网页全屏')?.trigger('click')
    expect(wrapper.find('.player').classes()).toContain('is-theater')
    expect(document.body.style.overflow).toBe('hidden')

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushPromises()

    expect(wrapper.find('.player').classes()).not.toContain('is-theater')
    expect(document.body.style.overflow).toBe('')
  })

  it('全屏按钮调用浏览器全屏接口', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    const stage = wrapper.find('.player').element as HTMLElement
    stage.requestFullscreen = vi.fn().mockResolvedValue(undefined)

    await buttonByTitle(wrapper, '全屏')?.trigger('click')
    await flushPromises()

    expect(stage.requestFullscreen).toHaveBeenCalledTimes(1)
  })

  it('长时间收不到视频帧时给出排查提示', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('webrtc'))
    const wrapper = mountPlayer()
    await flushPromises()

    await vi.advanceTimersByTimeAsync(10_000)
    await flushPromises()

    expect(wrapper.find('.overlay').text()).toContain('UDP 8000')
  })

  it('默认走 LL-HLS 的稳定档，按 3 秒目标起播', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    const instance = hlsLog.instances[0] as { config: Record<string, unknown> }
    expect(instance.config.lowLatencyMode).toBe(true)
    expect(instance.config.liveSyncDuration).toBe(3)
    expect(instance.config.liveMaxLatencyDuration).toBe(12)
    expect(instance.config.maxBufferLength).toBe(6)
    // 两个都给会被 hls.js 判成非法配置
    expect(instance.config.liveSyncDurationCount).toBeUndefined()

    await buttonByTitle(wrapper, '播放参数')?.trigger('click')
    await flushPromises()

    const tiers = wrapper.findAll('.tier-btn')
    expect(tiers.map((node) => node.text())).toEqual(['低延时', '稳定'])
    expect(tiers[1].classes()).toContain('active')
  })

  it('在播放参数卡片里选「低延时」会按 1.2 秒目标重建，并记住选择', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    await buttonByTitle(wrapper, '播放参数')?.trigger('click')
    await flushPromises()

    const low = wrapper.findAll('.tier-btn').find((node) => node.text() === '低延时')
    await low?.trigger('click')
    await flushPromises()

    expect(localStorage.getItem('tinycast.playTier')).toBe('low')
    expect(fetchPlayMock).toHaveBeenCalledTimes(2)

    const instance = hlsLog.instances[hlsLog.instances.length - 1] as { config: Record<string, unknown> }
    expect(instance.config.liveSyncDuration).toBe(1.2)
    expect(instance.config.liveMaxLatencyDuration).toBe(6)
    expect(instance.config.maxBufferLength).toBe(4)

    await buttonByTitle(wrapper, '显示播放统计')?.trigger('click')
    await flushPromises()
    expect(wrapper.find('.stats-grid').text()).toContain('低延时（1.2 秒）')
  })

  it('切到 HLS 通道后改用整段分片，延迟档位置灰', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    const hlsButton = wrapper.findAll('.mode-btn').find((node) => node.text() === 'HLS')
    await hlsButton?.trigger('click')
    await flushPromises()

    expect(localStorage.getItem('tinycast.llhls')).toBe('off')
    const instance = hlsLog.instances[hlsLog.instances.length - 1] as { config: Record<string, unknown> }
    expect(instance.config.lowLatencyMode).toBe(false)
    expect(instance.config.liveSyncDurationCount).toBe(3)
    expect(instance.config.liveSyncDuration).toBeUndefined()

    await buttonByTitle(wrapper, '播放参数')?.trigger('click')
    await flushPromises()
    for (const node of wrapper.findAll('.tier-btn')) {
      expect(node.attributes('disabled')).toBeDefined()
    }
    expect(wrapper.find('.settings').text()).toContain('档位不生效')

    await buttonByTitle(wrapper, '显示播放统计')?.trigger('click')
    await flushPromises()
    expect(wrapper.find('.stats-grid').text()).toContain('普通 HLS（档位不生效）')
  })

  it('后端没有配置播放代理时提示配置项', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('none'))
    const wrapper = mountPlayer()
    await flushPromises()

    expect(wrapper.find('.overlay').text()).toContain('MEDIAMTX_HLS_PROXY_TARGET')
    expect(wrapper.findAll('button').some((node) => node.text() === '重新连接')).toBe(true)
  })

  it('旧 WebView 没有 getVideoPlaybackQuality 时不算停帧，也不伪造帧率', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    expect('getVideoPlaybackQuality' in HTMLMediaElement.prototype).toBe(false)

    const wrapper = mountPlayer()
    await flushPromises()

    // 没有帧计数能力：只能拿 readyState / currentTime 判断“确实出画了”。
    const video = wrapper.find('video').element as HTMLVideoElement
    Object.defineProperty(video, 'readyState', { configurable: true, get: () => 4 })
    Object.defineProperty(video, 'currentTime', { configurable: true, get: () => 1.5, set: () => {} })

    hlsLog.emit('hlsManifestParsed')
    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()

    // 仍然能进入播放态（覆盖层收起），否则观众会一直盯着“正在连接”。
    expect(wrapper.find('.overlay').exists()).toBe(false)

    // 12 秒没有“新帧”（本来就是没有帧计数）也不能报停帧。
    await vi.advanceTimersByTimeAsync(12_000)
    await flushPromises()
    expect(wrapper.find('.stall-chip').exists()).toBe(false)

    await buttonByTitle(wrapper, '显示播放统计')?.trigger('click')
    await flushPromises()

    const grid = wrapper.find('.stats-grid').text()
    expect(grid).toContain('播放中')
    // 帧率与解码帧都不该伪造数字
    expect(grid).not.toContain('fps')
    expect(grid).toContain('解码帧—')
  })

  it('旧 WebView 一直没出画时仍给出超时提示，不会永远停在连接中', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    hlsLog.emit('hlsManifestParsed')
    // 没有帧计数，readyState / currentTime 也一直起不来：确实没画面
    await vi.advanceTimersByTimeAsync(10_000)
    await flushPromises()

    // 无帧计数只关掉「播放中掉帧」的误报，不能把「压根没起来」也一起吞掉
    expect(wrapper.find('.overlay').exists()).toBe(true)
    expect(wrapper.find('.overlay-title').text()).toContain('正在连接 HLS 播放')
    expect(wrapper.find('.overlay-note').text()).toContain('HLS 播放列表已拉取')
    expect(wrapper.find('.stall-chip').exists()).toBe(false)
  })

  it('关掉 LL-HLS 时，WebRTC 自动回退的文案写 HLS', async () => {
    localStorage.setItem('tinycast.llhls', 'off')
    fetchPlayMock.mockResolvedValue(playResponse('hls', true))
    const wrapper = mountPlayer()
    await flushPromises()

    const webrtc = wrapper.findAll('.mode-btn').find((node) => node.text() === 'WebRTC')
    await webrtc?.trigger('click')
    await flushPromises()

    for (let tick = 0; tick < 7; tick++) {
      await vi.advanceTimersByTimeAsync(1000)
      await flushPromises()
    }

    const chip = wrapper.find('.auto-chip')
    expect(chip.exists()).toBe(true)
    expect(chip.attributes('role')).toBe('status')
    expect(chip.text()).toContain('已自动切回 HLS')
    expect(chip.text()).not.toContain('LL-HLS')
    // 自动回退只改本次会话，用户的通道选择不被覆盖
    expect(localStorage.getItem('tinycast.playMode')).toBe('webrtc')
    expect(localStorage.getItem('tinycast.llhls')).toBe('off')
  })

  it('拖动音量条期间控制条不会中途收起', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const quality = { totalVideoFrames: 30, droppedVideoFrames: 0 }
    Object.defineProperty(HTMLMediaElement.prototype, 'getVideoPlaybackQuality', {
      configurable: true,
      value: () => quality,
    })

    const wrapper = mountPlayer()
    await flushPromises()

    hlsLog.emit('hlsManifestParsed')
    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()

    const controlsHidden = () =>
      (wrapper.find('.controls').attributes('style') ?? '').includes('display: none')
    const slider = wrapper.find('.volume-slider')
    expect(controlsHidden()).toBe(false)

    // 播放态下计时已经开始：先走 2 秒，再用一次 input 把计时重新推到 4.5 秒。
    await vi.advanceTimersByTimeAsync(2000)
    await slider.setValue('0.4')
    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()
    expect(controlsHidden()).toBe(false)

    // 手指按住滑块不动：没有 input 也不能收起控制条。
    await slider.trigger('pointerdown')
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(controlsHidden()).toBe(false)

    // 松手后重新按 2.5 秒计时
    await slider.trigger('pointerup')
    await vi.advanceTimersByTimeAsync(2600)
    await flushPromises()
    expect(controlsHidden()).toBe(true)
  })

  it('统计面板的 LL-HLS 延迟说明跟着档位走', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    hlsLog.emit('hlsManifestParsed')
    hlsLog.emit('hlsLevelUpdated', { details: { fragments: [{ end: 10 }], partTarget: 0.2 } })
    await vi.advanceTimersByTimeAsync(1100)
    await flushPromises()

    await buttonByTitle(wrapper, '显示播放统计')?.trigger('click')
    await flushPromises()
    expect(wrapper.find('.stats-note').text()).toContain('稳定档的目标延迟约 3 秒')

    await buttonByTitle(wrapper, '播放参数')?.trigger('click')
    await flushPromises()
    const low = wrapper.findAll('.tier-btn').find((node) => node.text() === '低延时')
    await low?.trigger('click')
    await flushPromises()

    // 重建后要重新收到 LL-HLS 清单事件，分发协议才会再标成 LL-HLS
    hlsLog.emit('hlsLevelUpdated', { details: { fragments: [{ end: 10 }], partTarget: 0.2 } })
    await flushPromises()
    expect(wrapper.find('.stats-note').text()).toContain('低延时档的目标延迟约 1.2 秒')
  })

  it('控制条、提示条与覆盖层补齐可访问性标注', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    const video = wrapper.find('video')
    expect(video.attributes('aria-label')).toBe('直播画面')
    expect(video.attributes('tabindex')).toBe('0')

    const labels = wrapper
      .findAll('.controls button[aria-label]')
      .map((node) => node.attributes('aria-label'))
    expect(labels).toEqual([
      '刷新播放',
      '播放参数',
      '显示播放统计',
      '取消静音',
      '小窗（画中画）',
      '网页全屏',
      '全屏',
    ])
    expect(wrapper.findAll('.mode-btn').map((node) => node.attributes('aria-pressed'))).toEqual([
      'true',
      'false',
      'false',
    ])

    // 连接中的覆盖层用 live region 播报，而不是把按钮也塞进去
    expect(wrapper.find('.overlay-text').attributes('role')).toBe('status')
    expect(wrapper.find('.overlay-text').attributes('aria-live')).toBe('polite')

    await buttonByTitle(wrapper, '播放参数')?.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('.tier-btn').map((node) => node.attributes('aria-pressed'))).toEqual([
      'false',
      'true',
    ])
    expect(wrapper.findAll('.engine-btn').map((node) => node.attributes('aria-pressed'))).toEqual([
      'true',
      'false',
      'false',
    ])
    expect(buttonByTitle(wrapper, '播放参数')?.attributes('aria-pressed')).toBe('true')
  })

  it('房间切到 GRACE 时停止播放', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    await wrapper.setProps({ room: { ...room, state: 'GRACE', graceUntil: Date.now() + 60_000 } })
    await flushPromises()

    expect(hlsLog.destroyed).toBe(1)
  })
})
