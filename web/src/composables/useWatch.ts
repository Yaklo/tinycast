import { onBeforeUnmount, ref } from 'vue'
import { ApiError, deleteWatch, postWatch } from '../api'

/**
 * 观看心跳。
 *
 * 为什么不用 MediaMTX 的 readers 统计人数：HLS 的播放会话是按请求创建的短连接，
 * 同一个人反复拉播放列表会被算成很多次；WebRTC 会话又看不到 HLS 观众。
 * 统一由前端上报 viewerId，后端按 viewerId 去重，两种播放模式的人数口径才一致。
 *
 * viewerId 存在 sessionStorage 里：同一个标签页刷新不会重复计数，
 * 关掉页面后心跳自然停止，后端超时回收。
 */

const STORAGE_KEY = 'tinycast.viewerId'

/** 心跳间隔：远小于后端默认的 30 秒超时，允许丢一两次。 */
const HEARTBEAT_MS = 10_000
/** 标签页切到后台后仍按 10 秒心跳，避免被后端 30 秒超时清理。 */
const HIDDEN_HEARTBEAT_MS = 10_000
/** 房间已释放时先别彻底放弃：同名房间重新开播后应能自动恢复计数。 */
const RELEASED_RETRY_MS = 30_000
/**
 * 连续 404 达到这个次数就彻底停下。
 *
 * 只降频不停止的话，房间释放后每 30 秒一次的 404 会一直刷日志，
 * 而接口本身永远不会自愈；停掉之后由调用方（RoomView 观察到 LIVE）再 start()。
 */
const MAX_NOT_FOUND = 3
/** 429 没有给出 Retry-After 时的兜底重试间隔。 */
const WATCH_LIMIT_RETRY_MS = 30_000

export function viewerId(): string {
  try {
    const saved = sessionStorage.getItem(STORAGE_KEY)
    if (saved) return saved
  } catch {
    // 隐私模式下 sessionStorage 可能不可用，退化成内存里的随机 ID
  }
  const id = randomID()
  try {
    sessionStorage.setItem(STORAGE_KEY, id)
  } catch {
    // 忽略：本次会话内仍然可用同一个 ID
  }
  return id
}

function randomID(): string {
  const cryptoObj = globalThis.crypto
  if (cryptoObj && typeof cryptoObj.randomUUID === 'function') {
    return cryptoObj.randomUUID()
  }
  if (cryptoObj && typeof cryptoObj.getRandomValues === 'function') {
    const bytes = new Uint8Array(16)
    cryptoObj.getRandomValues(bytes)
    return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
  }
  return 'v-' + Math.random().toString(36).slice(2) + Date.now().toString(36)
}

export interface WatchOptions {
  /** 覆盖心跳间隔，测试用。 */
  intervalMs?: number
  /** 关闭自动启停，交给调用方手动控制。 */
  manual?: boolean
  /**
   * 自动启停前的门槛：返回 false 就不自己开始心跳。
   *
   * 房间断流进入 GRACE 时不该继续计数，而自动启动发生在 setup 里，
   * 那时调用方还没来得及 stop()——一次 POST 已经打出去了。交给调用方按房间状态判断，
   * 就不会给 GRACE 房间白白记一个人。
   */
  canWatch?: () => boolean
}

