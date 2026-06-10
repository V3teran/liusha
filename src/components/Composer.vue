<script setup lang="ts">
// 发起器：选角色 + 写 brief。
// 有 convId 时走追加（followUp），否则新建对话（startChat）并向上抛新对话 ID。
import { ref } from 'vue'
import { startChat, followUp } from '../api/client'
import RolePicker from './RolePicker.vue'
const props = defineProps<{ convId?: string }>()
const brief = ref('')
const roleID = ref('')
const busyMsg = ref('')
const emit = defineEmits<{ started: [convID: string]; appended: [] }>()
async function send() {
  if (!brief.value.trim()) return
  busyMsg.value = ''
  try {
    if (props.convId) {
      const r = await followUp(props.convId, brief.value)
      brief.value = ''
      busyMsg.value = r.intent === 'qa' ? '正在回答…' : '已触发扫描'
      emit('appended')
    } else {
      const { conversation_id } = await startChat(brief.value, roleID.value)
      brief.value = ''
      emit('started', conversation_id)
    }
  } catch (e) {
    const err = e as Error & { busy?: boolean }
    busyMsg.value = err.busy ? '扫描进行中，先点停止再发' : '发送失败'
  }
}
</script>
<template>
  <div class="composer">
    <RolePicker v-if="!convId" v-model="roleID" />
    <textarea
      v-model="brief"
      :placeholder="convId ? '追加指令（在同一目标上继续扫描）…' : '描述要扫的目标 / 任务…'"
      @keydown.meta.enter="send"
    />
    <button @click="send">{{ convId ? '追加' : '发起' }}</button>
    <span v-if="busyMsg" class="busy">{{ busyMsg }}</span>
  </div>
</template>
