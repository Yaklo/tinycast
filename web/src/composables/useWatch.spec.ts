import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, ref } from 'vue'
import { mount } from '@vue/test-utils'
import { ApiError } from '../api'

const apiMocks = vi.hoisted(() => ({
  postWatch: vi.fn(),
  deleteWatch: vi.fn(),
}))

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>()
  return {
    ...actual,
    postWatch: apiMocks.postWatch,
    deleteWatch: apiMocks.deleteWatch,
  }
})

import { useWatch } from './useWatch'

describe('useWatch', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    apiMocks.postWatch.mockReset()
    apiMocks.deleteWatch.mockReset()
    sessionStorage.clear()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('房间释放后降频重试，同名房间重开播能恢复计数', async () => {
    apiMocks.postWatch
      .mockResolvedValueOnce({ room: {} })
      .mockRejectedValue(new ApiError(404, 'room_not_found', '房间不存在'))
    const watch = useWatch(() => 'alex', { manual: true, intervalMs: 1000 })

    watch.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(watch.active.value).toBe(true)

    await vi.advanceTimersByTimeAsync(1000)
    expect(watch.active.value).toBe(true)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(2)

    // 404 之后改为 30 秒一次，不再按 1 秒循环。
    await vi.advanceTimersByTimeAsync(29_000)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(2)

    apiMocks.postWatch.mockResolvedValue({ room: {} })
    await vi.advanceTimersByTimeAsync(1000)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(3)
    expect(watch.active.value).toBe(true)

    await vi.advanceTimersByTimeAsync(1000)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(4)
  })

  it('连续三次 404 后停止心跳，不再无限重试', async () => {
    apiMocks.postWatch.mockRejectedValue(new ApiError(404, 'room_not_found', '房间不存在'))
    const watch = useWatch(() => 'alex', { manual: true, intervalMs: 1000 })

    watch.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(1)
    expect(watch.active.value).toBe(true)

    // 单次 404 只降频，不停止。
    await vi.advanceTimersByTimeAsync(30_000)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(2)
    expect(watch.active.value).toBe(true)

    // 第三次 404 说明房间确实没了，直接停掉。
    await vi.advanceTimersByTimeAsync(30_000)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(3)
    expect(watch.active.value).toBe(false)

    // 停止之后不再有任何重试，日志不会再被刷。
    await vi.advanceTimersByTimeAsync(300_000)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(3)
  })

  it('404 停止后 start() 立刻补一拍，恢复计数', async () => {
    apiMocks.postWatch.mockRejectedValue(new ApiError(404, 'room_not_found', '房间不存在'))
    const watch = useWatch(() => 'alex', { manual: true, intervalMs: 1000 })

    watch.start()
    await vi.advanceTimersByTimeAsync(0)
    await vi.advanceTimersByTimeAsync(30_000)
    await vi.advanceTimersByTimeAsync(30_000)
    expect(watch.active.value).toBe(false)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(3)

    // 同名房间重新开播：RoomView 看到 LIVE 后 start()，不用等 30 秒。
    apiMocks.postWatch.mockResolvedValue({ room: {} })
    watch.start()
    expect(watch.active.value).toBe(true)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(4)

    await vi.advanceTimersByTimeAsync(1000)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(5)
    expect(watch.active.value).toBe(true)
  })

  it('中间成功过一次就重置 404 计数', async () => {
    apiMocks.postWatch
      .mockRejectedValueOnce(new ApiError(404, 'room_not_found', '房间不存在'))
      .mockRejectedValueOnce(new ApiError(404, 'room_not_found', '房间不存在'))
      .mockResolvedValue({ room: {} })
    const watch = useWatch(() => 'alex', { manual: true, intervalMs: 1000 })

    watch.start()
    await vi.advanceTimersByTimeAsync(0)
    await vi.advanceTimersByTimeAsync(30_000)
    await vi.advanceTimersByTimeAsync(30_000)
    expect(watch.active.value).toBe(true)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(3)

    // 失败两次 → 成功 → 再失败两次，都不该触发停止。
    apiMocks.postWatch.mockRejectedValue(new ApiError(404, 'room_not_found', '房间不存在'))
    await vi.advanceTimersByTimeAsync(1000)
    await vi.advanceTimersByTimeAsync(30_000)
    expect(watch.active.value).toBe(true)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(5)

    // 第三次连续 404 才会停。
    await vi.advanceTimersByTimeAsync(30_000)
    expect(watch.active.value).toBe(false)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(6)
  })

  it('进入 GRACE 被 stop() 后不再心跳，回到 LIVE 时立刻补一拍', async () => {
    apiMocks.postWatch.mockResolvedValue({ room: {} })
    const watch = useWatch(() => 'alex', { manual: true, intervalMs: 1000 })

    watch.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(1)

    // 房间断流进入 GRACE：RoomView 调 stop()，之后不再有请求。
    watch.stop()
    await vi.advanceTimersByTimeAsync(5000)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(1)

    // 恢复 LIVE：start() 立即补一拍，而不是等一个间隔。
    watch.start()
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1000)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(3)
    expect(watch.active.value).toBe(true)
  })

  it('切房后旧房间迟到的 404 不会算到新房间头上', async () => {
    const rejecters: ((err: unknown) => void)[] = []
    apiMocks.postWatch.mockImplementation(
      () =>
        new Promise((_resolve, reject) => {
          rejecters.push(reject)
        }),
    )
    const user = ref('alex')
    const watch = useWatch(() => user.value, { manual: true, intervalMs: 1000 })

    // 连续切三个房间，三个心跳都还在途。
    watch.start()
    user.value = 'bob'
    watch.start()
    user.value = 'carol'
    watch.start()
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(3)

    const notFound = new ApiError(404, 'room_not_found', '房间不存在')
    rejecters[0](notFound)
    rejecters[1](notFound)
    await vi.advanceTimersByTimeAsync(0)

    // 前两个房间的 404 不能累加计数：当前房间还是 LIVE 就该继续。
    expect(watch.active.value).toBe(true)

    rejecters[2](notFound)
    await vi.advanceTimersByTimeAsync(0)
    // carol 自己的这一次 404 只降频，不该因为前面几次就直接停掉。
    expect(watch.active.value).toBe(true)

    await vi.advanceTimersByTimeAsync(30_000)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(4)
    expect(watch.active.value).toBe(true)
  })

  it('429 人数上限保持心跳，并按 Retry-After 重试', async () => {
    apiMocks.postWatch
      .mockRejectedValueOnce(new ApiError(429, 'viewer_limit', '同一网络人数已达上限', 5000))
      .mockResolvedValue({ room: {} })
    const watch = useWatch(() => 'alex', { manual: true, intervalMs: 1000 })

    watch.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(watch.active.value).toBe(true)
    expect(watch.watchError.value?.code).toBe('viewer_limit')
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(1)

    // Retry-After 是 5 秒，不能被 1 秒的正常心跳间隔绕过。
    await vi.advanceTimersByTimeAsync(4999)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(2)
    expect(watch.watchError.value).toBeNull()

    // 成功后恢复正常的 1 秒间隔。
    await vi.advanceTimersByTimeAsync(1000)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(3)
  })

  it('429 没有 Retry-After 时按 30 秒兜底重试，且不会按 404 停止', async () => {
    apiMocks.postWatch.mockRejectedValue(new ApiError(429, 'room_full', '房间人数已达上限'))
    const watch = useWatch(() => 'alex', { manual: true, intervalMs: 1000 })

    watch.start()
    await vi.advanceTimersByTimeAsync(0)
    await vi.advanceTimersByTimeAsync(30_000)
    await vi.advanceTimersByTimeAsync(30_000)
    // 连续三次 429 只说明人数仍满，不能像连续 404 那样停掉心跳。
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(3)
    expect(watch.active.value).toBe(true)
    expect(watch.watchError.value?.code).toBe('room_full')

    apiMocks.postWatch.mockResolvedValue({ room: {} })
    await vi.advanceTimersByTimeAsync(30_000)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(4)
    expect(watch.watchError.value).toBeNull()
    expect(watch.active.value).toBe(true)
  })

  it('同一房间 stop()/start() 后，旧心跳的 429 不污染新会话', async () => {
    const rejecters: ((err: unknown) => void)[] = []
    const resolvers: ((value: { room: unknown }) => void)[] = []
    apiMocks.postWatch
      .mockImplementationOnce(
        () =>
          new Promise((_resolve, reject) => {
            rejecters.push(reject)
          }),
      )
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolvers.push(resolve)
          }),
      )
    const watch = useWatch(() => 'alex', { manual: true, intervalMs: 1000 })

    // 第一次心跳还没返回，房间进入 GRACE，RoomView 紧接着 stop()/start()。
    watch.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(1)
    watch.stop()
    watch.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(2)

    // 新会话成功。
    resolvers[0]({ room: {} })
    await vi.advanceTimersByTimeAsync(0)
    expect(watch.watchError.value).toBeNull()

    // 旧会话迟到 429：不能把上限提示重新盖到新会话上，也不能压住心跳。
    rejecters[0](new ApiError(429, 'viewer_limit', '同一网络人数已达上限', 30_000))
    await vi.advanceTimersByTimeAsync(0)
    expect(watch.watchError.value).toBeNull()

    await vi.advanceTimersByTimeAsync(1000)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(3)
  })

  it('429 之后遇到普通错误会清掉人数上限提示和退避', async () => {
    apiMocks.postWatch
      .mockRejectedValueOnce(new ApiError(429, 'viewer_limit', '同一网络人数已达上限', 5000))
      .mockRejectedValueOnce(new ApiError(500, 'server_error', '服务器错误'))
      .mockResolvedValue({ room: {} })
    const watch = useWatch(() => 'alex', { manual: true, intervalMs: 1000 })

    watch.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(watch.watchError.value?.code).toBe('viewer_limit')

    await vi.advanceTimersByTimeAsync(5000)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(0)
    expect(watch.watchError.value).toBeNull()

    await vi.advanceTimersByTimeAsync(1000)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(3)
  })

  it('Retry-After: 0 按正常间隔重试，而不是兜底 30 秒', async () => {
    apiMocks.postWatch
      .mockRejectedValueOnce(new ApiError(429, 'viewer_limit', '同一网络人数已达上限', 0))
      .mockResolvedValue({ room: {} })
    const watch = useWatch(() => 'alex', { manual: true, intervalMs: 1000 })

    watch.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(watch.watchError.value?.code).toBe('viewer_limit')

    await vi.advanceTimersByTimeAsync(1000)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(2)
    expect(watch.watchError.value).toBeNull()
  })

  it('429 退避期间切换前后台不会从当前时刻重新计时', async () => {
    apiMocks.postWatch
      .mockRejectedValueOnce(new ApiError(429, 'viewer_limit', '同一网络人数已达上限', 5000))
      .mockResolvedValue({ room: {} })
    apiMocks.deleteWatch.mockResolvedValue(undefined)

    const originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
    let hidden = false
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden })
    const wrapper = mount(defineComponent({
      setup() {
        useWatch(() => 'alex', { intervalMs: 1000 })
        return () => null
      },
    }))

    try {
      await vi.advanceTimersByTimeAsync(0)
      expect(apiMocks.postWatch).toHaveBeenCalledTimes(1)

      // 第 4 秒切到后台：退避截止时间仍在第 5 秒，不能被重排到第 9 秒。
      hidden = true
      document.dispatchEvent(new Event('visibilitychange'))
      await vi.advanceTimersByTimeAsync(4000)
      hidden = false
      document.dispatchEvent(new Event('visibilitychange'))
      await vi.advanceTimersByTimeAsync(999)
      expect(apiMocks.postWatch).toHaveBeenCalledTimes(1)

      await vi.advanceTimersByTimeAsync(1)
      expect(apiMocks.postWatch).toHaveBeenCalledTimes(2)
    } finally {
      wrapper.unmount()
      if (originalHidden) {
        Object.defineProperty(document, 'hidden', originalHidden)
      } else {
        delete (document as { hidden?: boolean }).hidden
      }
    }
  })

  it('同一房间 stop()/start() 后，旧会话的 404 不会累加到新会话', async () => {
    const rejecters: ((err: unknown) => void)[] = []
    const resolvers: ((value: { room: unknown }) => void)[] = []
    apiMocks.postWatch.mockImplementation(
      () =>
        new Promise((resolve, reject) => {
          resolvers.push(resolve)
          rejecters.push(reject)
        }),
    )
    apiMocks.deleteWatch.mockResolvedValue(undefined)

    let watch: ReturnType<typeof useWatch> | undefined
    const wrapper = mount(defineComponent({
      setup() {
        watch = useWatch(() => 'alex', { intervalMs: 1000 })
        return () => null
      },
    }))

    try {
      await vi.advanceTimersByTimeAsync(0)
      expect(apiMocks.postWatch).toHaveBeenCalledTimes(1)

      // 同一会话里通过可见性补拍制造多个在途请求（A+B+C+D）。
      for (let i = 0; i < 3; i += 1) {
        document.dispatchEvent(new Event('visibilitychange'))
      }
      await vi.advanceTimersByTimeAsync(0)
      expect(apiMocks.postWatch).toHaveBeenCalledTimes(4)

      watch!.stop()
      watch!.start()
      await vi.advanceTimersByTimeAsync(0)
      expect(apiMocks.postWatch).toHaveBeenCalledTimes(5)

      // 旧会话的四个 404 全部迟到，不能把新会话的三振计数推到上限。
      const notFound = new ApiError(404, 'room_not_found', '房间不存在')
      for (let i = 0; i < 4; i += 1) {
        rejecters[i](notFound)
      }
      await vi.advanceTimersByTimeAsync(0)
      expect(watch!.active.value).toBe(true)

      resolvers[4]({ room: {} })
      await vi.advanceTimersByTimeAsync(0)
      expect(watch!.active.value).toBe(true)
      expect(watch!.watchError.value).toBeNull()

      await vi.advanceTimersByTimeAsync(1000)
      expect(apiMocks.postWatch).toHaveBeenCalledTimes(6)
    } finally {
      wrapper.unmount()
    }
  })

  it('429 退避遇到重叠的普通错误仍按原截止时间重试', async () => {
    const rejecters: ((err: unknown) => void)[] = []
    apiMocks.postWatch.mockImplementation(
      () =>
        new Promise((_resolve, reject) => {
          rejecters.push(reject)
        }),
    )
    apiMocks.deleteWatch.mockResolvedValue(undefined)

    const wrapper = mount(defineComponent({
      setup() {
        useWatch(() => 'alex', { intervalMs: 1000 })
        return () => null
      },
    }))

    try {
      await vi.advanceTimersByTimeAsync(0)
      expect(apiMocks.postWatch).toHaveBeenCalledTimes(1)

      // 可见性补拍出第二个在途请求，两个请求属于同一会话。
      document.dispatchEvent(new Event('visibilitychange'))
      await vi.advanceTimersByTimeAsync(0)
      expect(apiMocks.postWatch).toHaveBeenCalledTimes(2)

      rejecters[0](new ApiError(429, 'viewer_limit', '同一网络人数已达上限', 5000))
      await vi.advanceTimersByTimeAsync(0)
      rejecters[1](new ApiError(500, 'server_error', '服务器错误'))
      await vi.advanceTimersByTimeAsync(0)

      // 普通错误不能把仍在生效的 5 秒退避改成正常 1 秒间隔。
      await vi.advanceTimersByTimeAsync(4999)
      expect(apiMocks.postWatch).toHaveBeenCalledTimes(2)
      await vi.advanceTimersByTimeAsync(1)
      expect(apiMocks.postWatch).toHaveBeenCalledTimes(3)
    } finally {
      wrapper.unmount()
    }
  })

  it('普通网络错误不会中断心跳', async () => {
    apiMocks.postWatch
      .mockRejectedValueOnce(new ApiError(500, 'server_error', '服务器错误'))
      .mockResolvedValue({ room: {} })
    const watch = useWatch(() => 'alex', { manual: true, intervalMs: 1000 })

    watch.start()
    await vi.advanceTimersByTimeAsync(0)
    expect(watch.active.value).toBe(true)

    await vi.advanceTimersByTimeAsync(1000)
    expect(watch.active.value).toBe(true)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(2)

    await vi.advanceTimersByTimeAsync(1000)
    expect(watch.active.value).toBe(true)
    expect(apiMocks.postWatch).toHaveBeenCalledTimes(3)
  })
})
