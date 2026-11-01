/** 房间生命周期状态，与后端 hub.State 一一对应。 */
export type RoomState = 'LIVE' | 'GRACE'

/** 房间的公开视图：后端不会下发任何令牌信息（连哈希都不给）。 */
export interface RoomView {
  username: string
  room: string
  state: RoomState
  viewerCount: number
  /** 本轮开播时间（Unix 毫秒）。 */
  startedAt: number
  /** GRACE 状态的释放时间点（Unix 毫秒），LIVE 时为 0。 */
  graceUntil: number
}

/** SSE 推送的完整状态快照。 */
export interface Snapshot {
  seq: number
  serverTime: number
  rooms: RoomView[]
}

export interface RoomsResponse {
  rooms: RoomView[]
  serverTime: number
  graceSeconds: number
}

export interface RoomResponse {
  room: RoomView
  serverTime: number
  graceSeconds: number
}

/** 浏览器播放一路直播所需的信息，GRACE 状态下为 null。 */
export interface Playback {
  protocol: 'webrtc'
  whepUrl: string
  streamUrl: string
}

export interface PlayResponse {
  username: string
  state: RoomState
  graceUntil?: number
  playback: Playback | null
  serverTime: number
  graceSeconds: number
}

/** 后端公开配置，用于生成 OBS 推流地址。 */
export interface AppConfig {
  app: string
  rtmpServer: string
  webrtcBase: string
  graceSeconds: number
}
