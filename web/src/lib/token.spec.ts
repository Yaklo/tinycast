import { describe, expect, it } from 'vitest'
import { generateToken, maskToken } from './token'

describe('generateToken', () => {
  it('默认生成 256 bit 的 base64url 令牌', () => {
    const token = generateToken()
    // 32 字节 → 43 个 base64url 字符（无填充）
    expect(token).toHaveLength(43)
    expect(token).toMatch(/^[A-Za-z0-9_-]+$/)
  })

  it('每次生成都不同', () => {
    const tokens = new Set(Array.from({ length: 50 }, () => generateToken()))
    expect(tokens.size).toBe(50)
  })

  it('拒绝少于 256 bit 的请求', () => {
    expect(() => generateToken(31)).toThrowError('推流令牌至少需要 256 bit 随机量')
    expect(() => generateToken(16)).toThrowError(/256 bit/)
    expect(() => generateToken(0)).toThrow()
  })

  it('maskToken 只暴露首尾各 4 位', () => {
    const token = 'abcdefghijklmnop'
    expect(maskToken(token)).toBe('abcd…mnop')
  })

  it('maskToken 对过短的令牌不泄露任何字符', () => {
    expect(maskToken('short')).toBe('****')
    expect(maskToken('0123456789')).toBe('****')
  })
})
