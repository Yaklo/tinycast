/**
 * HLS 清单的解析小工具。
 *
 * 需要它是因为「HLS」这个词其实盖了两种延迟完全不同的东西：
 * - LL-HLS：清单里有 #EXT-X-PART 这种 0.2 秒的分片（部分分片），客户端能贴着直播边缘播；
 * - 普通 HLS：只有整段分片，客户端至少退 1~3 个分片才起播。
 *
 * 两者在页面上都写成「HLS」，观众看不出为什么一个 1 秒、一个十几秒，所以统计面板里
 * 要能把它们分开。hls.js 会通过 partTarget 告诉我们，原生播放器就只能自己去读清单。
 */

/** 清单里是否带 LL-HLS 的分片标记（#EXT-X-PART / #EXT-X-PART-INF / PART-HOLD-BACK）。 */
export function hasLowLatencyParts(playlist: string): boolean {
  if (!playlist) return false
  return /#EXT-X-PART:|#EXT-X-PART-INF:|PART-HOLD-BACK=/.test(playlist)
}

/** 从 master 清单里取出第一个子清单地址并解析成绝对地址；不是 master 清单时返回空串。 */
export function firstVariantUrl(playlist: string, baseUrl: string): string {
  if (!playlist || !playlist.includes('#EXT-X-STREAM-INF:')) return ''
  const lines = playlist.split(/\r?\n/)
  for (let i = 0; i < lines.length; i += 1) {
    if (!lines[i].trim().startsWith('#EXT-X-STREAM-INF:')) continue
    for (let j = i + 1; j < lines.length; j += 1) {
      const line = lines[j].trim()
      if (!line || line.startsWith('#')) continue
      try {
        return new URL(line, baseUrl).toString()
      } catch {
        return line
      }
    }
  }
  return ''
}

/** 播放时真正在用的分发协议，和界面上「播放通道（HLS / WebRTC）」是两件事。 */
export type Delivery = 'll-hls' | 'hls' | 'whep' | 'unknown'

export function deliveryLabel(delivery: Delivery): string {
  switch (delivery) {
    case 'll-hls':
      return 'LL-HLS（分片 0.2s）'
    case 'whep':
      return 'WHEP（WebRTC）'
    case 'hls':
      return 'HLS（整段分片）'
    default:
      return '—'
  }
}
