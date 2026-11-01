<script setup lang="ts">
import StatusBadge from './StatusBadge.vue'
import { useCountdown } from '../composables/useCountdown'
import { formatAgo } from '../lib/format'
import { codecLabel } from '../lib/playback'
import type { RoomView } from '../types'

const props = defineProps<{ room: RoomView }>()

const { label: graceLabel, expired: graceExpired } = useCountdown(() => props.room.graceUntil)
</script>

<template>
  <RouterLink class="room-card" :to="'/r/' + room.username" :class="'is-' + room.state.toLowerCase()">
    <div class="card-top">
      <StatusBadge :state="room.state" />
      <span class="viewers" :title="room.viewerCount + ' 人在线观看'">
        <svg viewBox="0 0 20 20" fill="none" aria-hidden="true">
          <path
            d="M1.8 10s2.9-5.2 8.2-5.2S18.2 10 18.2 10s-2.9 5.2-8.2 5.2S1.8 10 1.8 10Z"
            stroke="currentColor"
            stroke-width="1.4"
            stroke-linejoin="round"
          />
          <circle cx="10" cy="10" r="2.4" stroke="currentColor" stroke-width="1.4" />
        </svg>
        {{ room.viewerCount }}
      </span>
    </div>

    <h3 class="room-name">{{ room.room }}</h3>
    <p class="room-user">@{{ room.username }}</p>

    <p v-if="room.media?.videoCodec" class="card-codec">
      <span class="codec">{{ codecLabel(room.media.videoCodec) }}</span>
      <span v-if="room.media.width && room.media.height" class="resolution">
        {{ room.media.width }}×{{ room.media.height }}
      </span>
    </p>

    <div class="card-foot">
      <span v-if="room.state === 'GRACE'" class="foot-grace">
        {{ graceExpired ? '即将释放' : '保留中 · ' + graceLabel }}
      </span>
      <span v-else class="foot-live">开播于 {{ formatAgo(room.startedAt) }}</span>
      <span class="enter">进入 <span aria-hidden="true">→</span></span>
    </div>
  </RouterLink>
</template>

<style scoped>
.room-card {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 18px;
  border-radius: 16px;
  border: 1px solid var(--line);
  background: linear-gradient(180deg, rgba(255, 255, 255, 0.04), rgba(255, 255, 255, 0.01));
  color: inherit;
  text-decoration: none;
  overflow: hidden;
  transition: transform 0.18s ease, border-color 0.18s ease, box-shadow 0.18s ease;
}

.room-card::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
  pointer-events: none;
  opacity: 0;
  transition: opacity 0.18s ease;
  background: radial-gradient(120% 80% at 100% 0%, rgba(99, 102, 241, 0.16), transparent 60%);
}

.room-card:hover {
  transform: translateY(-2px);
  border-color: rgba(129, 140, 248, 0.45);
  box-shadow: 0 14px 34px rgba(2, 6, 23, 0.55);
}

.room-card:hover::after {
  opacity: 1;
}

.card-top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.viewers {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12.5px;
  color: var(--text-dim);
}

.viewers svg {
  width: 15px;
  height: 15px;
}

.room-name {
  margin: 2px 0 0;
  font-size: 18px;
  font-weight: 600;
  line-height: 1.35;
  overflow-wrap: anywhere;
}

.room-user {
  margin: 0;
  font-size: 13px;
  color: var(--text-dim);
  font-family: var(--mono);
  overflow-wrap: anywhere;
}

.card-codec {
  margin: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 11.5px;
  color: var(--text-faint);
}

.codec {
  padding: 1px 6px;
  border-radius: 6px;
  border: 1px solid var(--line);
  color: var(--text-dim);
  font-family: var(--mono);
}

.card-foot {
  margin-top: auto;
  padding-top: 12px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  font-size: 12.5px;
  color: var(--text-faint);
  border-top: 1px solid var(--line);
}

.foot-grace {
  color: #fcd34d;
}

.enter {
  color: var(--text-dim);
  transition: color 0.18s ease;
}

.room-card:hover .enter {
  color: var(--accent-2);
}
</style>
