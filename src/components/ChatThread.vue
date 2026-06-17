<script setup lang="ts">
// 对话主线：从 store 读有序消息逐条渲染，末尾挂流式推理活动气泡（逐字打字机）。
// 自动滚底：仅当用户本就贴在底部时，新消息/增量才把视图顶到最新——向上翻看历史时不打扰。
import { computed, nextTick, ref, watch } from 'vue'
import type { Message } from '../api/types'
import { useConversationStore } from '../stores/conversation'
import { dayKey, dayLabel } from '../lib/format'
import MessageItem from './MessageItem.vue'
import ReasoningCard from './cards/ReasoningCard.vue'

const store = useConversationStore()
const el = ref<HTMLElement>()

// 在消息流中按天插入分隔条（今天 / 昨天 / 日期）——跨天对话一眼可辨，内联卡片只显示时分秒。
type Row = { kind: 'divider'; key: string; label: string } | { kind: 'msg'; key: number; msg: Message }
const rows = computed<Row[]>(() => {
  const out: Row[] = []
  let lastDay = ''
  for (const m of store.messages) {
    const day = dayKey(m.CreatedAt)
    if (day && day !== lastDay) {
      out.push({ kind: 'divider', key: 'day-' + day, label: dayLabel(m.CreatedAt) })
      lastDay = day
    }
    out.push({ kind: 'msg', key: m.Seq, msg: m })
  }
  return out
})

function nearBottom() {
  const e = el.value
  if (!e) return true
  return e.scrollHeight - e.scrollTop - e.clientHeight < 120
}

async function stickToBottom() {
  const stick = nearBottom()
  await nextTick()
  if (stick && el.value) el.value.scrollTop = el.value.scrollHeight
}

// 消息增减 + 流式增量都触发自动滚底判定。
watch(() => store.messages.length, stickToBottom)
watch(() => store.liveReasoning, stickToBottom)
</script>

<template>
  <div ref="el" class="thread">
    <template v-for="r in rows" :key="r.key">
      <div v-if="r.kind === 'divider'" class="day-divider"><span>{{ r.label }}</span></div>
      <MessageItem v-else :msg="r.msg" />
    </template>
    <ReasoningCard v-if="store.liveReasoning" :text="store.liveReasoning" streaming />
  </div>
</template>

<style scoped>
.day-divider {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 6px 0;
  color: var(--muted);
  font-size: 11.5px;
}
.day-divider::before,
.day-divider::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--border);
}
.day-divider span {
  flex-shrink: 0;
  font-family: var(--mono);
  letter-spacing: 0.02em;
}
</style>
