<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import CopyField from './CopyField.vue'
import { roomsState } from '../composables/useRooms'
import { closePublishDialog, publishDialogMode, publishDialogOpen } from '../composables/usePublishDialog'
import { copyText } from '../lib/clipboard'
import { generateToken } from '../lib/token'
import { loadPublisher, savePublisher } from '../lib/storage'
import type { SavedPublisher } from '../lib/storage'
import { validateRoomName, validateUsername } from '../lib/validate'

const step = ref<'form' | 'result'>('form')
const username = ref('')
const room = ref('')
const usernameError = ref('')
const roomError = ref('')
const publishCode = ref('')
const savedPublisher = ref<SavedPublisher | null>(null)
const copiedAll = ref(false)

const rtmpServer = computed(() => roomsState.config?.rtmpServer ?? '')
const fullStreamURL = computed(() => (rtmpServer.value ? rtmpServer.value + '/' + publishCode.value : publishCode.value))

function resetForm(): void {
  savedPublisher.value = loadPublisher()
  username.value = savedPublisher.value?.username ?? ''
  room.value = savedPublisher.value?.room ?? ''
  usernameError.value = ''
  roomError.value = ''
  publishCode.value = ''
  step.value = 'form'
}

/** 用已保存的配置直接回到结果页（令牌仍只存在于本机）。 */
function showSaved(): void {
  const saved = loadPublisher()
  if (!saved) return
  savedPublisher.value = saved
  username.value = saved.username
  room.value = saved.room
  publishCode.value = buildPublishCode(saved.username, saved.room, saved.token)
  step.value = 'result'
}

function buildPublishCode(user: string, roomName: string, token: string): string {
  // 房间名可能包含中文或空格，必须整体编码；username 已在后端校验为安全字符。
  return user + '?room=' + encodeURIComponent(roomName) + '&token=' + token
}

function create(): void {
  usernameError.value = validateUsername(username.value)
  roomError.value = validateRoomName(room.value)
  if (usernameError.value || roomError.value) return

  const user = username.value.trim()
  const roomName = room.value.trim()
  const token = generateToken()
  const saved: SavedPublisher = { username: user, room: roomName, token, createdAt: Date.now() }

  savePublisher(saved)
  savedPublisher.value = saved
  publishCode.value = buildPublishCode(user, roomName, token)
  step.value = 'result'
}

async function copyAll(): Promise<void> {
  const text = '服务器：' + rtmpServer.value + '\n推流码：' + publishCode.value
  copiedAll.value = await copyText(text)
  window.setTimeout(() => {
    copiedAll.value = false
  }, 1800)
}

function regenerate(): void {
  resetForm()
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') closePublishDialog()
}

onMounted(() => window.addEventListener('keydown', onKeydown))
onBeforeUnmount(() => window.removeEventListener('keydown', onKeydown))

watch(publishDialogOpen, (open) => {
  if (!open) return
  resetForm()
  if (publishDialogMode.value === 'saved') showSaved()
})
</script>

<template>
  <Transition name="fade">
    <div v-if="publishDialogOpen" class="overlay" @click.self="closePublishDialog()">
      <div class="dialog" role="dialog" aria-modal="true" aria-labelledby="publish-title">
        <header class="dialog-head">
          <div>
            <h2 id="publish-title">{{ step === 'form' ? '创建推流配置' : 'OBS 推流配置' }}</h2>
            <p class="dialog-sub">
              {{
                step === 'form'
                  ? '填写用户名与房间名称，令牌会在你的浏览器里随机生成。'
                  : '把下面两项填进 OBS 的「直播」设置页，即可开始推流。'
              }}
            </p>
          </div>
          <button type="button" class="icon-btn" aria-label="关闭" @click="closePublishDialog()">×</button>
        </header>

        <div v-if="step === 'form'" class="dialog-body">
          <label class="field">
            <span class="field-label">用户名 <em>同时作为推流流名</em></span>
            <input
              v-model="username"
              class="input"
              type="text"
              autocomplete="off"
              spellcheck="false"
              placeholder="例如 alex_live"
              @keydown.enter.prevent="create"
            />
            <span v-if="usernameError" class="field-error">{{ usernameError }}</span>
            <span v-else class="field-hint">3-32 位，字母/数字/下划线/短横线，需以字母或数字开头。</span>
          </label>

          <label class="field">
            <span class="field-label">房间名称 <em>仅用于展示</em></span>
            <input
              v-model="room"
              class="input"
              type="text"
              autocomplete="off"
              placeholder="例如 深夜写代码"
              @keydown.enter.prevent="create"
            />
            <span v-if="roomError" class="field-error">{{ roomError }}</span>
            <span v-else class="field-hint">可以与别人重名，最多 40 个字符。</span>
          </label>

          <div class="note">
            <strong>关于令牌</strong>
            <p>
              令牌使用 <code>crypto.getRandomValues()</code> 生成 256 bit 随机值，整个过程只发生在浏览器里，
              不会向服务器预先注册房间。配置会保存在本机浏览器中。
            </p>
          </div>

          <div v-if="savedPublisher" class="saved-row">
            <span>本机已保存：@{{ savedPublisher.username }} · {{ savedPublisher.room }}</span>
            <button type="button" class="btn btn-ghost btn-sm" @click="showSaved">查看现有配置</button>
          </div>
        </div>

        <div v-else class="dialog-body">
          <ol class="steps">
            <li>打开 OBS，进入「设置 → 直播」。</li>
            <li>服务选择「自定义…」。</li>
            <li>下面两项分别填入「服务器」与「推流码」。</li>
          </ol>

          <CopyField label="服务器地址" :value="rtmpServer || '（未获取到地址，请刷新页面）'" />
          <CopyField label="推流码" :value="publishCode" hint="包含用户名、房间名与令牌" />

          <details class="advanced">
            <summary>查看完整推流地址</summary>
            <CopyField label="完整地址" :value="fullStreamURL" />
          </details>

          <div class="note note-warn">
            <strong>请妥善保存</strong>
            <p>
              服务器不会保存令牌明文，配置只存在这台设备的浏览器里。若清除浏览器数据，只能重新创建配置。
              同一用户名与令牌断开后 5 分钟内可继续重推，超时后用户名会被释放。
            </p>
          </div>
        </div>

        <footer class="dialog-foot">
          <template v-if="step === 'form'">
            <button type="button" class="btn btn-ghost" @click="closePublishDialog()">取消</button>
            <button type="button" class="btn btn-primary" @click="create">生成推流配置</button>
          </template>
          <template v-else>
            <button type="button" class="btn btn-ghost" @click="regenerate">重新生成</button>
            <button type="button" class="btn btn-ghost" @click="copyAll">{{ copiedAll ? '已复制' : '复制全部' }}</button>
            <button type="button" class="btn btn-primary" @click="closePublishDialog()">完成</button>
          </template>
        </footer>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.overlay {
  position: fixed;
  inset: 0;
  z-index: 60;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
  background: rgba(4, 7, 12, 0.66);
  backdrop-filter: blur(6px);
  overflow-y: auto;
}

