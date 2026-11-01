import { describe, expect, it } from 'vitest'
import { codecLabel, collectSample, emptySample, sparklinePath } from './stats'
import type { StatEntry } from './stats'

const videoTrack: StatEntry = {
  type: 'inbound-rtp',
  kind: 'video',
  codecId: 'COT01_video/H264',
  bytesReceived: 1000,
  packetsReceived: 100,
  packetsLost: 1,
  framesDecoded: 30,
  frameWidth: 1280,
  frameHeight: 720,
  jitter: 0.012,
  jitterBufferDelay: 0.6,
  jitterBufferEmittedCount: 30,
}

const audioTrack: StatEntry = {
  type: 'inbound-rtp',
  kind: 'audio',
  codecId: 'COT01_audio/opus',
  bytesReceived: 200,
  packetsReceived: 50,
  packetsLost: 0,
}

const pair: StatEntry = {
  type: 'candidate-pair',
  selected: true,
  state: 'succeeded',
  currentRoundTripTime: 0.034,
}

const codecs: StatEntry[] = [
  { type: 'codec', id: 'COT01_video/H264', mimeType: 'video/H264' },
  { type: 'codec', id: 'COT01_audio/opus', mimeType: 'audio/opus' },
]

function report(video: StatEntry = videoTrack, audio: StatEntry = audioTrack): StatEntry[] {
  return [video, audio, pair, ...codecs]
}

describe('codecLabel', () => {
  it('从 mimeType 取出编码名', () => {
    expect(codecLabel('video/H264')).toBe('H264')
    expect(codecLabel('audio/opus')).toBe('OPUS')
  })

  it('辅助编码与空值返回空串', () => {
    expect(codecLabel('video/rtx')).toBe('')
    expect(codecLabel('video/red')).toBe('')
    expect(codecLabel(undefined)).toBe('')
    expect(codecLabel('')).toBe('')
  })
})

describe('collectSample', () => {
  it('第一次采样只填累计量，码率为 0', () => {
    const s = collectSample(report(), 1000)
    expect(s.videoKbps).toBe(0)
    expect(s.audioKbps).toBe(0)
    expect(s.videoCodec).toBe('H264')
    expect(s.audioCodec).toBe('OPUS')
    expect(s.width).toBe(1280)
    expect(s.height).toBe(720)
    expect(s.framesDecoded).toBe(30)
    expect(s.receivedBytes).toBe(1200)
    expect(s.rttMs).toBe(34)
    expect(s.jitterMs).toBe(12)
    expect(s.bufferMs).toBe(20)
    expect(s.lossRate).toBe(0)
  })

  it('第二次采样按时间差算出码率、帧率与丢包率', () => {
    const prev = collectSample(report(), 1000)
    const next = collectSample(
      report(
        { ...videoTrack, bytesReceived: 1000 + 125_000, packetsReceived: 200, packetsLost: 6, framesDecoded: 60 },
        { ...audioTrack, bytesReceived: 200 + 5_000, packetsReceived: 100 },
      ),
      2000,
      prev,
    )
    expect(next.videoKbps).toBe(1000) // 125000 字节 / 1 秒 = 1000 kbps
    expect(next.audioKbps).toBe(40)
    expect(next.fps).toBe(30)
    expect(next.lost).toBe(5)
    // 窗口内收到 (100+50)=150 包，丢 5 包
    expect(next.lossRate).toBeCloseTo(5 / 155, 6)
  })

  it('优先采用浏览器上报的 framesPerSecond', () => {
    const prev = collectSample(report(), 1000)
    const next = collectSample(report({ ...videoTrack, framesPerSecond: 29.97, framesDecoded: 31 }), 2000, prev)
    expect(next.fps).toBe(30)
  })

  it('累计量回退时（重连）不会出现负码率', () => {
    const prev = collectSample(report(), 1000)
    const next = collectSample(report({ ...videoTrack, bytesReceived: 10, packetsReceived: 2, framesDecoded: 0 }), 2000, prev)
    expect(next.videoKbps).toBe(0)
    expect(next.audioKbps).toBe(0)
    expect(next.lost).toBe(0)
  })

  it('缺少候选对与音频轨道时安全降级', () => {
    const s = collectSample([videoTrack, ...codecs], 1000)
    expect(s.rttMs).toBe(0)
    expect(s.audioCodec).toBe('')
    expect(s.jitterMs).toBe(12)
  })

  it('空报告得到全零采样', () => {
    expect(collectSample([], 5)).toEqual({ ...emptySample(5) })
  })

  it('mediaType 写法（旧浏览器）同样能识别轨道', () => {
    const s = collectSample([{ ...videoTrack, kind: undefined, mediaType: 'video' }], 1000)
    expect(s.width).toBe(1280)
  })
})

describe('sparklinePath', () => {
  it('少于两个点时返回空串', () => {
    expect(sparklinePath([], 100, 50)).toBe('')
    expect(sparklinePath([5], 100, 50)).toBe('')
  })

  it('按最大值归一化并留出 10% 顶部余量', () => {
    expect(sparklinePath([0, 10], 100, 50)).toBe('M0 50 L100 4.55')
    expect(sparklinePath([5, 5], 100, 40)).toBe('M0 3.64 L100 3.64')
  })

  it('全 0 序列也不会除零', () => {
    expect(sparklinePath([0, 0, 0], 90, 30)).toBe('M0 30 L45 30 L90 30')
  })

  it('非法值按 0 处理', () => {
    expect(sparklinePath([Number.NaN, 10], 100, 50)).toBe('M0 50 L100 4.55')
  })
})
