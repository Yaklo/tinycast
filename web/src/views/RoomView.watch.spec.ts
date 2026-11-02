import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { VueWrapper } from '@vue/test-utils'
import { toRaw } from 'vue'
import RoomView from './RoomView.vue'
import { refreshRooms, roomsState } from '../composables/useRooms'
import type { RoomView as RoomVM } from '../types'

/**
 * 这一份刻意不 mock useWatch：要验证的是"RoomView + 真实心跳"在初次加载时的协作，
 * 也就是房间一开始就是 GRACE 时到底会不会发出一次 postWatch。
 */

const routeState = vi.hoisted(() => ({
  route: null as unknown as { params: { username: string } },
}))

vi.mock('vue-router', async () => {
  const { reactive } = await import('vue')
  routeState.route = reactive({ params: { username: 'alex' } })
  return { useRoute: () => routeState.route }
})

const apiMocks = vi.hoisted(() => ({
  fetchRoom: vi.fn(),
  fetchRooms: vi.fn(),
  postWatch: vi.fn(),
  deleteWatch: vi.fn(),
}))

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    fetchRoom: apiMocks.fetchRoom,
    fetchRooms: apiMocks.fetchRooms,
    postWatch: apiMocks.postWatch,
    deleteWatch: apiMocks.deleteWatch,
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

describe('RoomView 初次加载时的心跳', () => {
  beforeEach(async () => {
    routeState.route.params.username = 'alex'
    apiMocks.fetchRoom.mockResolvedValue({ room: room(), serverTime: Date.now(), graceSeconds: 300 })
    apiMocks.postWatch.mockResolvedValue({ room: {} })
    apiMocks.deleteWatch.mockResolvedValue(undefined)
    await pushRooms([])
  })

  afterEach(() => {
    wrapper?.unmount()
    wrapper = null
  })

  it('一进来房间就是 GRACE：一次心跳都不发', async () => {
    await pushRooms([room({ state: 'GRACE', graceUntil: Date.now() + 120_000 })])

    const view = mountRoom()
    await flushPromises()

    expect(view.text()).toContain('主播已断开推流')
    // 断流的房间不该被算成一个观众，连 setup 里的那一拍也不行。
    expect(apiMocks.postWatch).not.toHaveBeenCalled()
  })

  it('一进来房间就是 LIVE：立刻上报一次', async () => {
    await pushRooms([room()])

    const view = mountRoom()
    await flushPromises()

    expect(view.text()).toContain('LIVE 推流中')
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(1)
    expect(apiMocks.postWatch).toHaveBeenCalledWith('alex', expect.any(String))
  })

  it('SSE 首帧还没到、靠 fetchRoom 兜底出 LIVE 房间时也要立刻上报', async () => {
    // 模拟刚打开页面：SSE 快照还没来，room 只能来自 fetchRoom 的兜底数据。
    // roomsState 对外是 readonly，这里直接改原始对象来还原"首帧未到"的状态。
    ;(toRaw(roomsState) as { loaded: boolean }).loaded = false

    const view = mountRoom()
    await flushPromises()

    expect(view.text()).toContain('LIVE 推流中')
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(1)
  })

  it('先 GRACE 后回到 LIVE：恢复时补一拍', async () => {
    await pushRooms([room({ state: 'GRACE', graceUntil: Date.now() + 120_000 })])

    const view = mountRoom()
    await flushPromises()
    expect(apiMocks.postWatch).not.toHaveBeenCalled()

    await pushRooms([room({ state: 'LIVE', startedAt: Date.now() })])
    await flushPromises()

    expect(view.text()).toContain('LIVE 推流中')
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(1)
  })
})
