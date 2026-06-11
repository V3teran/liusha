<script setup lang="ts">
// 工具调用卡：折叠头（工具名）+ 展开看美化后的入参 JSON。
import { computed, ref } from 'vue'
const props = defineProps<{ tool: string; args: string }>()
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
</script>

<template>
  <div class="tool-call" data-card="tool-call">
    <button class="head" :class="{ open }" @click="open = !open">
      <span class="caret">▸</span>
      <span class="dot" />
      <span class="label">调用</span>
      <code class="tool">{{ tool }}</code>
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
