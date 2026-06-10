<script setup lang="ts">
// 对话侧栏：挂载时拉取对话列表，点击向上抛出选中 ID；暴露 refresh 供外部刷新。
import { onMounted, ref } from 'vue'
import { listConversations } from '../api/client'
import type { Conversation } from '../api/types'
const items = ref<Conversation[]>([])
const emit = defineEmits<{ select: [convID: string] }>()
async function refresh() {
  items.value = await listConversations()
}
onMounted(refresh)
defineExpose({ refresh })
</script>
<template>
  <aside class="conv-list">
    <button class="refresh" @click="refresh">↻</button>
    <ul>
      <li v-for="c in items" :key="c.ID" @click="emit('select', c.ID)">
        {{ c.Title || c.ID.slice(0, 8) }}
      </li>
    </ul>
  </aside>
</template>
