import type { MediaInfo } from '../types'

/**
 * 判断一路推流"浏览器能不能播"。
 *
 * 背景：SRS 6 的 rtmp_to_rtc 不做转码。浏览器只 offer H.264，
 * 若主播用 H.265 推流，SDP 协商依然成功，但永远收不到视频 RTP，
 * 页面表现为"连上了、一直黑屏、没有任何报错"。后端会把 SRS 上报的
 * video.codec 带下来，这里据此给出可操作的中文提示。
 */

/** 是否是浏览器 WebRTC 无法解码的 H.265/HEVC。 */
export function isHEVC(codec?: string): boolean {
  if (!codec) return false
  const value = codec.trim().toUpperCase()
  return value === 'HEVC' || value === 'H265' || value === 'H.265'
}

/** 是否是不被支持的 H.264 档次：4:4:4 / 4:2:2 色度采样浏览器解不了。 */
export function isUnsupportedProfile(profile?: string): boolean {
  if (!profile) return false
  const value = profile.toLowerCase()
  return value.includes('4:4:4') || value.includes('4:2:2') || value.includes('10bit')
}

export type MediaIssueLevel = 'error' | 'warn'

export interface MediaIssue {
  level: MediaIssueLevel
  title: string
  detail: string
}

/** 编码信息能明确判断时返回提示，判断不了（无信息或正常 H.264）时返回 null。 */
export function mediaIssue(media?: MediaInfo | null): MediaIssue | null {
  if (!media) return null

  if (isHEVC(media.videoCodec)) {
    return {
      level: 'error',
      title: '主播正在用 H.265（HEVC）推流，绝大多数浏览器无法播放',
      detail:
        'WebRTC 播放只支持 H.264。请让主播在 OBS 的「设置 → 输出 → 串流 → 编码器」里改为 H.264（x264 或硬件 H.264）后重新推流。',
    }
  }

  if (isUnsupportedProfile(media.videoProfile)) {
    return {
      level: 'error',
      title: '推流用了 ' + String(media.videoProfile) + ' 档次，浏览器无法解码',
      detail:
        '请让主播把 H.264 的档次（profile）改为 Baseline / Main / High，色度采样 4:2:0、位深 8bit。',
    }
  }

  return null
}

/** 播放器一直收不到视频帧时的提示：先区分编码问题，再给网络/凭证类原因。 */
export function noFrameHint(media?: MediaInfo | null): string {
  const issue = mediaIssue(media)
  if (issue) return issue.title + '。' + issue.detail

  return (
    '已连上流媒体服务器，但一直收不到视频帧。常见原因：' +
    '① 服务器没放行 UDP 8000（Windows 请以管理员身份运行 open-firewall.cmd，云服务器要在安全组放行）；' +
    '② OBS 编码不是 H.264，或用了 4:4:4 / 10bit；' +
    '③ 跨机器观看时 SRS 的 rtc_server candidate 填成了 127.0.0.1。'
  )
}

/** 一行编码摘要，例如 "H264 · 1280×720 · High"；信息不足时返回空串。 */
export function describeMedia(media?: MediaInfo | null): string {
  if (!media) return ''
  const parts: string[] = []
  if (media.videoCodec) parts.push(media.videoCodec)
  if (media.width && media.height) parts.push(media.width + '×' + media.height)
  if (media.videoProfile) parts.push(media.videoProfile)
  return parts.join(' · ')
}
