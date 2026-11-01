import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Snapshot } from '../types'

/** 可手动喂事件的 EventSource 替身。 */
class FakeEventSource {
  static instances: FakeEventSource[] = []
  static CLOSED = 2

  readonly url: string
  readyState = 0
  closed = false
  private listeners = new Map<string, ((event: unknown) => void)[]>()

  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }

  addEventListener(type: string, handler: (event: unknown) => void): void {
    const list = this.listeners.get(type) ?? []
    list.push(handler)
    this.listeners.set(type, list)
  }

  close(): void {
    this.closed = true
    this.readyState = FakeEventSource.CLOSED
  }

  emit(type: string, event: unknown = {}): void {
    for (const handler of this.listeners.get(type) ?? []) handler(event)
  }
}

const snapshot: Snapshot = {
  seq: 7,
  serverTime: 1_700_000_000_000,
  rooms: [
    {
      username: 'alex',
      room: '深夜写代码',
      state: 'LIVE',
      viewerCount: 3,
      startedAt: 1_699_999_000_000,
      graceUntil: 0,
    },
  ],
}

/** 每个用例都重新加载模块，避免模块级单例状态互相污染。 */
async function loadModule() {
  vi.resetModules()
  const fetchMock = vi.fn(async (url: string) => {
    if (url === '/api/rooms') {
      return new Response(JSON.stringify({ ...snapshot, graceSeconds: 300 }), { status: 200 })
    }
    return new Response(JSON.stringify({ app: 'live', rtmpServer: 'rtmp://x/live', webrtcBase: 'webrtc://x/live', graceSeconds: 300 }), { status: 200 })
  })
  vi.stubGlobal('fetch', fetchMock)
  vi.stubGlobal('EventSource', FakeEventSource)
  return await import('./useRooms')
}

describe('useRooms', () => {
  beforeEach(() => {
    FakeEventSource.instances = []
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('首屏先用 REST 拉一次，随后由 SSE 快照接管', async () => {
    const mod = await loadModule()
    mod.startRooms()
    await vi.waitFor(() => expect(mod.roomsState.loaded).toBe(true))
    expect(mod.roomsState.rooms).toHaveLength(1)
    expect(mod.findRoom('alex')?.room).toBe('深夜写代码')

    const es = FakeEventSource.instances[0]
    expect(es.url).toBe('/api/events')
    es.emit('open')
    expect(mod.roomsState.connected).toBe(true)

    es.emit('rooms', {
      data: JSON.stringify({
        ...snapshot,
        seq: 8,
        rooms: [{ ...snapshot.rooms[0], state: 'GRACE', viewerCount: 0, graceUntil: 1_700_000_300_000 }],
      }),
    })
    expect(mod.roomsState.seq).toBe(8)
    expect(mod.findRoom('alex')?.state).toBe('GRACE')
    expect(mod.liveRoomCount()).toBe(0)
  })

  it('用服务器时间校准本地时钟偏移', async () => {
    const mod = await loadModule()
    const localNow = 1_600_000_000_000
    vi.spyOn(Date, 'now').mockReturnValue(localNow)

    mod.startRooms()
    await vi.waitFor(() => expect(mod.roomsState.loaded).toBe(true))
    expect(mod.serverNow()).toBe(snapshot.serverTime)
  })

  it('坏帧不会破坏已加载的状态', async () => {
    const mod = await loadModule()
    mod.startRooms()
    await vi.waitFor(() => expect(mod.roomsState.loaded).toBe(true))

    FakeEventSource.instances[0].emit('rooms', { data: '{not json' })
    expect(mod.roomsState.rooms).toHaveLength(1)
  })
})
