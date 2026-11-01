import { describe, expect, it } from 'vitest'
import { applyTuning, hlsTuning, tierLabel, TIER_OPTIONS } from './tuning'

describe('hlsTuning', () => {
  it('低延时档按 1.2 秒起播', () => {
    const config = hlsTuning('low', true)
    expect(config.liveSyncDuration).toBe(1.2)
    expect(config.liveMaxLatencyDuration).toBe(6)
    expect(config.maxBufferLength).toBe(4)
    expect(config.catchUpAfter).toBe(1.8)
  })

  it('稳定档按 3 秒起播且不再主动追平', () => {
    const config = hlsTuning('stable', true)
    expect(config.liveSyncDuration).toBe(3)
    expect(config.liveMaxLatencyDuration).toBe(12)
    expect(config.maxBufferLength).toBe(6)
    expect(config.catchUpAfter).toBe(0)
  })

  it('关掉 LL-HLS 后两档是同一套普通 HLS 参数', () => {
    expect(hlsTuning('low', false)).toEqual(hlsTuning('stable', false))
    expect(hlsTuning('low', false).lowLatencyMode).toBe(false)
    expect(hlsTuning('low', false).liveSyncDurationCount).toBe(3)
  })

  it('任何组合都不同时给秒和分片数（hls.js 会直接抛错）', () => {
    for (const tier of ['low', 'stable'] as const) {
      for (const llhls of [true, false]) {
        const config = hlsTuning(tier, llhls)
        expect(config.liveSyncDuration !== undefined && config.liveSyncDurationCount !== undefined).toBe(false)
      }
    }
  })
})

describe('tierLabel', () => {
  it('按通道与档位给出人话', () => {
    expect(tierLabel('low', true)).toBe('低延时（1.2 秒）')
    expect(tierLabel('stable', true)).toBe('稳定（3 秒）')
    expect(tierLabel('low', false)).toBe('普通 HLS（档位不生效）')
    expect(tierLabel('stable', false)).toBe('普通 HLS（档位不生效）')
  })
})

describe('TIER_OPTIONS', () => {
  it('只有低延时与稳定两档，默认提示写在第一项', () => {
    expect(TIER_OPTIONS.map((item) => item.label)).toEqual(['低延时', '稳定'])
    expect(TIER_OPTIONS.map((item) => item.value)).toEqual(['low', 'stable'])
  })
})

describe('applyTuning', () => {
  it('热写分区参数时把不适用的字段清掉', () => {
    const config: Record<string, unknown> = { liveSyncDurationCount: 3 }
    applyTuning(config, hlsTuning('low', true))
    expect(config.liveSyncDuration).toBe(1.2)
    expect(config.liveSyncDurationCount).toBeUndefined()
    const plain: Record<string, unknown> = { liveSyncDuration: 1.2 }
    applyTuning(plain, hlsTuning('stable', false))
    expect(plain.liveSyncDuration).toBeUndefined()
    expect(plain.liveSyncDurationCount).toBe(3)
  })
})