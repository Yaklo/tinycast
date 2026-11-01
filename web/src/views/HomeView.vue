<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import PublishConfigDialog from '../components/PublishConfigDialog.vue'
import RoomCard from '../components/RoomCard.vue'
import { liveRoomCount, roomsState } from '../composables/useRooms'
import { openPublishDialog, publishDialogOpen } from '../composables/usePublishDialog'
import { clearPublisher, loadPublisher } from '../lib/storage'
import type { SavedPublisher } from '../lib/storage'
import { maskToken } from '../lib/token'

const saved = ref<SavedPublisher | null>(loadPublisher())

watch(publishDialogOpen, (open) => {
  if (!open) saved.value = loadPublisher()
})

const rooms = computed(() => roomsState.rooms)
const liveCount = computed(() => liveRoomCount())
const graceCount = computed(() => rooms.value.length - liveCount.value)
const loading = computed(() => !roomsState.loaded && rooms.value.length === 0)

function forgetSaved(): void {
  clearPublisher()
  saved.value = null
}
</script>

<template>
  <div class="home">
    <section class="hero">
      <p class="eyebrow">OBS 推流 · 浏览器直看 · 无需注册</p>
      <h1 class="hero-title">
        用 OBS 推流，<br />
        <span class="grad-text">分享一条链接就能看。</span>
      </h1>
      <p class="hero-sub">
        TinyCast 只做三件事：校验推流身份、维护房间生命周期、把 WebRTC 画面送到浏览器。
        没有账号系统，也没有数据库，推流一开，房间即刻出现。
      </p>
      <div class="hero-actions">
        <button type="button" class="btn btn-primary btn-lg" @click="openPublishDialog()">创建推流配置</button>
        <button v-if="saved" type="button" class="btn btn-ghost btn-lg" @click="openPublishDialog('saved')">
          查看我的配置
        </button>
      </div>
    </section>

    <section class="rooms">
      <div class="rooms-head">
        <h2>正在直播</h2>
        <div class="rooms-meta">
          <span class="meta-item"><b>{{ liveCount }}</b> 路直播中</span>
          <span v-if="graceCount > 0" class="meta-item warn"><b>{{ graceCount }}</b> 个保留中</span>
        </div>
      </div>

      <p v-if="roomsState.error" class="banner banner-error">{{ roomsState.error }}</p>

      <div v-if="loading" class="grid">
        <div v-for="n in 3" :key="n" class="skeleton"></div>
      </div>

      <div v-else-if="rooms.length > 0" class="grid">
        <RoomCard v-for="room in rooms" :key="room.username" :room="room" />
      </div>

      <div v-else class="empty">
        <div class="empty-mark" aria-hidden="true">
          <span></span>
        </div>
        <h3>现在还没有人开播</h3>
        <p>点击「创建推流配置」，把生成的地址填进 OBS，开始推流后房间会立刻出现在这里。</p>
        <button type="button" class="btn btn-primary" @click="openPublishDialog()">创建推流配置</button>
      </div>
    </section>

    <section v-if="saved" class="mine">
      <div class="mine-info">
        <span class="mine-label">本机保存的推流配置</span>
        <span class="mine-name">{{ saved.room }}</span>
        <span class="mine-meta">@{{ saved.username }} · 令牌 {{ maskToken(saved.token) }}</span>
      </div>
      <div class="mine-actions">
        <button type="button" class="btn btn-ghost btn-sm" @click="openPublishDialog('saved')">查看推流配置</button>
        <button type="button" class="btn btn-ghost btn-sm danger" @click="forgetSaved">删除</button>
      </div>
    </section>

    <PublishConfigDialog />
  </div>
</template>

<style scoped>
.home {
  display: flex;
  flex-direction: column;
  gap: 44px;
}

.hero {
  padding: clamp(20px, 5vw, 52px) 0 0;
  max-width: 760px;
}

.eyebrow {
  margin: 0 0 14px;
  font-size: 12.5px;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  color: var(--text-faint);
}

.hero-title {
  margin: 0 0 18px;
  font-size: clamp(30px, 5.4vw, 46px);
  line-height: 1.18;
  font-weight: 660;
  letter-spacing: -0.015em;
}

.grad-text {
  background: var(--grad);
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}

.hero-sub {
  margin: 0 0 26px;
  font-size: 14.5px;
  line-height: 1.85;
  color: var(--text-dim);
  max-width: 620px;
}

.hero-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
}

.btn-lg {
  padding: 11px 22px;
  font-size: 14px;
  border-radius: 12px;
}

.rooms {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.rooms-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 12px;
}

.rooms-head h2 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
}

.rooms-meta {
  display: flex;
  gap: 14px;
  font-size: 12.5px;
  color: var(--text-faint);
}

.meta-item b {
  color: var(--text);
  font-weight: 600;
}

.meta-item.warn b {
  color: #fcd34d;
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 16px;
}

.skeleton {
  height: 156px;
  border-radius: 16px;
  border: 1px solid var(--line);
  background: linear-gradient(100deg, var(--panel) 30%, var(--panel-2) 50%, var(--panel) 70%);
  background-size: 240% 100%;
  animation: shimmer 1.5s ease-in-out infinite;
}

.empty {
  border: 1px dashed var(--line-strong);
  border-radius: 18px;
  padding: 46px 24px;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  background: linear-gradient(180deg, rgba(255, 255, 255, 0.02), transparent);
}

.empty-mark {
  width: 46px;
  height: 46px;
  border-radius: 14px;
  background: var(--panel-2);
  border: 1px solid var(--line);
  display: grid;
  place-items: center;
  margin-bottom: 4px;
}

.empty-mark span {
  width: 12px;
  height: 12px;
  border-radius: 50%;
  background: var(--grad);
}

.empty h3 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
}

.empty p {
  margin: 0 0 8px;
  font-size: 13px;
  color: var(--text-dim);
  line-height: 1.7;
  max-width: 420px;
}

.banner {
  margin: 0;
  padding: 10px 14px;
  border-radius: 11px;
  font-size: 13px;
}

.banner-error {
  background: rgba(244, 63, 94, 0.1);
  border: 1px solid rgba(244, 63, 94, 0.28);
  color: #fda4af;
}

.mine {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
  padding: 16px 18px;
  border-radius: 16px;
  border: 1px solid var(--line);
  background: var(--panel);
}

.mine-info {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.mine-label {
  font-size: 11.5px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--text-faint);
}

.mine-name {
  font-size: 15px;
  font-weight: 560;
}

.mine-meta {
  font-size: 12.5px;
  color: var(--text-dim);
  font-family: var(--mono);
}

.mine-actions {
  display: flex;
  gap: 8px;
}

.btn-sm {
  padding: 7px 13px;
  font-size: 12.5px;
}

.danger:hover {
  color: #fda4af;
  border-color: rgba(244, 63, 94, 0.4);
}

@keyframes shimmer {
  0% {
    background-position: 140% 0;
  }
  100% {
    background-position: -40% 0;
  }
}
</style>
