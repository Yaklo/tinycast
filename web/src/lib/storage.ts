/**
 * 推流配置保存在本机浏览器里（需求建议使用 localStorage）。
 *
 * 后端从不保存令牌明文，也不提供"找回配置"的能力，因此这里存的就是唯一副本。
 */
export interface SavedPublisher {
  username: string
  room: string
  token: string
  createdAt: number
}

const STORAGE_KEY = 'tinycast.publisher.v1'

function storage(): Storage | null {
  try {
    const s = window.localStorage
    const probe = '__tinycast_probe__'
    s.setItem(probe, '1')
    s.removeItem(probe)
    return s
  } catch {
    // 隐私模式或禁用存储时静默降级：配置只存在于当前页面
    return null
  }
}

export function loadPublisher(): SavedPublisher | null {
  const s = storage()
  if (!s) return null
  try {
    const raw = s.getItem(STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<SavedPublisher>
    if (typeof parsed.username !== 'string' || typeof parsed.room !== 'string' || typeof parsed.token !== 'string') {
      return null
    }
    return {
      username: parsed.username,
      room: parsed.room,
      token: parsed.token,
      createdAt: typeof parsed.createdAt === 'number' ? parsed.createdAt : Date.now(),
    }
  } catch {
    return null
  }
}

export function savePublisher(publisher: SavedPublisher): void {
  const s = storage()
  if (!s) return
  try {
    s.setItem(STORAGE_KEY, JSON.stringify(publisher))
  } catch {
    // 配额不足等情况忽略即可，不影响推流本身
  }
}

export function clearPublisher(): void {
  const s = storage()
  if (!s) return
  try {
    s.removeItem(STORAGE_KEY)
  } catch {
    // 忽略
  }
}
