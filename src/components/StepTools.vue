<script setup lang="ts">
// 步内工具折叠：一个 reasoning(想) 之下的若干工具调用默认收起成一行按钮，点击展开。
// 减少对话流噪音——工具调用细节按需查看；派发/漏洞/想 不进这里（由 ChatThread 留在外面）。
import { ref, computed } from 'vue'
import type { Message } from '../api/types'
import MessageItem from './MessageItem.vue'

const props = defineProps<{ tools: Message[] }>()
const expanded = ref(false)

// 工具名摘要（去重，最多 4 个）——折叠态给个内容预览，不必展开就知大概干了啥。
const preview = computed(() => {
  const names = props.tools
    .map((t) => t.Metadata?.ToolName)
    .filter((n): n is string => !!n)
  const uniq = [...new Set(names)]
  const head = uniq.slice(0, 4).join('、')
  return uniq.length > 4 ? `${head} 等` : head
})
</script>

<template>
  <div class="step-tools">
    <button class="st-toggle" :class="{ open: expanded }" @click="expanded = !expanded">
      <span class="st-caret">{{ expanded ? '▾' : '▸' }}</span>
      <span class="st-icon">⚙</span>
      <span class="st-count">{{ tools.length }} 个工具调用</span>
      <span v-if="!expanded && preview" class="st-preview">{{ preview }}</span>
    </button>
    <div v-if="expanded" class="st-body">
      <MessageItem v-for="t in tools" :key="t.Seq" :msg="t" />
    </div>
  </div>
</template>

<style scoped>
.step-tools {
  align-self: flex-start;
  max-width: 88%;
  margin-left: 40px; /* 与 reasoning 卡左对齐缩进，视觉归属其下 */
}
.st-toggle {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 11.5px;
  color: var(--muted);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 3px 10px;
  cursor: pointer;
  transition: background var(--duration-fast, 150ms);
}
.st-toggle:hover {
  background: var(--surface);
  color: var(--text);
}
.st-caret {
  font-size: 10px;
  width: 9px;
}
.st-icon {
  font-size: 11px;
}
.st-count {
  font-weight: 600;
}
.st-preview {
  font-family: var(--mono);
  opacity: 0.7;
  margin-left: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 320px;
}
.st-body {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: 6px;
}
</style>
