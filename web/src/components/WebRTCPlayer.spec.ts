import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import WebRTCPlayer from './WebRTCPlayer.vue'
import type { RoomView } from '../types'

// 播放器依赖真实 WebRTC 与网络，单测里全部换成可控的打桩实现：
// RTCPeerConnection 只负责回放一份固定的 stats 报告，fetch 只负责返回一段 SDP。
const { fetchPlayMock } = vi.hoisted(() => ({ fetchPlayMock: vi.fn() }))

vi.mock('../api', () => ({ fetchPlay: (username: string) => fetchPlayMock(username) }))

interface FakeEntry {
  [key: string]: unknown
}

class FakePeerConnection {
  static instances: FakePeerConnection[] = []

  ontrack: ((event: unknown) => void) | null = null
  onconnectionstatechange: (() => void) | null = null
  connectionState = 'new'
  iceConnectionState = 'new'
  iceGatheringState = 'complete'
  closed = false
  stats = new Map<string, FakeEntry>()

  constructor() {
    FakePeerConnection.instances.push(this)
  }

  addTransceiver(): void {}
  addEventListener(): void {}
  removeEventListener(): void {}

  async createOffer(): Promise<{ type: string; sdp: string }> {
    return { type: 'offer', sdp: 'v=0 offer' }
  }

  async setLocalDescription(): Promise<void> {}
  async setRemoteDescription(): Promise<void> {}

  close(): void {
    this.closed = true
  }

  async getStats(): Promise<Map<string, FakeEntry>> {
    return this.stats
  }
}

/** 一份"视频正常在播"的 stats 报告：累计字节数按采样次数递增。 */
function statsReport(videoBytes: number): Map<string, FakeEntry> {
  return new Map<string, FakeEntry>([
    ['C1', { id: 'C1', type: 'codec', mimeType: 'video/H264' }],
    ['C2', { id: 'C2', type: 'codec', mimeType: 'audio/opus' }],
    [
      'V1',
      {
        id: 'V1',
        type: 'inbound-rtp',
        kind: 'video',
        codecId: 'C1',
        bytesReceived: videoBytes,
        packetsReceived: 900,
        packetsLost: 100,
        framesDecoded: 30,
        frameWidth: 1280,
        frameHeight: 720,
        jitter: 0.004,
        jitterBufferDelay: 1.2,
        jitterBufferEmittedCount: 30,
      },
    ],
    [
      'A1',
      { id: 'A1', type: 'inbound-rtp', kind: 'audio', codecId: 'C2', bytesReceived: 0, packetsReceived: 100 },
    ],
    ['P1', { id: 'P1', type: 'candidate-pair', selected: true, state: 'succeeded', currentRoundTripTime: 0.021 }],
  ])
}

const room = {
  username: 'alex',
  room: '测试直播',
  state: 'LIVE',
  viewerCount: 0,
  startedAt: 0,
  graceUntil: 0,
} as RoomView

async function mountPlayer() {
  vi.useFakeTimers()
  const wrapper = mount(WebRTCPlayer, { props: { room } })
  await vi.advanceTimersByTimeAsync(0)
  return wrapper
}

/** 把连接推进到 connected，再让定时采样跑够 n 次。 */
async function connect(pc: FakePeerConnection, ticks = 2): Promise<void> {
  pc.connectionState = 'connected'
  pc.onconnectionstatechange?.()
  for (let i = 1; i <= ticks; i += 1) {
    pc.stats = statsReport(125000 * i)
    await vi.advanceTimersByTimeAsync(1000)
  }
}

function buttonByTitle(wrapper: ReturnType<typeof mount>, title: string) {
  return wrapper.findAll('button').find((item) => item.attributes('title') === title)
}

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  FakePeerConnection.instances = []
  fetchPlayMock.mockReset()
  document.body.style.overflow = ''
})

