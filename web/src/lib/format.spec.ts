import { describe, expect, it, vi } from 'vitest'
import { formatAgo, formatClock, formatDuration } from './format'

describe('formatDuration', () => {
  it('按 分:秒 与 时:分:秒 输出倒计时', () => {
    expect(formatDuration(0)).toBe('0:00')
    expect(formatDuration(65_000)).toBe('1:05')
    expect(formatDuration(125_000)).toBe('2:05')
    expect(formatDuration(3_600_000 + 61_000)).toBe('1:01:01')
  })

  it('负数时长归零而不是出现负号', () => {
    expect(formatDuration(-5_000)).toBe('0:00')
  })
})

describe('formatClock', () => {
  it('时间戳为 0 时显示占位符', () => {
    expect(formatClock(0)).toBe('-')
  })

  it('输出补零的 HH:mm', () => {
    const ts = new Date(2026, 10, 1, 9, 5).getTime()
    expect(formatClock(ts)).toBe('09:05')
  })
})

describe('formatAgo', () => {
  it('时间戳为 0 时显示占位符', () => {
    expect(formatAgo(0)).toBe('-')
  })

  it('按分钟/小时/天分级描述', () => {
    const now = new Date('2026-11-01T12:00:00Z').getTime()
    vi.spyOn(Date, 'now').mockReturnValue(now)
    expect(formatAgo(now - 10_000)).toBe('刚刚')
    expect(formatAgo(now - 5 * 60_000)).toBe('5 分钟前')
    expect(formatAgo(now - 3 * 3_600_000)).toBe('3 小时前')
    expect(formatAgo(now - 2 * 86_400_000)).toBe('2 天前')
  })
})
