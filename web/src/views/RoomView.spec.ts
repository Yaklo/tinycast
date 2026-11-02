import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import RoomView from './RoomView.vue'
import { ApiError } from '../api'
import { refreshRooms } from '../composables/useRooms'
import type { RoomView as RoomVM } from '../types'

const routeState = vi.hoisted(() => ({
  route: null as unknown as { params: { username: string } },
}))

// 用响应式的 route 才能模拟"同一页面里切到另一个房间"。
vi.mock('vue-router', async () => {
  const { reactive } = await import('vue')
  routeState.route = reactive({ params: { username: 'alex' } })
  return { useRoute: () => routeState.route }
})

const apiMocks = vi.hoisted(() => ({ fetchRoom: vi.fn(), fetchRooms: vi.fn() }))

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return { ...actual, fetchRoom: apiMocks.fetchRoom, fetchRooms: apiMocks.fetchRooms }
})

const watchMocks = vi.hoisted(() => ({
  start: vi.fn(),
  stop: vi.fn(),
  leave: vi.fn(),
}))

vi.mock('../composables/useWatch', async () => {
  const { ref } = await import('vue')
  return {
    useWatch: () => ({
      viewerId: 'viewer-test',
      active: ref(false),
      watchError: ref(null),
      start: watchMocks.start,
      stop: watchMocks.stop,
      leave: watchMocks.leave,
    }),
  }
})

function room(overrides: Partial<RoomVM> = {}): RoomVM {
  return {
    username: 'alex',
    room: '深夜写代码',
    state: 'LIVE',
    viewerCount: 3,
    startedAt: Date.now() - 60_000,
    graceUntil: 0,
    ...overrides,
  }
}

/** 模拟 SSE 推送一帧快照：真实链路里就是 useRooms 收到 rooms 事件。 */
async function pushRooms(rooms: RoomVM[]): Promise<void> {
  apiMocks.fetchRooms.mockResolvedValue({ rooms, serverTime: Date.now(), graceSeconds: 300 })
  await refreshRooms()
  await nextTick()
  await flushPromises()
}

let wrapper: VueWrapper | null = null

function mountRoom(): VueWrapper {
  wrapper = mount(RoomView, {
    global: {
      stubs: {
        RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' },
        LivePlayer: { props: ['room', 'media'], template: '<div class="player-stub" />' },
      },
    },
  })
  return wrapper
}

