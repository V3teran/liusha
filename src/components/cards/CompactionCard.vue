<script setup lang="ts">
// 压缩卡：上下文压缩发生时显示（老 turn 蒸馏成 1 条摘要，防 context 爆）。
// 让用户对长对话的上下文裁剪有感知（对齐 Claude Code 的 compaction 可见），点击展开看蒸馏摘要。
import { ref } from 'vue'

defineProps<{
  label: string // 「压缩了 N 条历史消息」（后端 Result）
  summary: string // 蒸馏摘要正文（后端 Text）
}>()
const open = ref(false)
</script>

<template>
  <div class="compaction" data-card="compaction">
    <button class="head" :class="{ open }" @click="open = !open">
      <span class="caret">▸</span>
      <span class="icon">🗜</span>
      <span class="label">上下文已压缩{{ label ? ' · ' + label : '' }}</span>
      <span class="hint">{{ open ? '收起' : '看摘要' }}</span>
    </button>
    <div v-if="open && summary" class="body">{{ summary }}</div>
  </div>
</template>

<style scoped>
.compaction {
  align-self: center;
  max-width: 70%;
  margin: 4px 0;
}
.head {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  background: var(--surface-2);
  border: 1px dashed var(--border);
  border-radius: 999px;
  padding: 4px 14px;
  cursor: pointer;
  font-size: 12px;
  color: var(--muted);
}
.head:hover {
  border-color: var(--accent);
  color: var(--text);
}
.caret {
  font-size: 10px;
  transition: transform 0.15s;
}
.head.open .caret {
  transform: rotate(90deg);
}
.icon {
  font-size: 12px;
}
.label {
  flex: 1;
}
.hint {
  font-size: 11px;
  opacity: 0.7;
}
.body {
  margin-top: 6px;
  padding: 10px 14px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 10px;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--text);
  white-space: pre-wrap;
}
</style>
