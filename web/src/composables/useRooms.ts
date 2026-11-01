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

function scheduleReconnect(): void {
  if (reconnectTimer !== null) return
  reconnectTimer = window.setTimeout(() => {
    reconnectTimer = null
    connect()
  }, 3000)
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
    // EventSource 自己会重连；只有彻底关闭时才需要我们接管重试
    if (es.readyState === EventSource.CLOSED) {
      closeSource()
      scheduleReconnect()
    }
  })
}

/** 先用普通请求拉一次，保证首屏立刻有数据；随后由 SSE 接管。 */
export async function refreshRooms(): Promise<void> {
  try {
    const data = await fetchRooms()
    state.rooms = data.rooms ?? []
    state.graceSeconds = data.graceSeconds || state.graceSeconds
    state.offset = data.serverTime - Date.now()
    state.loaded = true
    state.error = ''
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
}

export function findRoom(username: string): RoomView | undefined {
  return state.rooms.find((room) => room.username === username)
}

export function liveRoomCount(): number {
  return state.rooms.filter((room) => room.state === 'LIVE').length
}
