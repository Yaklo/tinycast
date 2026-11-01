import { describe, expect, it } from 'vitest'
import { describeMedia, isHEVC, isUnsupportedProfile, mediaIssue, noFrameHint } from './playback'

describe('isHEVC', () => {
  it('识别 SRS 上报的 HEVC 与常见写法', () => {
    expect(isHEVC('HEVC')).toBe(true)
    expect(isHEVC('hevc')).toBe(true)
    expect(isHEVC('H265')).toBe(true)
    expect(isHEVC('H.265')).toBe(true)
  })

  it('不把 H.264 当成 HEVC', () => {
    expect(isHEVC('H264')).toBe(false)
    expect(isHEVC(undefined)).toBe(false)
    expect(isHEVC('')).toBe(false)
  })
})

describe('isUnsupportedProfile', () => {
  it('4:4:4 / 4:2:2 / 10bit 视为不支持', () => {
    expect(isUnsupportedProfile('High 4:4:4 Predictive')).toBe(true)
    expect(isUnsupportedProfile('High 4:2:2')).toBe(true)
    expect(isUnsupportedProfile('10bit')).toBe(true)
  })

  it('常规档次视为支持', () => {
    expect(isUnsupportedProfile('High')).toBe(false)
    expect(isUnsupportedProfile('Baseline')).toBe(false)
    expect(isUnsupportedProfile(undefined)).toBe(false)
  })
})

describe('mediaIssue', () => {
  it('没有编码信息时不提示', () => {
    expect(mediaIssue(null)).toBeNull()
    expect(mediaIssue(undefined)).toBeNull()
    expect(mediaIssue({})).toBeNull()
  })

  it('H.265 给出错误级提示并指引改成 H.264', () => {
    const issue = mediaIssue({ videoCodec: 'HEVC', videoProfile: 'Main', width: 640, height: 360 })
    expect(issue?.level).toBe('error')
    expect(issue?.detail).toContain('H.264')
    expect(issue?.title).toContain('H.265')
  })

  it('4:4:4 档次给出错误级提示', () => {
    const issue = mediaIssue({ videoCodec: 'H264', videoProfile: 'High 4:4:4 Predictive' })
    expect(issue?.level).toBe('error')
    expect(issue?.title).toContain('4:4:4')
    expect(issue?.detail).toContain('4:2:0')
  })

  it('正常 H.264 High 不提示', () => {
    expect(mediaIssue({ videoCodec: 'H264', videoProfile: 'High', width: 1280, height: 720 })).toBeNull()
  })
})

describe('noFrameHint', () => {
  it('已知是 H.265 时复述编码原因，而不是让用户查防火墙', () => {
    const hint = noFrameHint({ videoCodec: 'HEVC' })
    expect(hint).toContain('H.265')
    expect(hint).not.toContain('UDP 8000')
  })

  it('编码未知时列出防火墙、编码、candidate 三类常见原因', () => {
    const hint = noFrameHint(null)
    expect(hint).toContain('UDP 8000')
    expect(hint).toContain('H.264')
    expect(hint).toContain('candidate')
  })
})

describe('describeMedia', () => {
  it('拼出编码、分辨率与档次', () => {
    expect(describeMedia({ videoCodec: 'H264', videoProfile: 'High', width: 1280, height: 720 })).toBe(
      'H264 · 1280×720 · High',
    )
  })

  it('信息不全时不出现多余分隔符', () => {
    expect(describeMedia({ videoCodec: 'H264' })).toBe('H264')
    expect(describeMedia(null)).toBe('')
    expect(describeMedia({})).toBe('')
  })
})
