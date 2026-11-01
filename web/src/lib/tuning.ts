/**
 * 播放延迟档位。
 *
 * 延迟和缓冲说的是同一件事：直播延迟 = 直播边缘 − 播放位置，而缓冲余量永远 ≤ 直播延迟。
 * 想把缓冲从 1.2 秒加到 3 秒，播放位置就必须落后边缘 3 秒，所以「更稳」必然「更慢」。
 * 这里把每个档位的参数集中成一张表，播放器只按表下发，避免改一个漏一个。
 */
export type Tier = 'auto' | 'low' | 'stable'

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

export interface TierSample {
  bufferMs: number
  downloadKbps: number
  videoKbps: number
  audioKbps: number
}

export const TIER_OPTIONS = [
  {
    value: 'auto',
    label: '自动',
    hint: '先按 1.2 秒播；发现网络跟不上就自动放宽到 3 秒，不会自己降回来。',
  },
  {
    value: 'low',
    label: '低延时',
    hint: '锁定 1.2 秒目标：延迟最低，弱网下更容易卡。',
  },
  {
    value: 'stable',
    label: '稳定',
    hint: '锁定 3 秒目标：缓冲更大，弱网下更稳，代价是多 1.8 秒延迟。',
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
 * 关掉 LL-HLS 时前面的三档全部失效（普通 HLS 的分片粒度太粗，档位已经没有意义）。
 * 自动档被判定需要放宽时按「稳定」的参数走。
 */
export function hlsTuning(tier: Tier, llhls: boolean, upgraded = false): HlsTuning {
  if (!llhls) return { ...PLAIN_TUNING }
  if (tier === 'stable' || (tier === 'auto' && upgraded)) return { ...STABLE_TUNING }
  return { ...LOW_TUNING }
}

/** 面板上显示的档位名。 */
export function tierLabel(tier: Tier, llhls: boolean, upgraded = false): string {
  if (!llhls) return '普通 HLS（档位不生效）'
  if (tier === 'low') return '低延时（1.2 秒）'
  if (tier === 'stable') return '稳定（3 秒）'
  return upgraded ? '自动 · 已放宽到 3 秒' : '自动（1.2 秒起）'
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

/**
 * 自动档要不要放宽。
 *
 * 三个信号，按可信度排序：
 * 1. 一分钟内被强行追平两次以上（观众已经在看跳帧）；
 * 2. 缓冲反复见底（马上就要卡）；
 * 3. 实测下载速率明显低于码率。
 *
 * 第 3 条阈值压到 0.6 倍，是因为「分片字节 ÷ HTTP 耗时」里混着服务端等分片的时间，
 * 系统性地偏低（实测约 0.6~0.8 倍），用 1.4 倍去比会在满速局域网里也误判。
 * 另外放宽之后不会自动降回来，误判一次就要用户手动改，所以宁可晚一点。
 */
export function shouldUpgradeTier(
  samples: TierSample[],
  jumps = 0,
): { upgrade: boolean; reason: string } {
  if (jumps >= 2) return { upgrade: true, reason: '网络跟不上，频繁追平直播边缘' }
  if (samples.length < 8) return { upgrade: false, reason: '' }

  const starved = samples.filter((item) => item.bufferMs > 0 && item.bufferMs < 900).length
  if (starved >= 3) return { upgrade: true, reason: '缓冲反复见底' }

  let download = 0
  let bitrate = 0
  for (const item of samples) {
    download += item.downloadKbps
    bitrate += item.videoKbps + item.audioKbps
  }
  download /= samples.length
  bitrate /= samples.length
  if (download > 0 && bitrate > 0 && download < bitrate * 0.6) {
    return { upgrade: true, reason: '下载速率明显低于码率' }
  }
  return { upgrade: false, reason: '' }
}