export function useWatch(username: () => string, options: WatchOptions = {}) {
  const interval = options.intervalMs ?? HEARTBEAT_MS
  const active = ref(false)
  /** 最近一次心跳的 429 原因；用于界面提示人数上限，不阻塞播放。 */
  const watchError = ref<ApiError | null>(null)
  const id = viewerId()

  let timer: ReturnType<typeof setTimeout> | null = null
  let current = ''
  let released = false
  /** 连续 404 次数，成功一次就清零。 */
  let notFoundCount = 0
  /** 429 要求的最早重试时间（绝对时间）；其余错误不延长正常心跳间隔。 */
  let retryUntil = 0
  /**
   * 会话世代：stop()/start()/leave() 都会递增。
   * 同一个 username 在 GRACE↔LIVE 之间切换时 current 不变，单靠 current 无法
   * 区分“上一次心跳”和“本次会话”，旧响应会把 429/404 记到新会话头上。
   */
  let epoch = 0

  function clearTimer(): void {
    if (timer !== null) {
      clearTimeout(timer)
      timer = null
    }
  }

  function schedule(): void {
    clearTimer()
    let delay = released
      ? RELEASED_RETRY_MS
      : typeof document !== 'undefined' && document.hidden
        ? Math.max(interval, HIDDEN_HEARTBEAT_MS)
        : interval
    // 429 的 Retry-After 是服务端明确要求的等待截止时间，不能被更短的正常间隔绕过，
    // 也不能因为切前后台把截止时间不断往后推。
    const wait = Math.max(0, retryUntil - Date.now())
    if (wait > delay) delay = wait
    timer = setTimeout(tick, delay)
  }

  async function tick(): Promise<void> {
    if (!active.value) return
    const user = current
    if (!user) return
    // 在 await 前固定世代：stop()/start() 之后返回的旧请求必须整体丢弃。
    const seq = epoch
    try {
      await postWatch(user, id)
      // 只认当前房间、当前会话的响应：切房/重开后旧结果不能算到新会话头上。
      if (seq === epoch && current === user) {
        released = false
        notFoundCount = 0
        retryUntil = 0
        watchError.value = null
      }
    } catch (err) {
      // 房间释放（404）时先降频重试：主播用同名房间重开播后还能自动恢复计数。
      // 连续多次 404 说明房间确实没了，停掉心跳，避免无限重试刷日志；
      // 之后由调用方观察到 LIVE 再 start()。网络抖动等其它错误保持正常间隔。
      // 同样只认当前房间：否则快速切换几个已释放的房间，几次在途 404 会累加到 3 次，
      // 把正在直播的新房间的心跳误停。
      if (seq === epoch && current === user && err instanceof ApiError && err.status === 404) {
        notFoundCount += 1
        released = true
        retryUntil = 0
        watchError.value = null
        if (notFoundCount >= MAX_NOT_FOUND) {
          stop()
          return
        }
      } else if (seq === epoch && current === user && err instanceof ApiError && err.status === 429) {
        // 人数上限不是播放错误：播放器继续工作，只是这个标签页暂时不计入人数。
        // 保持 active，按 Retry-After 退避重试；409 room_not_live 由房间状态
        // watcher 负责 stop()，不会走到这里的 404 停止逻辑。
        watchError.value = err
        released = false
        notFoundCount = 0
        const wait = err.retryAfter !== undefined ? Math.max(0, err.retryAfter) : WATCH_LIMIT_RETRY_MS
        retryUntil = Date.now() + wait
      } else if (seq === epoch && current === user && err instanceof ApiError && err.status === 409) {
        // 断流瞬间仍在途的心跳：停止计数，但保留 active，由房间状态 watcher
        // 在 GRACE 时 stop()、恢复 LIVE 时 start()，不会把这次失败算成房间不存在。
        // 若 watcher 已经先 stop()，恢复 LIVE 时 start() 会立即补拍；
        // 否则沿用当前定时器，在下一个间隔重试。
        watchError.value = null
        released = false
        retryUntil = 0
      } else if (seq === epoch && current === user) {
        // 网络错误/500 等：清掉上一次 429 的提示，但不要提前解除仍在生效的
        // Retry-After 退避；只有截止时间已过才按正常间隔继续。
        watchError.value = null
        if (retryUntil <= Date.now()) retryUntil = 0
      }
    }
    if (active.value && seq === epoch && current === user) schedule()
  }

  function start(): void {
    const user = username()
    if (!user) return
    if (active.value && current === user && !released) return
    stop()
    current = user
    released = false
    notFoundCount = 0
    retryUntil = 0
    watchError.value = null
    active.value = true
    // start() 立刻补一拍：从 GRACE 恢复 LIVE、404 之后重新开播都不用等下一个间隔。
    void tick()
  }

  function stop(): void {
    clearTimer()
    active.value = false
    // 让所有在途请求失效：尤其是同一 username 的 GRACE→LIVE 重开播。
    epoch += 1
    retryUntil = 0
  }

  /** 离开页面：主动减一，并且不再重排心跳。 */
  function leave(): void {
    const user = current
    stop()
    current = ''
    notFoundCount = 0
    retryUntil = 0
    watchError.value = null
    if (!user) return
    void deleteWatch(user, id).catch(() => {})
  }

  function onVisibilityChange(): void {
    if (!document.hidden) {
      // 回到前台：心跳若被连续 404 停掉，这里按房间状态重新开始（room 仍是 LIVE 时）。
      if (!active.value) {
        if (allowed()) start()
        return
      }
      // 429 退避期内不因为切回前台就绕过 Retry-After；按原截止时间等待。
      if (retryUntil > Date.now()) {
        schedule()
        return
      }
      // 立刻补一次心跳，避免后台降频期间被超时清理。
      void tick()
      return
    }
    if (active.value) schedule()
  }

  /** 调用方允许现在上报吗？没配置就一律允许。 */
  function allowed(): boolean {
    return options.canWatch ? options.canWatch() : true
  }

  /** 从 bfcache 返回时页面没有重新挂载，pagehide 里停掉的心跳必须重新启动。 */
  function onPageShow(event: PageTransitionEvent): void {
    // 同样先问过调用方：房间已经进入 GRACE 时别借 bfcache 恢复又开一次心跳。
    if (event.persisted && !active.value && allowed()) start()
  }

  if (!options.manual) {
    if (allowed()) start()
    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', onVisibilityChange)
    }
    if (typeof window !== 'undefined') {
      window.addEventListener('pagehide', leave)
      window.addEventListener('pageshow', onPageShow)
    }
    onBeforeUnmount(() => {
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', onVisibilityChange)
      }
      if (typeof window !== 'undefined') {
        window.removeEventListener('pagehide', leave)
        window.removeEventListener('pageshow', onPageShow)
      }
      leave()
    })
  }

  return { viewerId: id, active, watchError, start, stop, leave }
}
