<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import StatusBadge from '../components/StatusBadge.vue'
import WebRTCPlayer from '../components/WebRTCPlayer.vue'
import { ApiError, fetchRoom } from '../api'
import { roomsState, findRoom } from '../composables/useRooms'
import { useCountdown } from '../composables/useCountdown'
import { formatAgo, formatClock } from '../lib/format'
import { mediaIssue } from '../lib/playback'
import type { MediaInfo, RoomView } from '../types'

const route = useRoute()
const username = computed(() => String(route.params.username ?? ''))

const fallback = ref<RoomView | null>(null)
const loading = ref(true)
const notFound = ref(false)
const fetchError = ref('')
/** 推流编码信息（来自 SRS），用于在播放前就提示"H.265 浏览器放不了"。 */
const media = ref<MediaInfo | null>(null)

const issue = computed(() => mediaIssue(media.value))

/** 以 SSE 快照为准；快照尚未到达时用一次请求兜底，快照到达后不再使用兜底数据。 */
const room = computed<RoomView | null>(() =>
  findRoom(username.value) ?? (roomsState.loaded ? null : fallback.value),
)

const { label: graceLabel, expired: graceExpired } = useCountdown(() => room.value?.graceUntil ?? 0)

async function load(): Promise<void> {
  loading.value = true
  notFound.value = false
  fetchError.value = ''

  if (findRoom(username.value)) {
    loading.value = false
    return
  }

  try {
    const data = await fetchRoom(username.value)
    fallback.value = data.room
    media.value = data.media ?? null
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      notFound.value = true
    } else {
      fetchError.value = err instanceof Error ? err.message : '加载房间失败'
    }
  } finally {
    loading.value = false
  }
}

/**
 * 单独取一次房间详情拿编码信息：SSE 快照里没有它，
 * 而 H.265 这类"注定黑屏"的情况值得在页面顶部先说清楚。
 */
async function loadMedia(): Promise<void> {
  try {
    const data = await fetchRoom(username.value)
    media.value = data.media ?? null
  } catch {
    // 编码信息只是提示增强，拿不到就按"未知"处理
  }
}

onMounted(() => {
  void load()
  void loadMedia()
})

watch(username, () => {
  fallback.value = null
  media.value = null
  void load()
  void loadMedia()
})

// 重新推流后编码可能变了（比如主播把 H.265 改成了 H.264），跟着刷新一次。
watch(
  () => room.value?.state,
  (state, prev) => {
    if (state && state !== prev) void loadMedia()
  },
)

watch(
  room,
  (value) => {
    document.title = value ? value.room + ' · TinyCast' : 'TinyCast · 轻量直播'
  },
  { immediate: true },
)
</script>

<template>
  <div class="room-page">
    <RouterLink to="/" class="back">← 返回房间列表</RouterLink>

    <div v-if="notFound" class="state-panel">
      <h2>房间不存在或已释放</h2>
      <p>断流超过保留时间后，用户名会连同房间一起释放。请让主播重新推流，或回到首页看看其他房间。</p>
      <RouterLink to="/" class="btn btn-primary">返回首页</RouterLink>
    </div>

    <div v-else-if="loading && !room" class="state-panel">
      <p>正在加载房间…</p>
    </div>

    <template v-else-if="room">
      <header class="room-head">
        <div class="title-block">
          <div class="title-row">
            <h1>{{ room.room }}</h1>
            <StatusBadge :state="room.state" />
          </div>
          <p class="sub">
            <span class="mono">@{{ room.username }}</span>
            <span class="dot" aria-hidden="true">·</span>
            <span v-if="room.state === 'LIVE'">开播于 {{ formatAgo(room.startedAt) }}</span>
            <span v-else>推流已断开</span>
          </p>
        </div>
      </header>

      <p v-if="room.warning === 'stream_unparsed'" class="media-warn">
        主播的推流码没有被服务器正确解析（房间名里有未编码的字符），观众端会一直黑屏。
        请让主播回到首页「创建推流配置」重新复制推流码后再推流。
      </p>

      <p v-if="room.state === 'LIVE' && issue" class="media-warn">
        {{ issue.title }}。{{ issue.detail }}
      </p>

      <div class="layout">
        <div class="stage">
          <WebRTCPlayer v-if="room.state === 'LIVE'" :room="room" :media="media" />

          <div v-else class="grace">
            <div class="grace-inner">
              <span class="grace-icon" aria-hidden="true">⏸</span>
              <h3>主播已断开推流</h3>
              <p>房间会继续保留，等待原身份重连。超时后房间与用户名会被自动释放。</p>
              <div class="grace-timer">
                <span class="timer-label">剩余保留时间</span>
                <span class="timer-value" :class="{ expired: graceExpired }">{{ graceLabel }}</span>
              </div>
            </div>
          </div>
        </div>

        <aside class="side">
          <div class="stat">
            <span class="stat-label">在线观看</span>
            <span class="stat-value">{{ room.viewerCount }}<em> 人</em></span>
          </div>
          <div class="stat">
            <span class="stat-label">推流状态</span>
            <span class="stat-value small">{{ room.state === 'LIVE' ? 'LIVE 推流中' : 'GRACE 断流保留' }}</span>
          </div>
          <div class="stat">
            <span class="stat-label">本轮开播</span>
            <span class="stat-value small">{{ formatClock(room.startedAt) }}</span>
          </div>
          <div v-if="room.state === 'GRACE'" class="stat">
            <span class="stat-label">自动释放</span>
            <span class="stat-value small">{{ formatClock(room.graceUntil) }}</span>
          </div>

          <p v-if="fetchError" class="side-note warn">{{ fetchError }}</p>
          <p v-if="!roomsState.connected" class="side-note">实时连接断开，正在自动重连…</p>
        </aside>
      </div>
    </template>

    <div v-else class="state-panel">
      <h2>房间已释放</h2>
      <p>断流超过保留时间后，房间与用户名已自动释放。请让主播重新推流，或回到首页看看其他房间。</p>
      <RouterLink to="/" class="btn btn-primary">返回首页</RouterLink>
    </div>
  </div>
