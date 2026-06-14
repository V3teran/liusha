<script setup lang="ts">
// 工具调用卡：折叠头（工具名）+ 展开看美化后的入参 JSON。子代理工具加青色标识区分主/子。
import { computed, ref } from 'vue'
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
// agent 色贯穿主/子：orchestrator（主）紫、子代理青，左边框 + chip 同色。
const isMain = computed(() => props.agentName === 'orchestrator')
const isSub = computed(() => !!props.agentName && props.agentName !== 'orchestrator')
</script>

<template>
  <div class="tool-call" :class="{ 'is-main': isMain, 'is-sub': isSub }" data-card="tool-call">
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
/* agent 色贯穿：主(orchestrator)紫 / 子代理青，左竖线 + chip 同色 */
.tool-call.is-main .head { border-left: 2px solid #722ed1; }
.tool-call.is-main .agent { color: #722ed1; background: rgba(114, 46, 209, 0.14); }
.tool-call.is-sub .head { border-left: 2px solid #13a8a8; }
.tool-call.is-sub .agent { color: #13a8a8; background: rgba(19, 168, 168, 0.14); }
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
