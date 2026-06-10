<script setup lang="ts">
// 根装配：鉴权门 → 侧栏 + 主线 + 发起器。
// 选中/发起对话时切流：关旧 SSE、清 store、补历史、订新流。
import { ref } from 'vue'
import { getApiKey, listMessages } from './api/client'
import { useConversationStore } from './stores/conversation'
import { openEventStream, type StreamHandle } from './composables/useEventStream'
import ApiKeyGate from './components/ApiKeyGate.vue'
import ConversationList from './components/ConversationList.vue'
import Composer from './components/Composer.vue'
import ChatThread from './components/ChatThread.vue'

const ready = ref(!!getApiKey())
const store = useConversationStore()
let handle: StreamHandle | null = null

async function open(convID: string) {
  handle?.close()
  store.reset()
  for (const m of await listMessages(convID)) store.ingest(m)
  handle = openEventStream(convID, store)
}
</script>
<template>
  <ApiKeyGate v-if="!ready" @ready="ready = true" />
  <div v-else class="app">
    <ConversationList @select="open" />
    <main>
      <ChatThread />
      <Composer @started="open" />
    </main>
  </div>
</template>