</template>

<style scoped>
.room-page {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

.back {
  align-self: flex-start;
  font-size: 13px;
  color: var(--text-dim);
  text-decoration: none;
  padding: 4px 0;
}

.back:hover {
  color: var(--text);
}

.room-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.title-row {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.title-row h1 {
  margin: 0;
  font-size: clamp(22px, 3.4vw, 30px);
  font-weight: 640;
  letter-spacing: -0.01em;
  overflow-wrap: anywhere;
}

.sub {
  margin: 8px 0 0;
  font-size: 13px;
  color: var(--text-dim);
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.mono {
  font-family: var(--mono);
}

.dot {
  color: var(--text-faint);
}

.media-warn {
  margin: 0;
  padding: 12px 14px;
  border-radius: 12px;
  border: 1px solid rgba(245, 158, 11, 0.35);
  background: rgba(245, 158, 11, 0.08);
  color: #fcd34d;
  font-size: 12.5px;
  line-height: 1.7;
}

.layout {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 240px;
  gap: 20px;
  align-items: start;
}

.stage {
  min-width: 0;
}

.grace {
  aspect-ratio: 16 / 9;
  border-radius: 16px;
  border: 1px dashed rgba(245, 158, 11, 0.35);
  background: radial-gradient(circle at 50% 30%, rgba(245, 158, 11, 0.08), rgba(5, 8, 13, 0.95));
  display: grid;
  place-items: center;
  padding: 24px;
  text-align: center;
}

.grace-inner {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  max-width: 420px;
}

.grace-icon {
  font-size: 22px;
  width: 44px;
  height: 44px;
  border-radius: 14px;
  display: grid;
  place-items: center;
  background: rgba(245, 158, 11, 0.12);
  border: 1px solid rgba(245, 158, 11, 0.3);
  color: #fcd34d;
}

.grace h3 {
  margin: 4px 0 0;
  font-size: 17px;
  font-weight: 600;
}

.grace p {
  margin: 0;
  font-size: 13px;
  color: var(--text-dim);
  line-height: 1.75;
}

.grace-timer {
  margin-top: 8px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  align-items: center;
  padding: 10px 20px;
  border-radius: 12px;
  background: rgba(13, 19, 27, 0.8);
  border: 1px solid var(--line);
}

.timer-label {
  font-size: 11.5px;
  color: var(--text-faint);
  letter-spacing: 0.06em;
}

.timer-value {
  font-family: var(--mono);
  font-size: 22px;
  font-weight: 600;
  color: #fcd34d;
  font-variant-numeric: tabular-nums;
}

.timer-value.expired {
  color: #fda4af;
}

.side {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 6px 16px 14px;
  border-radius: 16px;
  border: 1px solid var(--line);
  background: var(--panel);
}

.stat {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 0;
  border-bottom: 1px solid var(--line);
}

.stat:last-of-type {
  border-bottom: none;
}

.stat-label {
  font-size: 12.5px;
  color: var(--text-faint);
}

.stat-value {
  font-size: 20px;
  font-weight: 620;
  font-variant-numeric: tabular-nums;
}

.stat-value em {
  font-style: normal;
  font-size: 12px;
  color: var(--text-faint);
  font-weight: 400;
}

.stat-value.small {
  font-size: 13px;
  font-weight: 500;
  color: var(--text);
}

.side-note {
  margin: 10px 0 0;
  font-size: 12px;
  color: var(--text-faint);
  line-height: 1.6;
}

.side-note.warn {
  color: #fda4af;
}

.state-panel {
  border: 1px solid var(--line);
  border-radius: 16px;
  background: var(--panel);
  padding: 40px 24px;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
}

.state-panel h2 {
  margin: 0;
  font-size: 18px;
}

.state-panel p {
  margin: 0 0 8px;
  font-size: 13px;
  color: var(--text-dim);
  line-height: 1.7;
  max-width: 460px;
}

@media (max-width: 860px) {
  .layout {
    grid-template-columns: minmax(0, 1fr);
  }

  .side {
    flex-direction: row;
    flex-wrap: wrap;
    gap: 0 18px;
  }

  .stat {
    flex: 1 1 130px;
    border-bottom: none;
  }
}
</style>
