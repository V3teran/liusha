<script setup lang="ts">
// 派发卡：orchestrator 派子代理（AI 指挥 AI 团队）。两个时刻：
//   - 派发开始（args）：🛰️ 派发 → reconnaissance + brief
//   - 派发完成（done + durationMs）：✓/✗ + 子代理执行总时长（task 工具的 tool_result）
import { computed } from 'vue'
import { agentAccent, agentLabel } from '../../lib/agentColor'

const props = defineProps<{
  args?: string
  durationMs?: number // 完成时：子代理执行总耗时
  err?: string // 完成时：子代理出错信息
  done?: boolean // true=派发完成卡
}>()

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
const agentColor = computed(() => agentAccent(parsed.value.subagent_type))
const agentText = computed(() => agentLabel(parsed.value.subagent_type) || '子代理')
const brief = computed(() => (parsed.value.description || '').trim())
const fmtMs = (n?: number) => (n && n > 0 ? (n >= 1000 ? (n / 1000).toFixed(1) + 's' : n + 'ms') : '')
</script>

<template>
  <div class="spawn-card" :class="{ done, fail: !!err }" data-card="spawn">
    <div class="sp-head">
      <template v-if="done">
        <span class="sp-icon">{{ err ? '✗' : '✓' }}</span>
        <span class="sp-label">派发{{ err ? '失败' : '完成' }}</span>
        <span v-if="durationMs" class="sp-dur" title="子代理执行总耗时">⏱ {{ fmtMs(durationMs) }}</span>
      </template>
      <template v-else>
        <span class="sp-icon">🛰️</span>
        <span class="sp-label">派发</span>
        <span class="sp-arrow">→</span>
        <span class="sp-agent" :style="{ color: agentColor.accent, background: agentColor.soft }">{{ agentText }}</span>
      </template>
    </div>
    <div v-if="!done && brief" class="sp-brief">{{ brief }}</div>
    <div v-if="done && err" class="sp-brief sp-fail">{{ err }}</div>
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
.sp-dur {
  margin-left: auto;
  font-family: var(--mono);
  font-size: 11px;
  color: var(--muted);
  background: var(--surface-2);
  border-radius: 4px;
  padding: 1px 7px;
}
.sp-arrow { color: var(--muted); }
/* 派发完成卡：成功绿/失败红的图标，弱化派发卡的紫背景渐变（已是结果不是动作） */
.spawn-card.done { background: var(--surface); }
.spawn-card.done .sp-icon { color: var(--success); }
.spawn-card.fail .sp-icon { color: var(--error); }
.spawn-card.fail { border-left-color: var(--error); }
.sp-fail { color: var(--error); }
.sp-agent {
  /* color/background 由 inline :style 注入（agentAccent 按被派子代理着色：青=侦察/玫红=利用…） */
  font-family: var(--mono);
  font-weight: 600;
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
