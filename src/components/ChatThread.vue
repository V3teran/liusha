<script setup lang="ts">
// 对话主线：从 store 读有序消息，逐条交给 MessageItem 渲染。
import { useConversationStore } from '../stores/conversation'
import MessageItem from './MessageItem.vue'
const store = useConversationStore()
</script>
<template>
  <div class="thread">
    <MessageItem v-for="m in store.messages" :key="m.Seq" :msg="m" />
  </div>
</template>
