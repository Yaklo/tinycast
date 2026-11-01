<script setup lang="ts">
import { onMounted } from 'vue'
import AppHeader from './components/AppHeader.vue'
import { startRooms } from './composables/useRooms'

onMounted(() => {
  startRooms()
})
</script>

<template>
  <div class="shell">
    <AppHeader />
    <main class="main">
      <RouterView />
    </main>
    <footer class="foot">
      <span>TinyCast · MediaMTX + Go + Vue</span>
      <span class="foot-sep">·</span>
      <span>无需注册，推流即开房</span>
    </footer>
  </div>
</template>

<style scoped>
.shell {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}

.main {
  flex: 1;
  width: 100%;
  max-width: 1120px;
  margin: 0 auto;
  padding: clamp(20px, 4vw, 40px) clamp(16px, 4vw, 40px) 56px;
}

.foot {
  border-top: 1px solid var(--line);
  padding: 18px clamp(16px, 4vw, 40px) 26px;
  display: flex;
  gap: 8px;
  justify-content: center;
  font-size: 12px;
  color: var(--text-faint);
  flex-wrap: wrap;
}

.foot-sep {
  opacity: 0.5;
}
</style>
