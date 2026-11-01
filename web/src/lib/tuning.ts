/**
 * 播放延迟档位。
 *
 * 延迟和缓冲说的是同一件事：直播延迟 = 直播边缘 − 播放位置，而缓冲余量永远 ≤ 直播延迟。
 * 想把缓冲从 1.2 秒加到 3 秒，播放位置就必须落后边缘 3 秒，所以「更稳」必然「更慢」。
 * 这里把两档的参数集中成一张表，播放器只按表下发，避免改一个漏一个。
 */
export type Tier = 'low' | 'stable'

export interface HlsTuning {
  lowLatencyMode: boolean
  liveSyncDuration?: number
  liveMaxLatencyDuration?: number
  liveSyncDurationCount?: number
  liveMaxLatencyDurationCount?: number
  maxBufferLength: number
  maxLiveSyncPlaybackRate: number
  backBufferLength: number
  /** 落后多少秒才主动跳回直播边缘；0 表示不再主动追 */
  catchUpAfter: number
}

/** 档位按钮，按「延迟从低到高」排列。 */
export const TIER_OPTIONS = [
  {
    value: 'low',
    label: '低延时',
    hint: '目标 1.2 秒：贴着直播边缘播，弱网下更容易卡。',
  },
  {
    value: 'stable',
    label: '稳定',
    hint: '目标 3 秒：缓冲更大，弱网下更稳，代价是多 1.8 秒延迟。',
  },
] as const

/** 低延时：贴着直播边缘播，允许 1.5 倍速追平。 */
const LOW_TUNING: HlsTuning = {
  lowLatencyMode: true,
  liveSyncDuration: 1.2,
  liveMaxLatencyDuration: 6,
  maxBufferLength: 4,
  maxLiveSyncPlaybackRate: 1.5,
  backBufferLength: 10,
  catchUpAfter: 1.8,
}

/** 稳定：目标 3 秒，缓冲给到 6 秒，不再主动追平（免得“追一下又卡”循环）。 */
const STABLE_TUNING: HlsTuning = {
  lowLatencyMode: true,
  liveSyncDuration: 3,
  liveMaxLatencyDuration: 12,
  maxBufferLength: 6,
  maxLiveSyncPlaybackRate: 1,
  backBufferLength: 10,
  catchUpAfter: 0,
}

/** 普通 HLS：不解析 LL-HLS 分片，按整段分片退 3 个缓冲，弱网最稳、延迟十几秒。 */
const PLAIN_TUNING: HlsTuning = {
  lowLatencyMode: false,
  liveSyncDurationCount: 3,
  liveMaxLatencyDurationCount: 6,
  maxBufferLength: 12,
  maxLiveSyncPlaybackRate: 1,
  backBufferLength: 30,
  catchUpAfter: 0,
}

/**
 * 当前该用的参数。
 *
 * 普通 HLS 通道下档位不生效（分片粒度太粗，1.2 秒和 3 秒没有区别）。
 */
export function hlsTuning(tier: Tier, llhls: boolean): HlsTuning {
  if (!llhls) return { ...PLAIN_TUNING }
  if (tier === 'stable') return { ...STABLE_TUNING }
  return { ...LOW_TUNING }
}

/** 面板上显示的档位名。 */
export function tierLabel(tier: Tier, llhls: boolean): string {
  if (!llhls) return '普通 HLS（档位不生效）'
  if (tier === 'stable') return '稳定（3 秒）'
  return '低延时（1.2 秒）'
}

/** 把参数热写到 hls.js 的 config 上：这些字段控制器每次采样都会重新读，不需要重建实例。 */
export function applyTuning(config: Record<string, unknown>, tuning: HlsTuning): void {
  config.lowLatencyMode = tuning.lowLatencyMode
  config.liveSyncDuration = tuning.liveSyncDuration
  config.liveMaxLatencyDuration = tuning.liveMaxLatencyDuration
  config.liveSyncDurationCount = tuning.liveSyncDurationCount
  config.liveMaxLatencyDurationCount = tuning.liveMaxLatencyDurationCount
  config.maxBufferLength = tuning.maxBufferLength
  config.maxLiveSyncPlaybackRate = tuning.maxLiveSyncPlaybackRate
  config.backBufferLength = tuning.backBufferLength
}