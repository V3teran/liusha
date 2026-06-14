<script setup lang="ts">
// 推理卡：agent 的思路/分析/计划/决策（markdown 富文本）+ 本次 LLM 交互的 token/耗时元信息。
import { computed } from 'vue'
import { renderMarkdown } from '../../lib/markdown'

const props = defineProps<{
  text: string
  agentName?: string // 产出该推理的 agent（orchestrator/exploitation/reconnaissance）
  inTokens?: number
  outTokens?: number
  latencyMs?: number
  streaming?: boolean // 流式活动气泡：显示「推理中」+ 闪烁光标，token/耗时 chip 待最终帧
}>()

const html = computed(() => renderMarkdown(props.text))
const fmtTok = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 'k' : String(n)) : '')
const fmtMs = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 's' : n + 'ms') : '')
const hasMeta = computed(() => (props.inTokens || 0) > 0 || (props.latencyMs || 0) > 0)

// agent 名 → 中文角色标签（区分指挥官/侦察/利用，让用户看清是哪个子代理在推理）。
const AGENT_LABELS: Record<string, string> = {
  orchestrator: '指挥官',
  exploitation: '利用',
  reconnaissance: '侦察',
  'traffic-analysis': '流量分析',
}
const agentLabel = computed(() => {
  const n = props.agentName?.trim()
  if (!n) return ''
  return AGENT_LABELS[n] || n
})
</script>

<template>
  <div class="reasoning-card" data-card="reasoning">
    <div class="rc-head">
      <span class="rc-icon">🧠</span>
      <span class="rc-label">{{ streaming ? '推理中' : '推理' }}</span>
      <span v-if="agentLabel" class="rc-agent">{{ agentLabel }}</span>
      <span v-if="hasMeta" class="rc-meta">
        <span v-if="(inTokens || 0) > 0" class="rc-chip" title="输入 token">↑ {{ fmtTok(inTokens) }}</span>
        <span v-if="(outTokens || 0) > 0" class="rc-chip" title="输出 token">↓ {{ fmtTok(outTokens) }}</span>
        <span v-if="(latencyMs || 0) > 0" class="rc-chip" title="耗时">⏱ {{ fmtMs(latencyMs) }}</span>
      </span>
    </div>
    <div class="rc-body markdown-body" v-html="html" /><span v-if="streaming" class="rc-cursor" />
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
.rc-agent {
  font-size: 11px;
  font-weight: 600;
  color: #722ed1;
  background: rgba(114, 46, 209, 0.14);
  border-radius: 4px;
  padding: 1px 7px;
}
.rc-meta { margin-left: auto; display: flex; gap: 6px; }
.rc-chip {
  font-family: var(--mono);
  font-size: 11px;
  color: var(--muted);
  background: var(--surface-2);
  border-radius: 4px;
  padding: 1px 6px;
}
.rc-body { font-size: 14px; line-height: 1.6; color: var(--text); display: inline; }
.rc-cursor {
  display: inline-block;
  width: 7px;
  height: 14px;
  margin-left: 2px;
  vertical-align: text-bottom;
  background: #722ed1;
  border-radius: 1px;
  animation: rc-blink 1s steps(2, start) infinite;
}
@keyframes rc-blink {
  to { visibility: hidden; }
}
</style>
