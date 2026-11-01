/** 房间生命周期状态，与后端 hub.State 一一对应。 */
export type RoomState = 'LIVE' | 'GRACE'

/** 推流编码信息，由后端从 MediaMTX 的路径信息里读出。 */
export interface MediaInfo {
  videoCodec?: string
  videoProfile?: string
  videoLevel?: string
  width?: number
  height?: number
  audioCodec?: string
}

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
  /** 当前推流的编码参数，只在 LIVE 且已探明时才有。 */
  media?: MediaInfo | null
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

/**
 * 播放方式。
 *
 * - hls：默认。延迟约 1 秒，有声音，支持 H.264 / H.265 / AV1（取决于浏览器解码能力）。
 * - webrtc：最低延迟（约 0.3 秒）。MediaMTX 不做转码，AAC 音频在 WebRTC 里拿不到声音。
 * - none：后端两侧代理都没配置，说明部署不完整。
 */
export type PlayProtocol = 'hls' | 'webrtc' | 'none'

/** 浏览器播放一路直播所需的信息，GRACE 状态下为 null。 */
export interface Playback {
  protocol: PlayProtocol
  /** HLS 播放列表地址（m3u8），protocol 为 hls 时存在。 */
  hlsUrl?: string
  /** WHEP 信令地址，protocol 为 webrtc 时存在。 */
  whepUrl?: string
}

export interface PlayResponse {
  username: string
  state: RoomState
  graceUntil?: number
  playback?: Playback | null
  media?: MediaInfo | null
  serverTime: number
  graceSeconds: number
}

/** 后端公开配置，用于生成 OBS 推流地址。 */
export interface AppConfig {
  app: string
  rtmpServer: string
  graceSeconds: number
  /** 后端是否配置了 HLS 代理。 */
  hls: boolean
  /** 后端是否配置了 WebRTC（WHEP）代理。 */
  webrtc: boolean
}
