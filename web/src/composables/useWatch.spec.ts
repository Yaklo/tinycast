import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
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
