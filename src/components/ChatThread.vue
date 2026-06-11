<script setup lang="ts">
// 对话主线：从 store 读有序消息逐条渲染。
// 自动滚底：仅当用户本就贴在底部时，新消息才把视图顶到最新——向上翻看历史时不打扰。
import { nextTick, ref, watch } from 'vue'
import { useConversationStore } from '../stores/conversation'
import MessageItem from './MessageItem.vue'

const store = useConversationStore()
const el = ref<HTMLElement>()

function nearBottom() {
  const e = el.value
  if (!e) return true
  return e.scrollHeight - e.scrollTop - e.clientHeight < 120
}

watch(
  () => store.messages.length,
  async () => {
    const stick = nearBottom()
    await nextTick()
    if (stick && el.value) el.value.scrollTop = el.value.scrollHeight
  }
)
</script>

<template>
  <div ref="el" class="thread">
    <MessageItem v-for="m in store.messages" :key="m.Seq" :msg="m" />
  </div>
</template>
