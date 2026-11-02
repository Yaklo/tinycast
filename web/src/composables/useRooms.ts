import { reactive, readonly } from 'vue'
import { fetchConfig, fetchRooms } from '../api'
import type { AppConfig, RoomView, Snapshot } from '../types'

/**
 * 全局房间状态：唯一数据源是后端推送的完整快照。
 *
 * 每次收到快照就整体替换列表，前端不维护增量，因此丢帧、重连都不会让界面错乱。
 */
interface RoomsState {
  rooms: RoomView[]
  seq: number
  /** 服务器时钟与本地时钟的偏差，用于把 graceUntil 换算成倒计时。 */
  offset: number
  connected: boolean
  loaded: boolean
  error: string
  graceSeconds: number
  config: AppConfig | null
}

const state = reactive<RoomsState>({
  rooms: [],
  seq: 0,
  offset: 0,
  connected: false,
  loaded: false,
  error: '',
  graceSeconds: 300,
  config: null,
})

export const roomsState = readonly(state)

let source: EventSource | null = null
let reconnectTimer: number | null = null
let fallbackTimer: number | null = null
let reconnectAttempts = 0
let started = false

/** 以服务器时间为准的"现在"，用于倒计时，避免本机时钟漂移。 */
export function serverNow(): number {
  return Date.now() + state.offset
}

function applySnapshot(snap: Snapshot): void {
  state.rooms = Array.isArray(snap.rooms) ? snap.rooms : []
  state.seq = snap.seq ?? 0
  if (typeof snap.serverTime === 'number' && snap.serverTime > 0) {
    state.offset = snap.serverTime - Date.now()
  }
  state.loaded = true
}

/** SSE 断开时按 3s、6s、12s… 退避重连，避免服务端 503 时打满请求。 */
function scheduleReconnect(): void {
  if (reconnectTimer !== null) return
  const base = Math.min(30_000, 3000 * 2 ** reconnectAttempts)
  const delay = base + Math.floor(Math.random() * 500)
  reconnectAttempts += 1
  reconnectTimer = window.setTimeout(() => {
    reconnectTimer = null
    connect()
  }, delay)
}

/** EventSource 被 503 拦住时不会暴露状态码，用低频 REST 轮询兜底，保证界面还能更新。 */
function scheduleFallback(): void {
  if (fallbackTimer !== null) return
  fallbackTimer = window.setTimeout(async () => {
    fallbackTimer = null
    if (state.connected) return
    await refreshRooms()
    scheduleFallback()
  }, 15_000)
}

function stopFallback(): void {
  if (fallbackTimer !== null) {
    clearTimeout(fallbackTimer)
    fallbackTimer = null
  }
}

function closeSource(): void {
  if (source) {
    source.close()
    source = null
  }
}

function connect(): void {
  closeSource()
  let es: EventSource
  try {
    es = new EventSource('/api/events')
  } catch {
    scheduleReconnect()
    return
  }
  source = es

  es.addEventListener('open', () => {
    state.connected = true
    state.error = ''
    reconnectAttempts = 0
    stopFallback()
  })

  es.addEventListener('rooms', (event) => {
    const message = event as MessageEvent<string>
    try {
      applySnapshot(JSON.parse(message.data) as Snapshot)
      state.connected = true
      state.error = ''
    } catch {
      // 坏帧直接忽略，下一帧会修复状态
    }
  })

  es.addEventListener('error', () => {
    state.connected = false
    state.error = '实时连接中断，正在重试'
    // EventSource 自己会重连；彻底关闭时由我们退避接管，未关闭时用 REST 兜底。
    if (es.readyState === EventSource.CLOSED) {
      closeSource()
      scheduleReconnect()
    }
    scheduleFallback()
  })
}

/** 先用普通请求拉一次，保证首屏立刻有数据；随后由 SSE 接管。 */
export async function refreshRooms(): Promise<void> {
  try {
    const data = await fetchRooms()
    state.rooms = data.rooms ?? []
    state.graceSeconds = data.graceSeconds || state.graceSeconds
    if (typeof data.serverTime === 'number' && data.serverTime > 0) {
      state.offset = data.serverTime - Date.now()
    }
    state.loaded = true
    // 不要在这里清掉 SSE 的错误提示：REST 兜底成功不代表实时连接已经恢复。
    if (state.connected) state.error = ''
  } catch (err) {
    state.error = err instanceof Error ? err.message : '获取房间列表失败'
    state.loaded = true
  }
}

async function loadConfig(): Promise<void> {
  try {
    state.config = await fetchConfig()
    if (state.config.graceSeconds > 0) state.graceSeconds = state.config.graceSeconds
  } catch {
    // 配置只用于展示推流地址，取不到就等下次
  }
}

/** 应用启动时调用一次（App.vue）。 */
export function startRooms(): void {
  if (started) return
  started = true
  void refreshRooms()
  void loadConfig()
  connect()
  scheduleFallback()
}

export function findRoom(username: string): RoomView | undefined {
  return state.rooms.find((room) => room.username === username)
}

export function liveRoomCount(): number {
  return state.rooms.filter((room) => room.state === 'LIVE').length
}
