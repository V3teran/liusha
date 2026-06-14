<script setup lang="ts">
// 工具调用卡：折叠头（工具名）+ 展开看美化后的入参 JSON。按 agent 名着左边框 + chip 色，区分谁在调用。
import { computed, ref } from 'vue'
import { agentAccent } from '../../lib/agentColor'
const props = defineProps<{ tool: string; args: string; agentName?: string }>()
const open = ref(false)
const pretty = computed(() => {
  if (!props.args) return ''
  try {
    return JSON.stringify(JSON.parse(props.args), null, 2)
  } catch {
    return props.args
  }
})
const hasArgs = computed(() => !!pretty.value && pretty.value !== '{}')
const accent = computed(() => agentAccent(props.agentName)) // 每个 agent 独立色
</script>

<template>
  <div
    class="tool-call"
    :class="{ 'has-agent': !!agentName }"
    :style="{ '--ag': accent.accent, '--ag-soft': accent.soft }"
    data-card="tool-call"
  >
    <button class="head" :class="{ open }" @click="open = !open">
      <span class="caret">▸</span>
      <span class="dot" />
      <span class="label">调用</span>
      <code class="tool">{{ tool }}</code>
      <span v-if="agentName" class="agent">{{ agentName }}</span>
    </button>
    <pre v-if="open && hasArgs" class="args">{{ pretty }}</pre>
  </div>
</template>

<style scoped>
.tool-call { align-self: flex-start; max-width: 85%; }
.head {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 6px 12px;
  cursor: pointer;
  font-size: 13px;
  color: var(--text);
}
.head:hover { border-color: var(--accent); }
.caret { color: var(--muted); transition: transform 0.15s; font-size: 11px; }
.head.open .caret { transform: rotate(90deg); }
.dot { width: 6px; height: 6px; border-radius: 50%; background: var(--accent); }
.label { color: var(--muted); }
.tool { font-family: var(--mono); color: var(--accent); font-weight: 600; }
.agent {
  font-family: var(--mono);
  font-size: 10.5px;
  color: var(--muted);
  background: var(--surface-2);
  border-radius: 4px;
  padding: 0 6px;
}
/* 按 agent 名着色（--ag 由 inline style 注入）：左竖线 + chip 同色，每个 agent 一色 */
.tool-call.has-agent .head { border-left: 2px solid var(--ag); }
.tool-call.has-agent .agent { color: var(--ag); background: var(--ag-soft); }
.args {
  margin: 6px 0 0;
  background: var(--bg);
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  padding: 10px;
  font-size: 12px;
  max-height: 280px;
  overflow: auto;
}
</style>
