<script setup lang="ts">
import { computed } from 'vue'
import type { Message } from '../api/types'
import UserBubble from './cards/UserBubble.vue'
import AssistantText from './cards/AssistantText.vue'
import ReasoningCard from './cards/ReasoningCard.vue'
import SpawnCard from './cards/SpawnCard.vue'
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
  if (ev.Kind === 'reasoning') return 'reasoning'
  if (ev.Kind === 'spawn') return 'spawn'
  if (ev.Kind === 'tool_call') return 'tool-call'
  if (ev.ToolName === 'write_finding' && !ev.Err) return 'finding'
  return 'tool-result'
})
</script>

<template>
  <UserBubble v-if="kind === 'user'" :content="msg.Content" />
  <AssistantText v-else-if="kind === 'assistant'" :content="msg.Content" />
  <ReasoningCard
    v-else-if="kind === 'reasoning'"
    :text="msg.Metadata!.Text || msg.Content"
    :in-tokens="msg.Metadata!.InTokens"
    :out-tokens="msg.Metadata!.OutTokens"
    :latency-ms="msg.Metadata!.LatencyMs"
  />
  <SpawnCard v-else-if="kind === 'spawn'" :args="msg.Metadata!.Args" />
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
