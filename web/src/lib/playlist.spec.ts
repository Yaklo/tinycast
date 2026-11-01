import { describe, expect, it } from 'vitest'
import { deliveryLabel, firstVariantUrl, hasLowLatencyParts } from './playlist'

const MASTER = [
  '#EXTM3U',
  '#EXT-X-VERSION:9',
  '#EXT-X-INDEPENDENT-SEGMENTS',
  '#EXT-X-STREAM-INF:BANDWIDTH=2800000,CODECS="avc1.64001e,mp4a.40.2"',
  'video1_stream.m3u8?session=8f3c',
].join('\n')

describe('hasLowLatencyParts', () => {
  it('认出 LL-HLS 的分片标记', () => {
    expect(hasLowLatencyParts('#EXT-X-PART-INF:PART-TARGET=0.20000')).toBe(true)
    expect(hasLowLatencyParts('#EXT-X-PART:DURATION=0.20000,URI="part1.mp4",INDEPENDENT=YES')).toBe(true)
    expect(hasLowLatencyParts('#EXT-X-SERVER-CONTROL:CAN-BLOCK-RELOAD=YES,PART-HOLD-BACK=0.50000')).toBe(true)
  })

  it('普通 HLS 与空清单不算', () => {
    expect(hasLowLatencyParts('#EXTM3U\n#EXTINF:4.00000,\nseg1.mp4')).toBe(false)
    expect(hasLowLatencyParts('')).toBe(false)
  })
})

describe('firstVariantUrl', () => {
  it('把相对子清单解析成绝对地址', () => {
    expect(firstVariantUrl(MASTER, 'http://127.0.0.1:18855/hls/live/alex/index.m3u8')).toBe(
      'http://127.0.0.1:18855/hls/live/alex/video1_stream.m3u8?session=8f3c',
    )
  })

  it('已经是绝对地址时原样返回', () => {
    const master = '#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nhttp://cdn.example.com/a.m3u8'
    expect(firstVariantUrl(master, 'http://127.0.0.1:18855/x/index.m3u8')).toBe('http://cdn.example.com/a.m3u8')
  })

  it('跳过 # 注释行再取地址', () => {
    const master = '#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n#EXT-X-FOO:bar\nchild.m3u8'
    expect(firstVariantUrl(master, 'http://h/a/index.m3u8')).toBe('http://h/a/child.m3u8')
  })

  it('不是 master 清单时返回空串', () => {
    expect(firstVariantUrl('#EXTM3U\n#EXTINF:4,\nseg1.mp4', 'http://h/a/index.m3u8')).toBe('')
    expect(firstVariantUrl('', 'http://h/a/index.m3u8')).toBe('')
  })
})

describe('deliveryLabel', () => {
  it('把 LL-HLS 与普通 HLS 分开写', () => {
    expect(deliveryLabel('ll-hls')).toContain('LL-HLS')
    expect(deliveryLabel('hls')).toBe('HLS（整段分片）')
    expect(deliveryLabel('whep')).toContain('WHEP')
    expect(deliveryLabel('unknown')).toBe('—')
  })
})