describe('RoomView', () => {
  beforeEach(async () => {
    routeState.route.params.username = 'alex'
    apiMocks.fetchRooms.mockResolvedValue({ rooms: [], serverTime: Date.now(), graceSeconds: 300 })
    apiMocks.fetchRoom.mockResolvedValue({ room: room(), serverTime: Date.now(), graceSeconds: 300 })
    await refreshRooms()
  })

  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
  })

  it('先 404、房间随后出现在快照里时不再停在"房间不存在"', async () => {
    apiMocks.fetchRoom.mockRejectedValue(new ApiError(404, 'room_not_found', '房间不存在'))

    const view = mountRoom()
    await flushPromises()
    expect(view.text()).toContain('房间不存在或已释放')
    expect(view.text()).not.toContain('深夜写代码')

    // 主播开播：SSE 快照里出现同名房间。
    await pushRooms([room({ state: 'LIVE' })])

    expect(view.text()).not.toContain('房间不存在或已释放')
    expect(view.text()).toContain('深夜写代码')
    expect(view.text()).toContain('LIVE 推流中')
    expect(watchMocks.start).toHaveBeenCalled()
  })

  it('先 404、房间以 GRACE 出现时同样显示房间（等待原身份重连）', async () => {
    apiMocks.fetchRoom.mockRejectedValue(new ApiError(404, 'room_not_found', '房间不存在'))

    const view = mountRoom()
    await flushPromises()
    expect(view.text()).toContain('房间不存在或已释放')

    await pushRooms([room({ state: 'GRACE', graceUntil: Date.now() + 120_000 })])

    expect(view.text()).not.toContain('房间不存在或已释放')
    expect(view.text()).toContain('主播已断开推流')
    // GRACE 不应该继续给已断流的房间计数。
    expect(watchMocks.stop).toHaveBeenCalled()
  })

  it('初次加载不会误停心跳', async () => {
    // 房间已在快照里：useWatch 自己会在 setup 时 start()，RoomView 不该插一脚。
    await pushRooms([room()])
    apiMocks.fetchRoom.mockResolvedValue({ room: room(), serverTime: Date.now(), graceSeconds: 300 })

    const view = mountRoom()
    await flushPromises()

    expect(view.text()).toContain('深夜写代码')
    expect(watchMocks.stop).not.toHaveBeenCalled()
    expect(watchMocks.start).not.toHaveBeenCalled()
    expect(watchMocks.leave).not.toHaveBeenCalled()
  })

  it('GRACE 时停止心跳，恢复 LIVE 时立刻补一拍', async () => {
    await pushRooms([room()])

    const view = mountRoom()
    await flushPromises()
    expect(view.text()).toContain('LIVE 推流中')
    expect(watchMocks.stop).not.toHaveBeenCalled()

    await pushRooms([room({ state: 'GRACE', graceUntil: Date.now() + 120_000 })])
    expect(view.text()).toContain('主播已断开推流')
    expect(watchMocks.stop).toHaveBeenCalledTimes(1)
    expect(watchMocks.start).not.toHaveBeenCalled()

    // 同名房间重新开播：立刻补一拍。
    await pushRooms([room({ state: 'LIVE', startedAt: Date.now() })])
    expect(view.text()).toContain('LIVE 推流中')
    expect(watchMocks.start).toHaveBeenCalledTimes(1)
    expect(watchMocks.stop).toHaveBeenCalledTimes(1)
  })

  it('切换到另一个正在直播的房间时，把心跳换成新房间且不误停', async () => {
    await pushRooms([room(), room({ username: 'bob', room: '白噪音' })])

    const view = mountRoom()
    await flushPromises()
    expect(view.text()).toContain('深夜写代码')
    expect(watchMocks.start).not.toHaveBeenCalled()

    routeState.route.params.username = 'bob'
    await flushPromises()

    expect(view.text()).toContain('白噪音')
    expect(watchMocks.leave).toHaveBeenCalledTimes(1)
    expect(watchMocks.start).toHaveBeenCalledTimes(1)
    // 两个房间都是 LIVE，state 没变化，不该误停。
    expect(watchMocks.stop).not.toHaveBeenCalled()
  })

  it('在两个 GRACE 房间之间切换时不会启动心跳', async () => {
    const grace = { state: 'GRACE' as const, graceUntil: Date.now() + 120_000 }
    await pushRooms([room(grace), room({ username: 'bob', room: '白噪音', ...grace })])

    const view = mountRoom()
    await flushPromises()
    expect(view.text()).toContain('主播已断开推流')
    expect(watchMocks.start).not.toHaveBeenCalled()

    routeState.route.params.username = 'bob'
    await flushPromises()

    expect(view.text()).toContain('白噪音')
    expect(watchMocks.leave).toHaveBeenCalledTimes(1)
    // 房间状态始终是 GRACE，状态 watcher 不会触发；切房也不能给断流房间开心跳。
    expect(watchMocks.start).not.toHaveBeenCalled()
  })

  it('同名房间重新开播（state 仍为 LIVE、startedAt 变化）时补一拍心跳', async () => {
    await pushRooms([room()])

    const view = mountRoom()
    await flushPromises()
    expect(view.text()).toContain('LIVE 推流中')
    watchMocks.start.mockClear()

    // 断流很短暂，快照里从未出现过 GRACE；只有 startedAt 变了。
    await pushRooms([room({ startedAt: Date.now() })])

    expect(watchMocks.start).toHaveBeenCalledTimes(1)
    expect(watchMocks.stop).not.toHaveBeenCalled()
  })

  it('房间从快照里消失（被释放）时停止心跳', async () => {
    await pushRooms([room()])

    const view = mountRoom()
    await flushPromises()

    await pushRooms([])
    expect(view.text()).toContain('房间已释放')
    expect(watchMocks.stop).toHaveBeenCalledTimes(1)
  })
})
