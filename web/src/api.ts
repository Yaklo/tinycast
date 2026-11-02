import type { AppConfig, PlayResponse, RoomResponse, RoomsResponse } from './types'

/** 后端返回的错误结构，与 httpapi.apiError 一致。 */
interface ApiErrorBody {
  code?: string
  message?: string
}

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  /** 服务端通过 Retry-After 建议的重试间隔（毫秒）；没有该响应头时为 undefined。 */
  readonly retryAfter?: number

  constructor(status: number, code: string, message: string, retryAfter?: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.retryAfter = retryAfter
  }
}

/**
 * 解析 Retry-After。后端目前发整数秒，同时兼容 HTTP-date 形式；
 * 上限 5 分钟，避免异常响应让前端长时间不再上报人数。
 */
function parseRetryAfter(value: string | null): number | undefined {
  const raw = value?.trim()
  if (!raw) return undefined
  const seconds = Number(raw)
  let ms: number
  if (Number.isFinite(seconds)) {
    // Retry-After: 0 表示可以立刻重试，不能被当成“没有响应头”再兜底 30 秒。
    if (seconds < 0) return undefined
    ms = Math.ceil(seconds) * 1000
  } else {
    const at = Date.parse(raw)
    if (!Number.isFinite(at)) return undefined
    // HTTP-date 已经过去等同于“现在重试”。
    ms = Math.max(0, at - Date.now())
  }
  if (!Number.isFinite(ms)) return undefined
  return Math.min(ms, 5 * 60_000)
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response
  try {
    res = await fetch(path, { headers: { Accept: 'application/json' }, ...init })
  } catch {
    throw new ApiError(0, 'network_error', '无法连接到服务器，请检查网络后重试')
  }

  if (!res.ok) {
    let body: ApiErrorBody = {}
    try {
      body = (await res.json()) as ApiErrorBody
    } catch {
      // 忽略非 JSON 响应体
    }
    const code = body.code ?? 'http_' + res.status
    throw new ApiError(
      res.status,
      code,
      body.message ?? '请求失败（' + res.status + '）',
      parseRetryAfter(res.headers.get('Retry-After')),
    )
  }

  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export function fetchRooms(): Promise<RoomsResponse> {
  return request<RoomsResponse>('/api/rooms')
}

export function fetchRoom(username: string): Promise<RoomResponse> {
  return request<RoomResponse>('/api/rooms/' + encodeURIComponent(username))
}

export function fetchPlay(username: string): Promise<PlayResponse> {
  return request<PlayResponse>('/api/rooms/' + encodeURIComponent(username) + '/play')
}

export function fetchConfig(): Promise<AppConfig> {
  return request<AppConfig>('/api/config')
}

/**
 * 观看心跳：既是"进入房间"，也是"我还在这"。
 *
 * 后端用 viewerId 去重，因此同一个标签页反复心跳只会算一个观众；
 * 关掉标签页后心跳停止，超时后自动从人数里移除。
 */
export function postWatch(username: string, viewerId: string): Promise<{ room: unknown }> {
  return request<{ room: unknown }>('/api/rooms/' + encodeURIComponent(username) + '/watch', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ viewerId }),
  })
}

/** 主动离开房间时调用，人数立刻减一，不用等心跳超时。 */
export function deleteWatch(username: string, viewerId: string): Promise<void> {
  const query = '?viewerId=' + encodeURIComponent(viewerId)
  // keepalive 让 pagehide / 关闭标签页时的请求不被浏览器直接丢弃。
  return request<void>('/api/rooms/' + encodeURIComponent(username) + '/watch' + query, {
    method: 'DELETE',
    keepalive: true,
  })
}
