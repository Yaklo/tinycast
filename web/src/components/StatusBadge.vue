<script setup lang="ts">
import type { RoomState } from '../types'

defineProps<{ state: RoomState }>()
</script>

<template>
  <span class="badge" :class="state === 'LIVE' ? 'badge-live' : 'badge-grace'">
    <span class="badge-dot"></span>
    {{ state === 'LIVE' ? '直播中' : '断流保留' }}
  </span>
</template>

<style scoped>
.badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  font-weight: 500;
  padding: 4px 9px;
  border-radius: 999px;
  border: 1px solid transparent;
  white-space: nowrap;
}

.badge-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.badge-live {
  color: #fda4af;
  background: rgba(244, 63, 94, 0.12);
  border-color: rgba(244, 63, 94, 0.3);
}

.badge-live .badge-dot {
  background: #fb7185;
  animation: pulse 1.6s ease-in-out infinite;
}

.badge-grace {
  color: #fcd34d;
  background: rgba(245, 158, 11, 0.12);
  border-color: rgba(245, 158, 11, 0.28);
}

@keyframes pulse {
  0%, 100% {
    opacity: 1;
    transform: scale(1);
  }
  50% {
    opacity: 0.45;
    transform: scale(0.82);
  }
}
</style>
