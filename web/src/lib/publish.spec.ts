import { describe, expect, it } from 'vitest'
import { buildPublishCode, buildStreamURL } from './publish'

describe('buildPublishCode', () => {
  it('用户名与令牌原样拼接，房间名做双重百分号编码', () => {
    expect(buildPublishCode('alex', '深夜写代码', 'TOKEN123')).toBe(
      'alex?room=%25E6%25B7%25B1%25E5%25A4%259C%25E5%2586%2599%25E4%25BB%25A3%25E7%25A0%2581&token=TOKEN123',
    )
  })

  it('OBS 解码一次后发到线上的仍是纯 ASCII，后端再解一层得到原房间名', () => {
    const code = buildPublishCode('alex', '测试', 'TOKEN123')
    const rawRoom = code.slice(code.indexOf('room=') + 5, code.indexOf('&token='))

    // OBS 会把推流码里的百分号编码先解一次（实测现象），线上因此是单次编码。
    const onWire = decodeURIComponent(rawRoom)
    expect(onWire).toBe('%E6%B5%8B%E8%AF%95')
    expect(/^[\x20-\x7e]*$/.test(onWire)).toBe(true)

    // SRS 解析成功，把 param 原样回调；后端 QueryUnescape + decodeRoomValue 还原展示名。
    expect(decodeURIComponent(onWire)).toBe('测试')
  })

  it('房间名里的 & 与 = 不会破坏查询串结构', () => {
    const code = buildPublishCode('alex', 'a&b=c', 'TOKEN123')
    const params = new URLSearchParams(code.slice(code.indexOf('?')))
    expect(params.get('token')).toBe('TOKEN123')
    expect(decodeURIComponent(decodeURIComponent(params.get('room') ?? ''))).toBe('a&b=c')
  })

  it('空格被编码成 %20 而不是保留原样', () => {
    expect(buildPublishCode('alex', 'a b', 'T')).toBe('alex?room=a%2520b&token=T')
  })
})

describe('buildStreamURL', () => {
  it('拼接服务器地址与推流码', () => {
    expect(buildStreamURL('rtmp://live.example.com/live', 'alex?room=x&token=y')).toBe(
      'rtmp://live.example.com/live/alex?room=x&token=y',
    )
  })

  it('服务器地址为空时只返回推流码', () => {
    expect(buildStreamURL('', 'alex')).toBe('alex')
  })

  it('去掉服务器地址结尾多余的斜杠', () => {
    expect(buildStreamURL('rtmp://h/live//', 'alex')).toBe('rtmp://h/live/alex')
  })
})
