<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { copyText } from '../lib/clipboard'

const props = defineProps<{ label: string; value: string; hint?: string }>()

const copied = ref(false)
const failed = ref(false)
let timer: number | null = null

async function copy(): Promise<void> {
  const ok = await copyText(props.value)
  copied.value = ok
  failed.value = !ok
  if (timer !== null) window.clearTimeout(timer)
  timer = window.setTimeout(() => {
    copied.value = false
    failed.value = false
  }, 1800)
}

onBeforeUnmount(() => {
  if (timer !== null) window.clearTimeout(timer)
})
</script>

<template>
  <div class="copy-field">
    <div class="copy-head">
      <span class="copy-label">{{ label }}</span>
      <span v-if="hint" class="copy-hint">{{ hint }}</span>
    </div>
    <div class="copy-body">
      <code class="copy-value">{{ value }}</code>
      <button type="button" class="btn btn-ghost btn-sm" @click="copy">
        {{ copied ? '已复制' : failed ? '请手动复制' : '复制' }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.copy-field {
  border: 1px solid var(--line);
  border-radius: 12px;
  background: var(--panel-2);
  padding: 10px 12px;
}

.copy-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 6px;
}

.copy-label {
  font-size: 12px;
  color: var(--text-dim);
}

.copy-hint {
  font-size: 11.5px;
  color: var(--text-faint);
}

.copy-body {
  display: flex;
  align-items: center;
  gap: 10px;
}

.copy-value {
  flex: 1;
  min-width: 0;
  font-family: var(--mono);
  font-size: 12.5px;
  color: var(--text);
  word-break: break-all;
  line-height: 1.5;
}

.btn-sm {
  padding: 5px 11px;
  font-size: 12.5px;
  flex: none;
}
</style>
