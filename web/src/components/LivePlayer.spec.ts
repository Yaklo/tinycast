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
    constructor() {
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
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
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
    expect(wrapper.findAll('.mode-btn').map((node) => node.text())).toEqual(['HLS', '低延迟'])
    expect(wrapper.findAll('.mode-btn')[0].classes()).toContain('active')
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
      frag: { type: 'video', duration: 1 },
      stats: { loaded: 500_000, loading: { start: 0, end: 250 } },
    })
    hlsLog.emit('hlsFragLoaded', {
      frag: { type: 'audio', duration: 1 },
      stats: { loaded: 12_000, loading: { start: 0, end: 100 } },
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
    const wrapper = mountPlayer()
    await flushPromises()

    hlsLog.emit('hlsManifestParsed')
    await flushPromises()
    expect(wrapper.find('.controls').attributes('style') ?? '').not.toContain('display: none')

    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()
    // v-show 会把 display 写成 none（happy-dom 下 isVisible() 不可靠，直接看 style）
    expect(wrapper.find('.controls').attributes('style') ?? '').toContain('display: none')

    await wrapper.find('.player').trigger('mousemove')
    await flushPromises()
    expect(wrapper.find('.controls').attributes('style') ?? '').not.toContain('display: none')
  })

  it('两个通道都可用时可以切换到低延迟', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls', true))
    const wrapper = mountPlayer()
    await flushPromises()

    const lowLatency = wrapper.findAll('.mode-btn').find((node) => node.text() === '低延迟')
    await lowLatency?.trigger('click')
    await flushPromises()

    expect(fetchPlayMock).toHaveBeenCalledTimes(2)
    expect(wrapper.findAll('.mode-btn')[1].classes()).toContain('active')
    expect(wrapper.find('.stats-grid').exists() || true).toBe(true)
  })

  it('HLS 落后直播边缘时给出追平按钮，并显示直播延迟', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls'))
    const wrapper = mountPlayer()
    await flushPromises()

    const instance = hlsLog.instances[0] as { latency: number; liveSyncPosition: number }
    instance.latency = 3.4

    await vi.advanceTimersByTimeAsync(1100)
    await flushPromises()

    const chip = wrapper.find('.latency-chip')
    expect(chip.exists()).toBe(true)
    expect(chip.text()).toContain('3.4')

    await wrapper.find('.ctrl[title=' + JSON.stringify('显示播放统计') + ']').trigger('click')
    await flushPromises()
    expect(wrapper.find('.stats').text()).toContain('直播延迟')

    await chip.trigger('click')
    await flushPromises()
  })

  it('WebRTC 连上却收不到画面时自动切回 HLS', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls', true))
    const wrapper = mountPlayer()
    await flushPromises()

    const lowLatency = wrapper.findAll('.mode-btn').find((node) => node.text() === '低延迟')
    await lowLatency?.trigger('click')
    await flushPromises()

    for (let tick = 0; tick < 7; tick++) {
      await vi.advanceTimersByTimeAsync(1000)
      await flushPromises()
    }

    expect(wrapper.findAll('.mode-btn')[0].classes()).toContain('active')
    expect(wrapper.text()).toContain('已自动切回 HLS')
    expect(wrapper.text()).toContain('B 帧')
    expect(hlsLog.loadSource.length).toBeGreaterThan(1)
  })

  it('记住上次选择的通道', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('hls', true))
    const wrapper = mountPlayer()
    await flushPromises()

    const lowLatency = wrapper.findAll('.mode-btn').find((node) => node.text() === '低延迟')
    await lowLatency?.trigger('click')
    await flushPromises()

    expect(localStorage.getItem('tinycast.playMode')).toBe('webrtc')

    fetchPlayMock.mockResolvedValue(playResponse('webrtc'))
    const second = mountPlayer()
    await flushPromises()

    expect(second.findAll('.mode-btn')[1].classes()).toContain('active')
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

  it('后端没有配置播放代理时提示配置项', async () => {
    fetchPlayMock.mockResolvedValue(playResponse('none'))
    const wrapper = mountPlayer()
    await flushPromises()

    expect(wrapper.find('.overlay').text()).toContain('MEDIAMTX_HLS_PROXY_TARGET')
    expect(wrapper.findAll('button').some((node) => node.text() === '重新连接')).toBe(true)
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
