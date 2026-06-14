<script setup lang="ts">
import { computed } from 'vue'
import type { Message } from '../api/types'
import Avatar from './cards/Avatar.vue'
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

const isUser = computed(() => kind.value === 'user')
// 叙述类（user / assistant / 推理）带头像；过程类（工具/结果/派发/漏洞）缩进对齐、不重复头像。
const showAvatar = computed(() => ['user', 'assistant', 'reasoning'].includes(kind.value))
</script>

<template>
  <div class="msg-row" :class="{ mine: isUser }">
    <div class="avatar-slot">
      <Avatar v-if="showAvatar" :who="isUser ? 'user' : 'agent'" />
    </div>
    <div class="msg-content">
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
    </div>
  </div>
</template>

<style scoped>
.msg-row {
  display: flex;
  gap: 10px;
  align-items: flex-start;
}
.msg-row.mine {
  flex-direction: row-reverse;
}
.avatar-slot {
  width: 30px;
  flex-shrink: 0;
}
.msg-content {
  min-width: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
}
.msg-row.mine .msg-content {
  align-items: flex-end;
}
</style>
