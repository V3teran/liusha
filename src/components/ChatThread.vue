<script setup lang="ts">
// 对话主线：从 store 读有序消息逐条渲染，末尾挂流式推理活动气泡（逐字打字机）。
// 自动滚底：仅当用户本就贴在底部时，新消息/增量才把视图顶到最新——向上翻看历史时不打扰。
import { nextTick, ref, watch } from 'vue'
import { useConversationStore } from '../stores/conversation'
import MessageItem from './MessageItem.vue'
import ReasoningCard from './cards/ReasoningCard.vue'

const store = useConversationStore()
const el = ref<HTMLElement>()

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
    <MessageItem v-for="m in store.messages" :key="m.Seq" :msg="m" />
    <ReasoningCard v-if="store.liveReasoning" :text="store.liveReasoning" streaming />
  </div>
</template>
