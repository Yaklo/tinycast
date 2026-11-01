<script setup lang="ts">
import { computed } from 'vue'
import { roomsState } from '../composables/useRooms'
import { openPublishDialog } from '../composables/usePublishDialog'

const statusText = computed(() => (roomsState.connected ? '实时同步中' : '正在重连…'))
</script>

<template>
  <header class="site-header">
    <RouterLink to="/" class="brand">
      <span class="brand-mark" aria-hidden="true">
        <span class="brand-dot"></span>
      </span>
      <span class="brand-text">
        <span class="brand-name">TinyCast</span>
        <span class="brand-tag">轻量直播</span>
      </span>
    </RouterLink>

    <div class="header-right">
      <span class="conn" :class="{ live: roomsState.connected }" :title="statusText">
        <span class="conn-dot"></span>
        <span class="conn-text">{{ statusText }}</span>
      </span>
      <button type="button" class="btn btn-primary" @click="openPublishDialog()">创建推流配置</button>
    </div>
  </header>
</template>

<style scoped>
.site-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 18px clamp(16px, 4vw, 40px);
  border-bottom: 1px solid var(--line);
  backdrop-filter: blur(12px);
  position: sticky;
  top: 0;
  z-index: 20;
  background: rgba(10, 14, 20, 0.72);
}

.brand {
  display: inline-flex;
  align-items: center;
  gap: 12px;
  color: var(--text);
  text-decoration: none;
}

.brand-mark {
  width: 34px;
  height: 34px;
  border-radius: 11px;
  background: var(--grad);
  display: grid;
  place-items: center;
  box-shadow: 0 6px 18px rgba(99, 102, 241, 0.35);
}

.brand-dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: #0a0e14;
}

.brand-text {
  display: flex;
  flex-direction: column;
  line-height: 1.15;
}

.brand-name {
  font-weight: 650;
  letter-spacing: 0.02em;
  font-size: 17px;
}

.brand-tag {
  font-size: 12px;
  color: var(--text-dim);
}

.header-right {
  display: flex;
  align-items: center;
  gap: 14px;
}

.conn {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  font-size: 12.5px;
  color: var(--text-dim);
  padding: 5px 11px;
  border-radius: 999px;
  border: 1px solid var(--line);
  background: var(--panel-2);
}

.conn-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--text-dim);
  transition: background 0.2s ease;
}

.conn.live .conn-dot {
  background: #34d399;
  box-shadow: 0 0 0 3px rgba(52, 211, 153, 0.16);
}

@media (max-width: 560px) {
  .conn-text {
    display: none;
  }
  .brand-tag {
    display: none;
  }
}
</style>