.dialog {
  width: min(560px, 100%);
  border-radius: 20px;
  border: 1px solid var(--line-strong);
  background: var(--panel);
  box-shadow: 0 30px 80px rgba(2, 6, 23, 0.7);
  display: flex;
  flex-direction: column;
  max-height: calc(100vh - 40px);
}

.dialog-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 14px;
  padding: 22px 24px 14px;
}

.dialog-head h2 {
  margin: 0 0 6px;
  font-size: 19px;
  font-weight: 620;
}

.dialog-sub {
  margin: 0;
  font-size: 13px;
  color: var(--text-dim);
  line-height: 1.6;
}

.icon-btn {
  border: 1px solid var(--line);
  background: var(--panel-2);
  color: var(--text-dim);
  width: 30px;
  height: 30px;
  border-radius: 9px;
  font-size: 17px;
  line-height: 1;
  cursor: pointer;
  flex: none;
}

.icon-btn:hover {
  color: var(--text);
  border-color: var(--line-strong);
}

.dialog-body {
  padding: 4px 24px 8px;
  display: flex;
  flex-direction: column;
  gap: 16px;
  overflow-y: auto;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 7px;
}

.field-label {
  font-size: 13px;
  color: var(--text);
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.field-label em {
  font-style: normal;
  font-size: 11.5px;
  color: var(--text-faint);
}

.input {
  width: 100%;
  padding: 11px 13px;
  border-radius: 11px;
  border: 1px solid var(--line);
  background: #0d1219;
  color: var(--text);
  font-size: 14px;
  font-family: inherit;
  transition: border-color 0.16s ease, box-shadow 0.16s ease;
}

.input:focus {
  outline: none;
  border-color: rgba(129, 140, 248, 0.6);
  box-shadow: 0 0 0 3px rgba(99, 102, 241, 0.16);
}

.field-hint {
  font-size: 11.5px;
  color: var(--text-faint);
  line-height: 1.5;
}

.field-error {
  font-size: 11.5px;
  color: #fda4af;
  line-height: 1.5;
}

.note {
  border: 1px solid var(--line);
  border-radius: 12px;
  background: var(--panel-2);
  padding: 12px 14px;
  font-size: 12.5px;
  color: var(--text-dim);
  line-height: 1.65;
}

.note strong {
  display: block;
  color: var(--text);
  font-weight: 560;
  margin-bottom: 4px;
  font-size: 12.5px;
}

.note p {
  margin: 0;
}

.note code {
  font-family: var(--mono);
  font-size: 11.5px;
  color: #a5b4fc;
}

.note-warn {
  border-color: rgba(245, 158, 11, 0.32);
  background: rgba(245, 158, 11, 0.07);
}

.saved-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  font-size: 12.5px;
  color: var(--text-dim);
  padding-top: 2px;
}

.steps {
  margin: 0;
  padding-left: 18px;
  font-size: 13px;
  color: var(--text-dim);
  line-height: 1.9;
}

.advanced summary {
  cursor: pointer;
  font-size: 12.5px;
  color: var(--text-dim);
  margin-bottom: 10px;
}

.dialog-foot {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 16px 24px 22px;
  border-top: 1px solid var(--line);
  margin-top: 12px;
}

.btn-sm {
  padding: 5px 11px;
  font-size: 12.5px;
}

.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.16s ease;
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
