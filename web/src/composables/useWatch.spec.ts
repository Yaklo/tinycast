import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
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
