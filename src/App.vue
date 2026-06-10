<script setup lang="ts">
// 根装配：鉴权门 → 侧栏 + 主线 + 发起器。
// 选中/发起对话时切流：关旧 SSE、清 store、补历史、订新流。
// 多轮：当前对话存 currentConv，传给 Composer 决定追加 vs 新建；提供新对话/停止扫描。
import { ref } from 'vue'
import { getApiKey, listMessages, abortScan } from './api/client'
import { useConversationStore } from './stores/conversation'
import { openEventStream, type StreamHandle } from './composables/useEventStream'
import ApiKeyGate from './components/ApiKeyGate.vue'
import ConversationList from './components/ConversationList.vue'
import Composer from './components/Composer.vue'
import ChatThread from './components/ChatThread.vue'

const ready = ref(!!getApiKey())
const store = useConversationStore()
const currentConv = ref<string>('')
let handle: StreamHandle | null = null

async function open(convID: string) {
  handle?.close()
  store.reset()
  currentConv.value = convID
  for (const m of await listMessages(convID)) store.ingest(m)
  handle = openEventStream(convID, store)
}
// 新对话：关流、清空、回到新建态。
function newConversation() {
  handle?.close()
  store.reset()
  currentConv.value = ''
}
// 停止当前对话关联的扫描。
async function stop() {
  if (currentConv.value) await abortScan(currentConv.value)
}
</script>
<template>
  <ApiKeyGate v-if="!ready" @ready="ready = true" />
  <div v-else class="app">
    <ConversationList @select="open" @new="newConversation" />
    <main>
      <div class="thread-head" v-if="currentConv">
        <button @click="stop">停止扫描</button>
      </div>
      <ChatThread />
      <Composer :conv-id="currentConv || undefined" @started="open" @appended="() => {}" />
    </main>
  </div>
</template>
