import { isAV1, isH265 } from './playback'

/**
 * HLS 的播放引擎选择。
 *
 * 浏览器里有两条完全不同的 HLS 管线：
 * - js：hls.js + MSE。缺点是要自己解封装、指标才看得见，而且 Safari 的 MSE 只解 H.264；
 * - native：交给浏览器（Safari / iOS）的原生 HLS 管线。省电、支持 AirPlay / 画中画，
 *   但 JS 拿不到分片信息，统计面板只能给出分辨率、帧率这类基础值。
 *
 * 历史上我们是"Safari 一律原生、其它浏览器一律 hls.js"，但用户没法干预：Safari 上
 * H.265 放不出来、或者原生管线把延迟拖到十几秒时，只能干看着。这里把它做成三档偏好。
 */

export type EnginePref = 'auto' | 'js' | 'native'
export type Engine = 'js' | 'native'

export interface EngineContext {
  pref: EnginePref
  /** 是不是 Safari 家族（Chromium 即使 canPlayType 返回 "maybe" 也不算）。 */
  safariLike: boolean
  /** video.canPlayType('application/vnd.apple.mpegurl') 非空。 */
  canPlayNative: boolean
  /** 有没有 MSE / ManagedMediaSource，也就是 hls.js 能不能跑。 */
  mseSupported: boolean
  /** 流里的视频编码，例如 H264 / H265 / AV1。 */
  videoCodec?: string | null
}

export interface EngineDecision {
  engine: Engine
  /** 实际用的引擎与用户选的不一致时为 true（浏览器能力不够，被降级了）。 */
  overridden: boolean
  /** 需要告诉用户的一句话，空串表示不用提示。 */
  notice: string
}

export const ENGINE_OPTIONS: { value: EnginePref; label: string; hint: string }[] = [
  { value: 'auto', label: '自动', hint: 'Safari 用原生播放器，Chrome / Edge 用 HLS.js' },
  { value: 'js', label: 'HLS.js', hint: '统一走 MSE，指标最全；Safari 上只解 H.264' },
  { value: 'native', label: '原生', hint: '交给浏览器自己的 HLS 管线，省电、支持 AirPlay' },
]

/** Chromium 的 canPlayType 对 HLS 也返回 "maybe"，但它没有真正的 HLS 管线。 */
export function isSafariLike(userAgent: string): boolean {
  return !/Chrome\/|Chromium\/|Edg\/|Android/.test(userAgent)
}

/** 有没有 MSE（hls.js 的前提）。 */
export function mseSupported(): boolean {
  if (typeof window === 'undefined') return false
  const scope = window as unknown as { MediaSource?: unknown; ManagedMediaSource?: unknown }
  return typeof scope.MediaSource !== 'undefined' || typeof scope.ManagedMediaSource !== 'undefined'
}

export function engineLabel(engine: Engine | '' ): string {
  if (engine === 'js') return 'HLS.js'
  if (engine === 'native') return '原生播放器'
  return '—'
}

export function prefLabel(pref: EnginePref): string {
  return ENGINE_OPTIONS.find((option) => option.value === pref)?.label ?? '自动'
}

/**
 * 决定这次用哪个引擎。
 *
 * 规则：
 * - auto：保持老行为——Safari 走原生，其余走 hls.js；
 * - js：只要浏览器有 MSE 就用 hls.js，哪怕在 Safari 上（代价是 H.265 / AV1 可能黑屏）；
 * - native：只有 Safari 家族能真正用上；Chrome / Edge 的 canPlayType 是假阳性，会降级到 hls.js。
 */
export function resolveEngine(ctx: EngineContext): EngineDecision {
  const nativeReady = ctx.safariLike && ctx.canPlayNative
  const highCodec = isH265(ctx.videoCodec ?? undefined) || isAV1(ctx.videoCodec ?? undefined)

  if (ctx.pref === 'native') {
    if (nativeReady) return { engine: 'native', overridden: false, notice: '' }
    return {
      engine: 'js',
      overridden: true,
      notice: ctx.mseSupported
        ? '这个浏览器没有原生 HLS 播放能力，已改用 HLS.js。'
        : '这个浏览器既没有原生 HLS 也没有 MSE，HLS 放不出来，请改用「WebRTC」通道。',
    }
  }

  if (ctx.pref === 'js') {
    if (!ctx.mseSupported) {
      if (nativeReady) {
        return {
          engine: 'native',
          overridden: true,
          notice: '这个浏览器没有 MSE，无法使用 HLS.js，已改用原生播放器。',
        }
      }
      return {
        engine: 'js',
        overridden: true,
        notice: '这个浏览器缺少 MSE，HLS 放不出来，请改用「WebRTC」通道或换浏览器。',
      }
    }
    return {
      engine: 'js',
      overridden: false,
      notice: highCodec && ctx.safariLike
        ? 'Safari 的 MSE 只解 H.264，当前是 H.265 / AV1，可能黑屏；放不出来就把播放方式切回「原生」。'
        : '',
    }
  }

  if (nativeReady) {
    return {
      engine: 'native',
      overridden: false,
      notice: highCodec
        ? '原生播放器依赖系统硬件解码 H.265 / AV1；放不出来时可在 🩺 面板把播放方式改成「HLS.js」。'
        : '',
    }
  }

  return { engine: 'js', overridden: false, notice: '' }
}
