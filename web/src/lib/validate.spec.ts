import { describe, expect, it } from 'vitest'
import { MAX_ROOM_NAME_RUNES, validateRoomName, validateUsername } from './validate'

describe('validateUsername', () => {
  it('接受合法用户名', () => {
    for (const name of ['alex', 'a1b', 'A1-_-01', 'user_name-01', 'a'.repeat(32)]) {
      expect(validateUsername(name)).toBe('')
    }
  })

  it('拒绝过短、过长与非法字符', () => {
    expect(validateUsername('')).not.toBe('')
    expect(validateUsername('ab')).not.toBe('')
    expect(validateUsername('A1')).not.toBe('')
    expect(validateUsername('a'.repeat(33))).not.toBe('')
    expect(validateUsername('-alex')).not.toBe('')
    expect(validateUsername('alex wang')).not.toBe('')
    expect(validateUsername('中文')).not.toBe('')
  })
})

describe('validateRoomName', () => {
  it('接受普通房间名', () => {
    expect(validateRoomName('深夜写代码')).toBe('')
    expect(validateRoomName('Room 01')).toBe('')
  })

  it('拒绝空名称、超长名称与控制字符', () => {
    expect(validateRoomName('   ')).not.toBe('')
    expect(validateRoomName('房'.repeat(MAX_ROOM_NAME_RUNES + 1))).not.toBe('')
    expect(validateRoomName('bad\u0007name')).not.toBe('')
  })
})
