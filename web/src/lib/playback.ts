import type { MediaInfo } from '../types'

/**
 * 播放方式与编码能力提示。
 *
 * 背景：MediaMTX 只负责转发，不做转码。同一个推流走不同播放通道时的能力差别很大：
 * - WebRTC 通道：浏览器普遍只支持 H.264（SDP 里 offer 不到就能协商到别的编码），
 *   H.265 / AV1 基本放不出来；而且 MediaMTX 不会把 AAC 转成 Opus，声音也没有。
 * - HLS 通道：fMP4 分片，H.264 / H.265 / AV1 都能带，声音正常，代价是延迟约 1 秒。
 *
 * 所以页面默认走 HLS，把 WebRTC 作为"低延迟"可选项；这里负责在切换时把
 * "为什么这个编码在这条通道上放不出来"讲清楚，避免观众只看到一块黑屏。
 */

export type PlayMode = 'hls' | 'webrtc'

/** 是否是 H.265 / HEVC。 */
export function isH265(codec?: string): boolean {
  if (!codec) return false
  const value = codec.trim().toUpperCase()
  return value === 'HEVC' || value === 'H265' || value === 'H.265'
}

/** 是否是 AV1。 */
export function isAV1(codec?: string): boolean {
  if (!codec) return false
  return codec.trim().toUpperCase() === 'AV1'
}

/** 是否是不被支持的档次：4:4:4 / 4:2:2 色度采样与 10bit 位深浏览器解不了。 */
export function isUnsupportedProfile(profile?: string): boolean {
  if (!profile) return false
  const value = profile.toLowerCase()
  return value.includes('4:4:4') || value.includes('4:2:2') || value.includes('10bit')
}

/** 把后端的编码名转成界面上好读的写法。 */
export function codecLabel(codec?: string): string {
  if (!codec) return ''
  if (isH265(codec)) return 'H.265'
  if (isAV1(codec)) return 'AV1'
  const upper = codec.trim().toUpperCase()
  if (upper === 'H264' || upper === 'H.264' || upper === 'AVC') return 'H.264'
  if (upper === 'AAC' || upper === 'MPEG-4 AUDIO') return 'AAC'
  if (upper === 'OPUS') return 'Opus'
  if (upper === 'MP3') return 'MP3'
  return codec.trim()
}

export type MediaIssueLevel = 'warn' | 'error'

export interface MediaIssue {
  level: MediaIssueLevel
  title: string
  detail: string
  /** 建议切换到的播放方式；用户点一下就能得到能看的画面。 */
  suggestMode?: PlayMode
}

/**
 * 结合当前播放通道判断"能不能播"。
 *
 * 返回 null 表示没有发现问题；level=error 时播放器基本必定黑屏，
 * level=warn 表示能播但依赖浏览器能力或体验有折扣。
 */
export function mediaIssue(media?: MediaInfo | null, mode: PlayMode = 'hls'): MediaIssue | null {
  if (!media) return null

  if (isUnsupportedProfile(media.videoProfile)) {
    return {
      level: 'error',
      title: '推流用了 ' + String(media.videoProfile) + ' 档次，浏览器无法解码',
      detail:
        '请让主播把编码改成 H.264 的 Baseline / Main / High 档次，色度采样 4:2:0、位深 8bit；H.265 与 AV1 也要求 4:2:0 8bit。',
    }
  }

  if (isH265(media.videoCodec)) {
    if (mode === 'webrtc') {
      return {
        level: 'error',
        title: 'H.265 无法通过 WebRTC 播放',
        detail: 'WebRTC 通道基本只支持 H.264。点上方的「HLS」即可正常观看 H.265，或让主播改成 H.264 推流。',
        suggestMode: 'hls',
      }
    }
    return {
      level: 'warn',
      title: 'H.265 需要浏览器硬件解码支持',
      detail:
        'Windows / macOS 的 Chrome、Edge 与 Safari 可以直接播放；Firefox 或部分 Linux 环境可能黑屏，届时可让主播改用 H.264。',
    }
  }

  if (isAV1(media.videoCodec)) {
    if (mode === 'webrtc') {
      return {
        level: 'warn',
        title: 'AV1 建议改用 HLS 播放',
        detail: 'WebRTC 通道对 AV1 的支持取决于浏览器版本，切到 HLS 更稳。',
        suggestMode: 'hls',
      }
    }
    return {
      level: 'warn',
      title: 'AV1 依赖浏览器解码支持',
      detail: '较新的 Chrome、Edge、Safari 支持 AV1；老旧浏览器可能黑屏，届时可让主播改用 H.264。',
    }
  }

  return null
}

/** 播放器一直收不到画面时的提示：先区分编码问题，再按通道给网络/部署类原因。 */
export function noFrameHint(media?: MediaInfo | null, mode: PlayMode = 'hls'): string {
  const issue = mediaIssue(media, mode)
  if (issue) return issue.title + '。' + issue.detail

  if (mode === 'webrtc') {
    return (
      'WebRTC 已连上流媒体服务器，但一直收不到视频帧。按顺序排查：' +
      '① OBS 的 H.264 开了 B 帧——媒体服务会直接断开这种流（日志里是 "doesn\'t support H264 streams with B-frames"），' +
      '请把 OBS 输出设置里的「最大 B 帧数」改成 0 后重新推流；' +
      '② 服务器的 UDP 8000 被防火墙/安全组拦住（Windows 请以管理员身份运行 open-firewall.cmd）；' +
      '③ MediaMTX 的 webrtcAdditionalHosts 没配置成观众能访问的地址；' +
      '④ 编码不是 H.264。先切到「HLS」可以确认推流本身是否正常（HLS 支持 B 帧与 H.265）。'
    )
  }

  return (
    'HLS 播放列表已拉取，但一直没有画面。常见原因：' +
    '① 后端的 MEDIAMTX_HLS_PROXY_TARGET 指向错误，或 MediaMTX 的 HLS 端口没起来；' +
    '② 浏览器不支持当前编码（H.265 / AV1 依赖硬解，可换 Chrome / Edge 试试）；' +
    '③ 主播侧推流已经中断，刷新页面看房间状态是否变为"断流保留"。'
  )
}

/** 一行编码摘要，例如 "H.264 · 1280×720 · High"；信息不足时返回空串。 */
export function describeMedia(media?: MediaInfo | null): string {
  if (!media) return ''
  const parts: string[] = []
  const video = codecLabel(media.videoCodec)
  if (video) parts.push(video)
  if (media.width && media.height) parts.push(media.width + '×' + media.height)
  if (media.videoProfile) parts.push(media.videoProfile)
  return parts.join(' · ')
}
