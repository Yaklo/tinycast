import { describe, expect, it } from 'vitest'
import {
  codecLabel,
  describeMedia,
  isAV1,
  isH265,
  isUnsupportedProfile,
  mediaIssue,
  noFrameHint,
} from './playback'

describe('codecLabel', () => {
  it('把后端的编码名转成界面写法', () => {
    expect(codecLabel('H264')).toBe('H.264')
    expect(codecLabel('h264')).toBe('H.264')
    expect(codecLabel('H265')).toBe('H.265')
    expect(codecLabel('HEVC')).toBe('H.265')
    expect(codecLabel('AV1')).toBe('AV1')
    expect(codecLabel('MPEG-4 Audio')).toBe('AAC')
    expect(codecLabel('Opus')).toBe('Opus')
    expect(codecLabel(undefined)).toBe('')
  })
})

describe('编码判断', () => {
  it('识别 H.265 与 AV1', () => {
    expect(isH265('H265')).toBe(true)
    expect(isH265('hevc')).toBe(true)
    expect(isH265('H264')).toBe(false)
    expect(isAV1('av1')).toBe(true)
    expect(isAV1('H265')).toBe(false)
  })

  it('识别浏览器解不了的档次', () => {
    expect(isUnsupportedProfile('High 4:4:4 Predictive')).toBe(true)
    expect(isUnsupportedProfile('4:2:2')).toBe(true)
    expect(isUnsupportedProfile('Main 10bit')).toBe(true)
    expect(isUnsupportedProfile('High')).toBe(false)
    expect(isUnsupportedProfile(undefined)).toBe(false)
  })
})

describe('mediaIssue', () => {
  it('没有编码信息时不做判断', () => {
    expect(mediaIssue(null)).toBeNull()
    expect(mediaIssue({})).toBeNull()
  })

  it('H.265 在 WebRTC 下是硬伤，并建议切到 HLS', () => {
    const issue = mediaIssue({ videoCodec: 'H265' }, 'webrtc')
    expect(issue?.level).toBe('error')
    expect(issue?.suggestMode).toBe('hls')
    expect(issue?.title).toContain('H.265')
  })

  it('H.265 在 HLS 下只提示兼容性', () => {
    const issue = mediaIssue({ videoCodec: 'H265' }, 'hls')
    expect(issue?.level).toBe('warn')
    expect(issue?.detail).toContain('Firefox')
  })

  it('AV1 在 WebRTC 下建议切换，在 HLS 下仅提示', () => {
    expect(mediaIssue({ videoCodec: 'AV1' }, 'webrtc')?.suggestMode).toBe('hls')
    expect(mediaIssue({ videoCodec: 'AV1' }, 'hls')?.level).toBe('warn')
  })

  it('4:4:4 / 10bit 两种通道都放不出来', () => {
    for (const mode of ['hls', 'webrtc'] as const) {
      const issue = mediaIssue({ videoCodec: 'H264', videoProfile: 'High 4:4:4' }, mode)
      expect(issue?.level).toBe('error')
      expect(issue?.suggestMode).toBeUndefined()
    }
  })

  it('正常 H.264 不报问题', () => {
    expect(mediaIssue({ videoCodec: 'H264', videoProfile: 'High' }, 'hls')).toBeNull()
    expect(mediaIssue({ videoCodec: 'H264', videoProfile: 'High' }, 'webrtc')).toBeNull()
  })
})

describe('noFrameHint', () => {
  it('优先给出编码层面的解释', () => {
    expect(noFrameHint({ videoCodec: 'H265' }, 'webrtc')).toContain('H.265')
  })

  it('HLS 与 WebRTC 给不同的排查方向', () => {
    const hls = noFrameHint(null, 'hls')
    expect(hls).toContain('MEDIAMTX_HLS_PROXY_TARGET')
    const webrtc = noFrameHint(null, 'webrtc')
    expect(webrtc).toContain('B 帧')
    expect(webrtc).toContain('UDP 8000')
  })
})

describe('describeMedia', () => {
  it('拼出编码、分辨率与档次', () => {
    expect(describeMedia({ videoCodec: 'H264', width: 1920, height: 1080, videoProfile: 'High' })).toBe(
      'H.264 · 1920×1080 · High',
    )
    expect(describeMedia({ videoCodec: 'H265' })).toBe('H.265')
    expect(describeMedia(null)).toBe('')
  })
})
