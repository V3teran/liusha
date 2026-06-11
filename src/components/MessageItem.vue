<script setup lang="ts">
import { computed } from 'vue'
import type { Message } from '../api/types'
import UserBubble from './cards/UserBubble.vue'
import AssistantText from './cards/AssistantText.vue'
import ToolCallCard from './cards/ToolCallCard.vue'
import ToolResultCard from './cards/ToolResultCard.vue'
import FindingCard from './cards/FindingCard.vue'

const props = defineProps<{ msg: Message }>()

// 卡片类型判定：普通消息看 Role；事件看 Metadata.Kind；write_finding 结果走 FindingCard。
const kind = computed(() => {
  const m = props.msg
  if (m.Kind === 'message') return m.Role === 'user' ? 'user' : 'assistant'
  const ev = m.Metadata
  if (!ev) return 'assistant'
  if (ev.Kind === 'tool_call') return 'tool-call'
  if (ev.ToolName === 'write_finding' && !ev.Err) return 'finding'
  return 'tool-result'
})
</script>
<template>
  <UserBubble v-if="kind === 'user'" :content="msg.Content" />
  <AssistantText v-else-if="kind === 'assistant'" :content="msg.Content" />
  <ToolCallCard v-else-if="kind === 'tool-call'" :tool="msg.Metadata!.ToolName" :args="msg.Metadata!.Args" />
  <FindingCard v-else-if="kind === 'finding'" :args="msg.Metadata!.Args" />
  <ToolResultCard
    v-else
    :tool="msg.Metadata!.ToolName"
    :result="msg.Metadata!.Result"
    :duration-ms="msg.Metadata!.DurationMs"
    :err="msg.Metadata!.Err"
  />
</template>
