<script setup lang="ts">
import { computed } from 'vue'
import type { Message } from '../api/types'
import { classifyMessage } from '../lib/messageKind'
import { clockTime, fullTime } from '../lib/format'
import Avatar from './cards/Avatar.vue'
import UserBubble from './cards/UserBubble.vue'
import AssistantText from './cards/AssistantText.vue'
import ReasoningCard from './cards/ReasoningCard.vue'
import SpawnCard from './cards/SpawnCard.vue'
import ToolCallCard from './cards/ToolCallCard.vue'
import ToolResultCard from './cards/ToolResultCard.vue'
import FindingCard from './cards/FindingCard.vue'
import CompactionCard from './cards/CompactionCard.vue'

const props = defineProps<{ msg: Message; step?: number }>()

// 卡片类型判定走共享分类器（lib/messageKind，与 ChatThread 分组逻辑同源）。
const kind = computed(() => classifyMessage(props.msg))

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
        :step="step"
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
      <CompactionCard
        v-else-if="kind === 'compaction'"
        :label="msg.Metadata!.Result"
        :summary="msg.Metadata!.Text"
      />
      <ToolResultCard
        v-else
        :tool="msg.Metadata!.ToolName"
        :result="msg.Metadata!.Result"
        :duration-ms="msg.Metadata!.DurationMs"
        :err="msg.Metadata!.Err"
        :agent-name="msg.Metadata!.AgentName"
      />
      <time
        v-if="msg.CreatedAt"
        class="msg-time"
        :datetime="msg.CreatedAt"
        :title="fullTime(msg.CreatedAt)"
      >
        {{ clockTime(msg.CreatedAt) }}
      </time>
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
.msg-time {
  margin-top: 3px;
  font-size: 11px;
  line-height: 1;
  color: var(--muted);
  font-family: var(--mono);
  opacity: 0.55;
  cursor: default;
}
</style>
