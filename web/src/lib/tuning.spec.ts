import { describe, expect, it } from 'vitest'
import { applyTuning, hlsTuning, shouldUpgradeTier, tierLabel, TIER_OPTIONS } from './tuning'
import type { TierSample } from './tuning'

function samples(overrides: Partial<TierSample>): TierSample[] {
  return Array.from({ length: 10 }, () => ({
    bufferMs: 1800,
    downloadKbps: 6000,
    videoKbps: 3000,
    audioKbps: 128,
    ...overrides,
  }))
}

describe('hlsTuning', () => {
  it('自动与低延时都从 1.2 秒起播', () => {
    for (const tier of ['auto', 'low'] as const) {
      const tuning = hlsTuning(tier, true)
      expect(tuning.lowLatencyMode).toBe(true)
      expect(tuning.liveSyncDuration).toBe(1.2)
      expect(tuning.liveMaxLatencyDuration).toBe(6)
      expect(tuning.maxBufferLength).toBe(4)
      expect(tuning.catchUpAfter).toBeGreaterThan(0)
    }
  })

  it('稳定档锁 3 秒缓冲，且不再主动追平', () => {
    const tuning = hlsTuning('stable', true)
    expect(tuning.liveSyncDuration).toBe(3)
    expect(tuning.liveMaxLatencyDuration).toBe(12)
    expect(tuning.maxBufferLength).toBe(6)
    expect(tuning.catchUpAfter).toBe(0)
  })

  it('自动档被放宽后按稳定档参数走', () => {
    expect(hlsTuning('auto', true, true)).toEqual(hlsTuning('stable', true))
  })

  it('关掉 LL-HLS 后三档全部失效，改按整段分片缓冲', () => {
    for (const tier of ['auto', 'low', 'stable'] as const) {
      const tuning = hlsTuning(tier, false)
      expect(tuning.lowLatencyMode).toBe(false)
      expect(tuning.liveSyncDurationCount).toBe(3)
      expect(tuning.catchUpAfter).toBe(0)
    }
  })

  it('任何时候都不同时给 liveSyncDuration 与 liveSyncDurationCount（hls.js 会直接抛错）', () => {
    for (const llhls of [true, false]) {
      for (const tier of ['auto', 'low', 'stable'] as const) {
        const tuning = hlsTuning(tier, llhls)
        const usesSeconds = tuning.liveSyncDuration !== undefined
        const usesCount = tuning.liveSyncDurationCount !== undefined
        expect(usesSeconds !== usesCount).toBe(true)
        expect(tuning.liveMaxLatencyDuration !== undefined).toBe(usesSeconds)
      }
    }
  })
})

describe('tierLabel', () => {
  it('按档位与开关给出可读文案', () => {
    expect(tierLabel('auto', true)).toBe('自动（1.2 秒起）')
    expect(tierLabel('auto', true, true)).toBe('自动 · 已放宽到 3 秒')
    expect(tierLabel('low', true)).toBe('低延时（1.2 秒）')
    expect(tierLabel('stable', true)).toBe('稳定（3 秒）')
    expect(tierLabel('stable', false)).toBe('普通 HLS（档位不生效）')
  })

  it('三个档位的顺序固定为 自动 / 低延时 / 稳定', () => {
    expect(TIER_OPTIONS.map((option) => option.value)).toEqual(['auto', 'low', 'stable'])
    expect(TIER_OPTIONS.every((option) => option.hint.length > 0)).toBe(true)
  })
})

describe('applyTuning', () => {
  it('热写 config 且清掉不适用的字段', () => {
    const config: Record<string, unknown> = {
      liveSyncDuration: 1.2,
      liveMaxLatencyDuration: 6,
      maxBufferLength: 4,
    }
    applyTuning(config, hlsTuning('stable', false))
    expect(config.lowLatencyMode).toBe(false)
    expect(config.liveSyncDuration).toBeUndefined()
    expect(config.liveSyncDurationCount).toBe(3)
    expect(config.maxBufferLength).toBe(12)
  })
})

describe('shouldUpgradeTier', () => {
  it('样本太少不判断', () => {
    expect(shouldUpgradeTier(samples({ bufferMs: 100 }).slice(0, 5)).upgrade).toBe(false)
  })

  it('缓冲反复见底就放宽', () => {
    const result = shouldUpgradeTier(samples({ bufferMs: 200 }))
    expect(result.upgrade).toBe(true)
    expect(result.reason).toContain('缓冲')
  })

  it('下载速率追不上码率就放宽', () => {
    const result = shouldUpgradeTier(samples({ bufferMs: 2000, downloadKbps: 2000, videoKbps: 4000 }))
    expect(result.upgrade).toBe(true)
    expect(result.reason).toContain('下载速率')
  })

  it('网络健康时保持低延时', () => {
    expect(shouldUpgradeTier(samples({ bufferMs: 2200, downloadKbps: 9000, videoKbps: 3000 })).upgrade).toBe(false)
  })

  it('下载速率只是略低于码率时不放宽（这个算法本身偏低）', () => {
    expect(shouldUpgradeTier(samples({ bufferMs: 2000, downloadKbps: 1900, videoKbps: 2500 })).upgrade).toBe(false)
  })

  it('一分钟内被追平两次就放宽', () => {
    const result = shouldUpgradeTier(samples({ bufferMs: 2200, downloadKbps: 9000, videoKbps: 3000 }), 2)
    expect(result.upgrade).toBe(true)
    expect(result.reason).toContain('追平')
  })

  it('还没测到码率时只看缓冲', () => {
    const result = shouldUpgradeTier(samples({ bufferMs: 2400, downloadKbps: 0, videoKbps: 0, audioKbps: 0 }))
    expect(result.upgrade).toBe(false)
  })
})
