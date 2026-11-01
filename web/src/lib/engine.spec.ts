import { describe, expect, it } from 'vitest'
import { ENGINE_OPTIONS, engineLabel, isSafariLike, prefLabel, resolveEngine } from './engine'
import type { EngineContext } from './engine'

const SAFARI_UA =
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Safari/605.1.15'
const IPHONE_UA =
  'Mozilla/5.0 (iPhone; CPU iPhone OS 17_4 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Mobile/15E148 Safari/604.1'
const CHROME_UA =
  'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36'
const EDGE_UA =
  'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36 Edg/126.0.0.0'

function context(patch: Partial<EngineContext> = {}): EngineContext {
  return {
    pref: 'auto',
    safariLike: true,
    canPlayNative: true,
    mseSupported: true,
    videoCodec: 'H264',
    ...patch,
  }
}

describe('isSafariLike', () => {
  it('认定 Safari 与 iOS Safari', () => {
    expect(isSafariLike(SAFARI_UA)).toBe(true)
    expect(isSafariLike(IPHONE_UA)).toBe(true)
  })

  it('排除 Chrome / Edge / Android：它们的 canPlayType 会误报', () => {
    expect(isSafariLike(CHROME_UA)).toBe(false)
    expect(isSafariLike(EDGE_UA)).toBe(false)
    expect(isSafariLike('Mozilla/5.0 (Linux; Android 14) Chrome/126.0 Mobile Safari/537.36')).toBe(false)
  })
})

describe('resolveEngine', () => {
  it('自动：Safari 走原生，Chromium 走 HLS.js', () => {
    expect(resolveEngine(context()).engine).toBe('native')
    expect(resolveEngine(context({ safariLike: false })).engine).toBe('js')
  })

  it('自动：Chromium 即使谎报 canPlayType 也不交给原生', () => {
    const decision = resolveEngine(context({ safariLike: false, canPlayNative: true }))
    expect(decision.engine).toBe('js')
    expect(decision.overridden).toBe(false)
  })

  it('自动：Safari 播 H.265 时提示依赖系统硬解', () => {
    const decision = resolveEngine(context({ videoCodec: 'H265' }))
    expect(decision.engine).toBe('native')
    expect(decision.notice).toContain('H.265')
  })

  it('自动：Safari 播 H.264 时不打扰用户', () => {
    expect(resolveEngine(context()).notice).toBe('')
  })

  it('用户选 HLS.js：有 MSE 就照办，H.265 时提醒 Safari 的 MSE 只解 H.264', () => {
    const h264 = resolveEngine(context({ pref: 'js' }))
    expect(h264.engine).toBe('js')
    expect(h264.overridden).toBe(false)
    expect(h264.notice).toBe('')

    const h265 = resolveEngine(context({ pref: 'js', videoCodec: 'HEVC' }))
    expect(h265.engine).toBe('js')
    expect(h265.notice).toContain('H.264')
  })

  it('用户选 HLS.js 但浏览器没有 MSE：退回原生并说明', () => {
    const decision = resolveEngine(context({ pref: 'js', mseSupported: false }))
    expect(decision.engine).toBe('native')
    expect(decision.overridden).toBe(true)
    expect(decision.notice).toContain('原生')
  })

  it('用户选 HLS.js 且既没有 MSE 也没有原生：提示改用低延迟通道', () => {
    const decision = resolveEngine(context({ pref: 'js', mseSupported: false, canPlayNative: false }))
    expect(decision.engine).toBe('js')
    expect(decision.overridden).toBe(true)
    expect(decision.notice).toContain('低延迟')
  })

  it('用户选原生：Safari 上照办', () => {
    const decision = resolveEngine(context({ pref: 'native' }))
    expect(decision.engine).toBe('native')
    expect(decision.overridden).toBe(false)
    expect(decision.notice).toBe('')
  })

  it('用户选原生：Chromium 上降级成 HLS.js 并说明', () => {
    const decision = resolveEngine(context({ pref: 'native', safariLike: false }))
    expect(decision.engine).toBe('js')
    expect(decision.overridden).toBe(true)
    expect(decision.notice).toContain('HLS.js')
  })

  it('用户选原生：Safari 但不支持 HLS 时降级成 HLS.js', () => {
    const decision = resolveEngine(context({ pref: 'native', canPlayNative: false }))
    expect(decision.engine).toBe('js')
    expect(decision.overridden).toBe(true)
  })
})

describe('文案', () => {
  it('引擎名', () => {
    expect(engineLabel('js')).toBe('HLS.js')
    expect(engineLabel('native')).toBe('原生播放器')
    expect(engineLabel('')).toBe('—')
  })

  it('三档偏好按从省心到可控排列', () => {
    expect(ENGINE_OPTIONS.map((option) => option.value)).toEqual(['auto', 'js', 'native'])
    expect(prefLabel('auto')).toBe('自动')
  })
})
