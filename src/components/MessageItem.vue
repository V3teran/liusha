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

// 卡片类型判定：普通消息看 Role；事件看 Metadata.Kind。
// write_finding 一次产生两条事件：tool_call（带 Args=漏洞详情）+ tool_result（仅 {id}，无展示价值）。
// → tool_call 渲染成 finding 卡（有 Args）；tool_result 隐藏（否则渲染成空的「INFO(无标题)」卡）。
const kind = computed(() => {
  const m = props.msg
  if (m.Kind === 'message') return m.Role === 'user' ? 'user' : 'assistant'
  const ev = m.Metadata
  if (!ev) return 'assistant'
  if (ev.Kind === 'reasoning') return 'reasoning'
  if (ev.Kind === 'spawn') return 'spawn'
  if (ev.ToolName === 'write_finding') {
    return ev.Kind === 'tool_call' && !ev.Err ? 'finding' : 'hidden'
  }
  // task 的 tool_result = 派发完成（带子代理执行总时长）→ 渲染成 spawn 完成卡，而非普通工具卡。
  if (ev.ToolName === 'task' && ev.Kind === 'tool_result') return 'spawn-done'
  if (ev.Kind === 'tool_call') return 'tool-call'
  return 'tool-result'
})

const isUser = computed(() => kind.value === 'user')
// 叙述类（user / assistant / 推理）带头像；过程类（工具/结果/派发/漏洞）缩进对齐、不重复头像。
const showAvatar = computed(() => ['user', 'assistant', 'reasoning'].includes(kind.value))
</script>

<template>
  <div v-if="kind !== 'hidden'" class="msg-row" :class="{ mine: isUser }">
    <div class="avatar-slot">
      <Avatar v-if="showAvatar" :who="isUser ? 'user' : 'agent'" />
    </div>
    <div class="msg-content">
      <UserBubble v-if="kind === 'user'" :content="msg.Content" />
      <AssistantText v-else-if="kind === 'assistant'" :content="msg.Content" />
      <ReasoningCard
        v-else-if="kind === 'reasoning'"
        :text="msg.Metadata!.Text || msg.Content"
        :agent-name="msg.Metadata!.AgentName"
        :in-tokens="msg.Metadata!.InTokens"
        :out-tokens="msg.Metadata!.OutTokens"
        :latency-ms="msg.Metadata!.LatencyMs"
      />
      <SpawnCard v-else-if="kind === 'spawn'" :args="msg.Metadata!.Args" />
      <SpawnCard
        v-else-if="kind === 'spawn-done'"
        done
        :duration-ms="msg.Metadata!.DurationMs"
        :err="msg.Metadata!.Err"
      />
      <ToolCallCard
        v-else-if="kind === 'tool-call'"
        :tool="msg.Metadata!.ToolName"
        :args="msg.Metadata!.Args"
        :agent-name="msg.Metadata!.AgentName"
      />
      <FindingCard v-else-if="kind === 'finding'" :args="msg.Metadata!.Args" />
      <ToolResultCard
        v-else
        :tool="msg.Metadata!.ToolName"
        :result="msg.Metadata!.Result"
        :duration-ms="msg.Metadata!.DurationMs"
        :err="msg.Metadata!.Err"
        :agent-name="msg.Metadata!.AgentName"
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
