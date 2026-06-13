<script setup lang="ts">
// 推理卡：agent 的思路/分析/计划/决策（markdown 富文本）+ 本次 LLM 交互的 token/耗时元信息。
import { computed } from 'vue'
import { renderMarkdown } from '../../lib/markdown'

const props = defineProps<{
  text: string
  inTokens?: number
  outTokens?: number
  latencyMs?: number
}>()

const html = computed(() => renderMarkdown(props.text))
const fmtTok = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 'k' : String(n)) : '')
const fmtMs = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 's' : n + 'ms') : '')
const hasMeta = computed(() => (props.inTokens || 0) > 0 || (props.latencyMs || 0) > 0)
</script>

<template>
  <div class="reasoning-card" data-card="reasoning">
    <div class="rc-head">
      <span class="rc-icon">🧠</span>
      <span class="rc-label">推理</span>
      <span v-if="hasMeta" class="rc-meta">
        <span v-if="(inTokens || 0) > 0" class="rc-chip" title="输入 token">↑ {{ fmtTok(inTokens) }}</span>
        <span v-if="(outTokens || 0) > 0" class="rc-chip" title="输出 token">↓ {{ fmtTok(outTokens) }}</span>
        <span v-if="(latencyMs || 0) > 0" class="rc-chip" title="耗时">⏱ {{ fmtMs(latencyMs) }}</span>
      </span>
    </div>
    <div class="rc-body markdown-body" v-html="html" />
  </div>
</template>

<style scoped>
.reasoning-card {
  align-self: flex-start;
  max-width: 88%;
  background: var(--surface);
  border: 1px solid var(--border);
  border-left: 3px solid #722ed1; /* Ant purple，推理强调色 */
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow);
  padding: 10px 14px;
}
.rc-head {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-bottom: 4px;
}
.rc-icon { font-size: 13px; }
.rc-label { font-size: 12px; font-weight: 600; color: #722ed1; }
.rc-meta { margin-left: auto; display: flex; gap: 6px; }
.rc-chip {
  font-family: var(--mono);
  font-size: 11px;
  color: var(--muted);
  background: var(--surface-2);
  border-radius: 4px;
  padding: 1px 6px;
}
.rc-body { font-size: 14px; line-height: 1.6; color: var(--text); }
</style>
