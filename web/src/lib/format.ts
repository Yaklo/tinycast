/** 把毫秒时长格式化成 4:32 / 1:02:03 这样的倒计时。 */
export function formatDuration(ms: number): string {
  const total = Math.max(0, Math.ceil(ms / 1000))
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const seconds = total % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  if (hours > 0) return hours + ':' + pad(minutes) + ':' + pad(seconds)
  return minutes + ':' + pad(seconds)
}

/** Unix 毫秒转成本地时间 HH:mm。 */
export function formatClock(ms: number): string {
  if (!ms) return '-'
  const d = new Date(ms)
  return String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0')
}

/** 相对时间描述，用于"开播于 3 分钟前"。 */
export function formatAgo(ms: number): string {
  if (!ms) return '-'
  const diff = Date.now() - ms
  if (diff < 60_000) return '刚刚'
  if (diff < 3_600_000) return Math.floor(diff / 60_000) + ' 分钟前'
  if (diff < 86_400_000) return Math.floor(diff / 3_600_000) + ' 小时前'
  return Math.floor(diff / 86_400_000) + ' 天前'
}
