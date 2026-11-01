import { computed, onBeforeUnmount, ref } from 'vue'
import { formatDuration } from '../lib/format'
import { serverNow } from './useRooms'

/**
 * 针对某个服务器时间点（Unix 毫秒）的倒计时。
 *
 * 目标时间来自后端快照，基准时间用服务器时钟偏移校正，因此本机时钟不准也不影响。
 */
export function useCountdown(target: () => number) {
  const now = ref(serverNow())
  const timer = window.setInterval(() => {
    now.value = serverNow()
  }, 500)

  onBeforeUnmount(() => window.clearInterval(timer))

  const remaining = computed(() => Math.max(0, target() - now.value))
  const label = computed(() => formatDuration(remaining.value))
  const expired = computed(() => remaining.value <= 0)

  return { remaining, label, expired }
}
