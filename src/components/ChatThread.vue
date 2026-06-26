<script setup lang="ts">
// 对话主线：从 store 读有序消息逐条渲染，末尾挂流式推理活动气泡（逐字打字机）。
// 自动滚底：仅当用户本就贴在底部时，新消息/增量才把视图顶到最新——向上翻看历史时不打扰。
import { computed, nextTick, ref, watch } from 'vue'
import type { Message } from '../api/types'
import { useConversationStore } from '../stores/conversation'
import { dayKey, dayLabel } from '../lib/format'
import { classifyMessage, isCollapsibleTool } from '../lib/messageKind'
import MessageItem from './MessageItem.vue'
import StepTools from './StepTools.vue'
import ReasoningCard from './cards/ReasoningCard.vue'

const store = useConversationStore()
const el = ref<HTMLElement>()

// 在消息流中按天插入分隔条（今天 / 昨天 / 日期）——跨天对话一眼可辨，内联卡片只显示时分秒。
// step：本次「用户指令」内的全局推理步号——每条 reasoning(想) 递增一步，跨所有 agent 统一计数
// （不按 agent 分组：一个 type 如 exploitation 会被 spawn 多个并发实例，按 type 累计会混淆、
//  按实例又无标识可分；全局序号无歧义）。**每条用户消息重置**：一次指令(发起→结束)是一个计数
// 周期，追加(follow-up)算新指令、步号从头。配合卡片已有的 agent 标签（编排/侦察/利用）定位「谁的第几步」。
// 按步分组渲染：reasoning(想)/spawn(派发)/finding(漏洞)/对话 留在外面独立成卡；
// 紧随某步的普通工具调用(tool-call/tool-result)累积成一个折叠组（StepTools），默认收起、点击展开——
// 减少噪音。'tools' row 即一段连续工具，遇到非工具消息(或换天)就 flush 收尾。
type Row =
  | { kind: 'divider'; key: string; label: string }
  | { kind: 'msg'; key: number; msg: Message; step?: number }
  | { kind: 'tools'; key: string; tools: Message[] }
const rows = computed<Row[]>(() => {
  const out: Row[] = []
  let lastDay = ''
  let step = 0
  let bucket: Message[] = [] // 累积的连续工具调用，遇非工具消息时 flush
  const flush = () => {
    if (bucket.length) {
      out.push({ kind: 'tools', key: 'tools-' + bucket[0].Seq, tools: bucket })
      bucket = []
    }
  }
  for (const m of store.messages) {
    const tag = classifyMessage(m)
    if (tag === 'hidden') continue
    // 普通工具调用 → 进折叠桶，不单独成行。
    if (isCollapsibleTool(tag)) {
      bucket.push(m)
      continue
    }
    flush() // 非工具消息：先收尾当前工具组
    // 用户指令边界：每条用户消息重置步号（追加 = 新指令 = 新计数周期）。
    if (m.Role === 'user') step = 0
    const day = dayKey(m.CreatedAt)
    if (day && day !== lastDay) {
      out.push({ kind: 'divider', key: 'day-' + day, label: dayLabel(m.CreatedAt) })
      lastDay = day
    }
    let stepNo: number | undefined
    if (tag === 'reasoning') {
      step += 1
      stepNo = step
    }
    out.push({ kind: 'msg', key: m.Seq, msg: m, step: stepNo })
  }
  flush() // 末尾残留工具组
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
      <StepTools v-else-if="r.kind === 'tools'" :tools="r.tools" />
      <MessageItem v-else :msg="r.msg" :step="r.step" />
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