describe('WebRTCPlayer', () => {
  it('默认展示下载速率曲线，并带出编码与分辨率', async () => {
    fetchPlayMock.mockResolvedValue({ playback: { whepUrl: '/rtc/v1/whep/' }, media: null })
    vi.stubGlobal('RTCPeerConnection', FakePeerConnection)
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        status: 201,
        headers: new Headers({ 'content-type': 'application/sdp' }),
        text: async () => 'v=0 answer',
      })),
    )

    const wrapper = await mountPlayer()
    await connect(FakePeerConnection.instances[0])

    expect(fetchPlayMock).toHaveBeenCalledWith('alex')

    const statsButton = buttonByTitle(wrapper, '显示播放统计')
    expect(statsButton).toBeTruthy()
    await statsButton?.trigger('click')

    const tabs = wrapper.findAll('.chart-tab')
    expect(tabs.map((tab) => tab.text())).toEqual(['下载速率', '帧率', '缓冲延迟', 'RTT', '丢包率'])
    expect(wrapper.find('.chart-tab.active').text()).toBe('下载速率')
    // 两秒累计 250KB 视频 → 1000kbps，界面用 Mbps 展示大数
    expect(wrapper.find('.chart-value').text()).toBe('1 Mbps')
    expect(wrapper.find('.chart-line').exists()).toBe(true)

    const grid = wrapper.find('.stats-grid').text()
    expect(grid).toContain('H264')
    expect(grid).toContain('1280×720')
    expect(grid).toContain('21 ms')
    expect(wrapper.text()).toContain('1 Mbps')

    wrapper.unmount()
  })

  it('网页全屏会锁住页面滚动，Esc 退出后恢复', async () => {
    fetchPlayMock.mockResolvedValue({ playback: { whepUrl: '/rtc/v1/whep/' }, media: null })
    vi.stubGlobal('RTCPeerConnection', FakePeerConnection)
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        status: 201,
        headers: new Headers({ 'content-type': 'application/sdp' }),
        text: async () => 'v=0 answer',
      })),
    )

    const wrapper = await mountPlayer()
    await connect(FakePeerConnection.instances[0], 1)

    const theaterButton = buttonByTitle(wrapper, '网页全屏')
    await theaterButton?.trigger('click')
    expect(wrapper.classes()).toContain('is-theater')
    expect(document.body.style.overflow).toBe('hidden')

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await wrapper.vm.$nextTick()
    expect(wrapper.classes()).not.toContain('is-theater')
    expect(document.body.style.overflow).toBe('')

    wrapper.unmount()
  })

  it('全屏按钮调用浏览器原生全屏接口', async () => {
    fetchPlayMock.mockResolvedValue({ playback: { whepUrl: '/rtc/v1/whep/' }, media: null })
    vi.stubGlobal('RTCPeerConnection', FakePeerConnection)
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        status: 201,
        headers: new Headers({ 'content-type': 'application/sdp' }),
        text: async () => 'v=0 answer',
      })),
    )

    const wrapper = await mountPlayer()
    await connect(FakePeerConnection.instances[0], 1)

    const requestFullscreen = vi.fn(async () => {})
    ;(wrapper.element as HTMLElement).requestFullscreen = requestFullscreen as unknown as () => Promise<void>

    await buttonByTitle(wrapper, '全屏')?.trigger('click')
    expect(requestFullscreen).toHaveBeenCalledTimes(1)

    wrapper.unmount()
  })

  it('一直没有解码帧时给出可操作的排查提示', async () => {
    fetchPlayMock.mockResolvedValue({ playback: { whepUrl: '/rtc/v1/whep/' }, media: null })
    vi.stubGlobal('RTCPeerConnection', FakePeerConnection)
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({
        ok: true,
        status: 201,
        headers: new Headers({ 'content-type': 'application/sdp' }),
        text: async () => 'v=0 answer',
      })),
    )

    const wrapper = await mountPlayer()
    const pc = FakePeerConnection.instances[0]
    pc.connectionState = 'connected'
    pc.onconnectionstatechange?.()
    // 报告里始终没有视频轨：连接是通的，但一帧都没有
    pc.stats = new Map<string, FakeEntry>()
    await vi.advanceTimersByTimeAsync(7000)

    expect(wrapper.find('.overlay').exists()).toBe(true)
    expect(wrapper.text()).toContain('重新连接')

    wrapper.unmount()
  })
})
