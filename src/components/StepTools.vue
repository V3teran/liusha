<script setup lang="ts">
// 步内工具折叠：一个 reasoning(想) 之下的若干工具调用默认收起成一行按钮，点击展开。
// 减少对话流噪音——工具调用细节按需查看；派发/漏洞/想 不进这里（由 ChatThread 留在外面）。
import { ref, computed } from 'vue'
import type { Message } from '../api/types'
import MessageItem from './MessageItem.vue'

const props = defineProps<{ tools: Message[] }>()
const expanded = ref(false)

// 工具调用次数：一次调用产生 tool_call + tool_result 两条消息，计数只数 tool_call（发起）——
// 否则一来一回会翻倍（38 次显示成 76）。全是孤立 result（call 丢失）时回退总条数兜底。
const callCount = computed(() => {
  const calls = props.tools.filter((t) => t.Metadata?.Kind === 'tool_call').length
  return calls > 0 ? calls : props.tools.length
})

// 工具预览：按类别聚合「次数」——计数用「次」(callCount 总次数)反映工作量，预览用
// 「类别 ×次数」(run_command ×5、browser_use ×2)反映干了啥+各几次。只数 tool_call(发起)，不重复算结果。
const preview = computed(() => {
  const counts: Record<string, number> = {}
  for (const t of props.tools) {
    if (t.Metadata?.Kind !== 'tool_call') continue
    const name = t.Metadata?.ToolName
    if (name) counts[name] = (counts[name] ?? 0) + 1
  }
  const entries = Object.entries(counts).sort((a, b) => b[1] - a[1])
  const head = entries
    .slice(0, 4)
    .map(([n, c]) => (c > 1 ? `${n} ×${c}` : n))
    .join('、')
  return entries.length > 4 ? `${head} 等` : head
})
</script>

<template>
  <div class="step-tools">
    <button class="st-toggle" :class="{ open: expanded }" @click="expanded = !expanded">
      <span class="st-caret">{{ expanded ? '▾' : '▸' }}</span>
      <span class="st-count">{{ callCount }} 次工具调用</span>
      <span v-if="!expanded && preview" class="st-preview">· {{ preview }}</span>
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
