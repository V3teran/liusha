<script setup lang="ts">
// 派发卡：orchestrator 派子代理（AI 指挥 AI 团队）。从 task 工具入参解析 subagent_type + description。
import { computed } from 'vue'

const props = defineProps<{ args: string }>()

interface SpawnArgs {
  subagent_type?: string
  description?: string
}
const parsed = computed<SpawnArgs>(() => {
  try {
    return JSON.parse(props.args || '{}')
  } catch {
    return {}
  }
})
const agent = computed(() => parsed.value.subagent_type || '子代理')
const brief = computed(() => (parsed.value.description || '').trim())
</script>

<template>
  <div class="spawn-card" data-card="spawn">
    <div class="sp-head">
      <span class="sp-icon">🛰️</span>
      <span class="sp-label">派发</span>
      <span class="sp-arrow">→</span>
      <span class="sp-agent">{{ agent }}</span>
    </div>
    <div v-if="brief" class="sp-brief">{{ brief }}</div>
  </div>
</template>

<style scoped>
/* 派发卡：紫=orchestrator（派活的主体）边框/标题；青=被派的目标子代理 chip。语义双色一目了然。 */
.spawn-card {
  align-self: flex-start;
  max-width: 88%;
  background: linear-gradient(180deg, rgba(114, 46, 209, 0.1), transparent), var(--surface);
  border: 1px solid var(--border);
  border-left: 3px solid #722ed1;
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow);
  padding: 10px 14px;
}
.sp-head {
  display: flex;
  align-items: center;
  gap: 7px;
  font-size: 13px;
}
.sp-icon { font-size: 14px; }
.sp-label { font-size: 12px; font-weight: 600; color: #722ed1; }
.sp-arrow { color: var(--muted); }
.sp-agent {
  font-family: var(--mono);
  font-weight: 600;
  color: #13a8a8;
  background: rgba(19, 168, 168, 0.14);
  border-radius: 5px;
  padding: 1px 8px;
}
.sp-brief {
  margin-top: 6px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--text);
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
