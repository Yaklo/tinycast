import { afterEach, describe, expect, it, vi } from 'vitest'
import { postWatch } from './api'

function errorResponse(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })
}

describe('ApiError Retry-After', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('把 Retry-After 的秒数换算成毫秒', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(errorResponse(429, {
      code: 'viewer_limit',
      message: '同一网络人数已达上限',
    }, { 'Retry-After': '7' })))

    await expect(postWatch('alex', 'viewer-1')).rejects.toMatchObject({
      status: 429,
      code: 'viewer_limit',
      retryAfter: 7000,
    })
  })

  it('没有 Retry-After 时不伪造重试时间', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(errorResponse(429, {
      code: 'room_full',
      message: '房间人数已达上限',
    })))

    await expect(postWatch('alex', 'viewer-1')).rejects.toMatchObject({
      status: 429,
      code: 'room_full',
      retryAfter: undefined,
    })
  })

  it('Retry-After: 0 表示立刻重试，不落进日期解析', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(errorResponse(429, {
      code: 'viewer_limit',
      message: '同一网络人数已达上限',
    }, { 'Retry-After': '0' })))

    await expect(postWatch('alex', 'viewer-1')).rejects.toMatchObject({
      status: 429,
      code: 'viewer_limit',
      retryAfter: 0,
    })
  })

  it('过去的 HTTP-date 按立刻重试处理，过大的值钳到 5 分钟', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(errorResponse(429, {
      code: 'viewer_limit',
      message: '同一网络人数已达上限',
    }, { 'Retry-After': new Date(Date.now() - 60_000).toUTCString() })))

    await expect(postWatch('alex', 'viewer-1')).rejects.toMatchObject({ retryAfter: 0 })

    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(errorResponse(429, {
      code: 'room_full',
      message: '房间人数已达上限',
    }, { 'Retry-After': '999999' })))

    await expect(postWatch('alex', 'viewer-1')).rejects.toMatchObject({ retryAfter: 300_000 })
  })
})
