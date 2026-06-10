<script setup lang="ts">
// 发起器：选角色 + 写 brief，提交后向上抛出新对话 ID。
import { ref } from 'vue'
import { startChat } from '../api/client'
import RolePicker from './RolePicker.vue'
const brief = ref('')
const roleID = ref('')
const emit = defineEmits<{ started: [convID: string] }>()
async function send() {
  if (!brief.value.trim()) return
  const { conversation_id } = await startChat(brief.value, roleID.value)
  brief.value = ''
  emit('started', conversation_id)
}
</script>
<template>
  <div class="composer">
    <RolePicker v-model="roleID" />
    <textarea v-model="brief" placeholder="描述要扫的目标 / 任务…" @keydown.meta.enter="send" />
    <button @click="send">发起</button>
  </div>
</template>
