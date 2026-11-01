<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { fetchPlay } from '../api'
import type { RoomView } from '../types'

const props = defineProps<{ room: RoomView }>()

type Phase = 'connecting' | 'playing' | 'error'

const videoEl = ref<HTMLVideoElement | null>(null)
const phase = ref<Phase>('connecting')
const message = ref('正在建立连接…')
const muted = ref(true)

let pc: RTCPeerConnection | null = null
let attempt = 0
let retryTimer: number | null = null

function clearRetry(): void {
  if (retryTimer !== null) {
    window.clearTimeout(retryTimer)
    retryTimer = null
  }
}

function teardown(): void {
  if (pc) {
    pc.ontrack = null
    pc.onconnectionstatechange = null
    pc.close()
    pc = null
  }
  if (videoEl.value) {
    videoEl.value.srcObject = null
  }
}

/** 等 ICE 收集完成再发 offer：SRS 的 WHEP 需要 offer 里已带候选地址。 */
function waitForIce(connection: RTCPeerConnection, timeoutMs = 2500): Promise<void> {
  if (connection.iceGatheringState === 'complete') return Promise.resolve()
  return new Promise((resolve) => {
    let timer = 0
    const finish = () => {
      window.clearTimeout(timer)
      connection.removeEventListener('icegatheringstatechange', onChange)
      resolve()
    }
    const onChange = () => {
      if (connection.iceGatheringState === 'complete') finish()
    }
    connection.addEventListener('icegatheringstatechange', onChange)
    timer = window.setTimeout(finish, timeoutMs)
  })
}

function retryLater(reason: string, delay: number): void {
  phase.value = 'error'
  message.value = reason
  clearRetry()
  retryTimer = window.setTimeout(() => {
    void start()
  }, delay)
}

async function start(): Promise<void> {
  const generation = ++attempt
  clearRetry()
  teardown()

  if (props.room.state !== 'LIVE') {
    phase.value = 'error'
    message.value = '推流已断开'
    return
  }

  phase.value = 'connecting'
  message.value = '正在建立连接…'

  try {
    const play = await fetchPlay(props.room.username)
    if (generation !== attempt) return

    if (!play.playback) {
      phase.value = 'error'
      message.value = '推流已断开'
      return
    }

    const connection = new RTCPeerConnection({ iceServers: [], bundlePolicy: 'max-bundle' })
    pc = connection

    connection.addTransceiver('video', { direction: 'recvonly' })
    connection.addTransceiver('audio', { direction: 'recvonly' })

    connection.ontrack = (event: RTCTrackEvent) => {
      const el = videoEl.value
      if (!el) return
      const stream = event.streams[0]
      if (stream && el.srcObject !== stream) {
        el.srcObject = stream
      }
      void el.play().catch(() => {
        // 自动播放被拦截时，由用户点击"播放"兜底
      })
    }

    connection.onconnectionstatechange = () => {
      if (connection !== pc) return
      const state = connection.connectionState
      if (state === 'connected') {
        phase.value = 'playing'
        message.value = ''
      } else if (state === 'failed') {
        retryLater('连接中断，正在重试…', 1500)
      } else if (state === 'disconnected') {
        message.value = '网络抖动，正在恢复…'
      }
    }

    const offer = await connection.createOffer()
    await connection.setLocalDescription(offer)
    await waitForIce(connection)
    if (generation !== attempt) {
      connection.close()
      return
    }

    const sdp = connection.localDescription?.sdp ?? offer.sdp ?? ''
    const res = await fetch(play.playback.whepUrl, {
      method: 'POST',
      headers: { 'Content-Type': 'application/sdp' },
      body: sdp,
    })
    if (generation !== attempt) return
    if (!res.ok) {
      retryLater('信令失败（HTTP ' + res.status + '），正在重试…', 2500)
      return
    }

    const answer = await res.text()
    await connection.setRemoteDescription({ type: 'answer', sdp: answer })
  } catch (err) {
    if (generation !== attempt) return
    const reason = err instanceof Error ? err.message : '播放失败'
    retryLater('连接失败：' + reason, 2500)
  }
}

function toggleMute(): void {
  const el = videoEl.value
  if (!el) return
  el.muted = !el.muted
  muted.value = el.muted
}

function manualRetry(): void {
  void start()
}

watch(() => [props.room.username, props.room.state] as const, () => void start(), { immediate: true })

onBeforeUnmount(() => {
  attempt += 1
  clearRetry()
  teardown()
})
</script>

<template>
  <div class="player">
    <video ref="videoEl" class="video" playsinline autoplay muted></video>

    <div v-if="phase !== 'playing'" class="overlay">
      <span v-if="phase === 'connecting'" class="spinner" aria-hidden="true"></span>
      <span class="overlay-text">{{ message }}</span>
      <button v-if="phase === 'error'" type="button" class="btn btn-ghost btn-sm" @click="manualRetry">重新连接</button>
    </div>

    <div class="controls">
      <span class="live-chip" aria-hidden="true">
        <span class="live-chip-dot"></span>
        LIVE
      </span>
      <span class="stream-url">{{ room.username }}</span>
      <button type="button" class="icon-btn" :title="muted ? '开启声音' : '静音'" @click="toggleMute">
        <template v-if="muted">🔇</template>
        <template v-else>🔊</template>
      </button>
    </div>
  </div>
</template>

<style scoped>
.player {
  position: relative;
  aspect-ratio: 16 / 9;
  width: 100%;
  border-radius: 16px;
  overflow: hidden;
  background: #05080d;
  border: 1px solid var(--line);
}

.video {
  width: 100%;
  height: 100%;
  object-fit: contain;
  background: #05080d;
  display: block;
}

.overlay {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 14px;
  background: radial-gradient(circle at center, rgba(15, 23, 42, 0.75), rgba(5, 8, 13, 0.92));
  color: var(--text-dim);
  font-size: 13.5px;
  text-align: center;
  padding: 20px;
}

.overlay-text {
  max-width: 80%;
  line-height: 1.6;
}

.spinner {
  width: 26px;
  height: 26px;
  border-radius: 50%;
  border: 2px solid rgba(255, 255, 255, 0.16);
  border-top-color: var(--accent-2);
  animation: spin 0.9s linear infinite;
}

.controls {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 14px;
  background: linear-gradient(180deg, transparent, rgba(3, 6, 11, 0.82));
  opacity: 0;
  transition: opacity 0.2s ease;
}

.player:hover .controls,
.player:focus-within .controls {
  opacity: 1;
}

.live-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 11px;
  letter-spacing: 0.08em;
  color: #fecdd3;
  background: rgba(244, 63, 94, 0.18);
  border: 1px solid rgba(244, 63, 94, 0.32);
  padding: 3px 8px;
  border-radius: 999px;
}

.live-chip-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #fb7185;
  animation: pulse 1.6s ease-in-out infinite;
}

.stream-url {
  font-family: var(--mono);
  font-size: 12px;
  color: var(--text-dim);
  flex: 1;
}

.icon-btn {
  border: 1px solid var(--line);
  background: rgba(13, 18, 25, 0.9);
  color: var(--text);
  width: 30px;
  height: 30px;
  border-radius: 9px;
  cursor: pointer;
  font-size: 13px;
  line-height: 1;
}

.btn-sm {
  padding: 6px 13px;
  font-size: 12.5px;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

@keyframes pulse {
  0%, 100% {
    opacity: 1;
  }
  50% {
    opacity: 0.4;
  }
}
</style>
