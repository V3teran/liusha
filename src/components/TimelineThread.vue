<script setup lang="ts">
// active 作战轨迹：把 agent 推理流渲染成垂直时间轴脊柱，而非聊天气泡。
// 专为「观测 AI 自主作战」设计——每步是轨迹节点（想/工具/派发/漏洞），子代理（非 orchestrator）
// 缩进 + 该 agent 配色竖条，一眼看出「编排在派活、侦察/利用在并行干活」。工具默认折叠。
// 复用 lib/threadRows 分组 + 现有卡片（MessageItem/StepTools/ReasoningCard），仅换外层布局。
import { computed, nextTick, ref, watch } from 'vue'
import type { Message } from '../api/types'
import { useConversationStore } from '../stores/conversation'
import { dayKey, dayLabel } from '../lib/format'
import { buildThreadRows, type ThreadRow } from '../lib/threadRows'
import { classifyMessage } from '../lib/messageKind'
import { agentAccent, agentLabel } from '../lib/agentColor'
import MessageItem from './MessageItem.vue'
import StepTools from './StepTools.vue'
import ReasoningCard from './cards/ReasoningCard.vue'
import { useTypewriter } from '../composables/useTypewriter'

const store = useConversationStore()
const el = ref<HTMLElement>()
const typedReasoning = useTypewriter(computed(() => store.liveReasoning))

const rows = computed(() => buildThreadRows(store.messages, { dayKey, dayLabel }))

// 每行的 agent 归属：驱动缩进（非 orchestrator = 子代理，缩进）+ 脊柱节点配色。
// reasoning/tool 取 Metadata.AgentName；tools 组取首条 tool 的 agent；其余（user/finding/派发）归 root。
function rowAgent(r: ThreadRow): string {
  if (r.kind === 'tools') return r.tools[0]?.Metadata?.AgentName || ''
  if (r.kind === 'msg') {
    const tag = classifyMessage(r.msg)
    if (tag === 'reasoning' || tag === 'tool-call' || tag === 'tool-result') {
      return r.msg.Metadata?.AgentName || ''
    }
  }
  return ''
}
// 子代理缩进：orchestrator 与无归属（root）不缩进；侦察/利用等子代理缩进 + 配色竖条。
function isSubAgent(agent: string): boolean {
  return !!agent && agent !== 'orchestrator'
}
// 用户消息/漏洞这类「里程碑」行不挂脊柱节点色（保持中性），只有 agent 步挂色。
function nodeAccent(r: ThreadRow) {
  return agentAccent(rowAgent(r)).accent
}
function subAgentLabel(agent: string): string {
  return agentLabel(agent)
}

// 流式活动节点的 agent 归属（驱动缩进 + 节点色，与落定行一致）。
const liveIsSub = computed(() => isSubAgent(store.liveAgentName))
const liveAccent = computed(() => agentAccent(store.liveAgentName).accent)

// —— 自动滚底（与 ChatThread 同款）：贴底才跟随，翻看历史不打扰。 ——
function nearBottom() {
  const e = el.value
  if (!e) return true
  return e.scrollHeight - e.scrollTop - e.clientHeight < 120
}
const atBottom = ref(true)
const unread = ref(0)
function onScroll() {
  const wasBottom = atBottom.value
  atBottom.value = nearBottom()
  if (atBottom.value && !wasBottom) unread.value = 0
}
function scrollToBottom() {
  if (el.value) el.value.scrollTop = el.value.scrollHeight
  unread.value = 0
}
async function stickToBottom() {
  const stick = nearBottom()
  await nextTick()
  if (stick && el.value) el.value.scrollTop = el.value.scrollHeight
}
watch(
  () => store.messages.length,
  (n, prev) => {
    if (nearBottom()) stickToBottom()
    else if (n > (prev ?? 0)) unread.value += n - (prev ?? 0)
  },
)
watch(typedReasoning, stickToBottom)
</script>

<template>
  <div class="tl-wrap">
    <div
      ref="el"
      class="tl"
      role="log"
      aria-live="polite"
      aria-relevant="additions"
      aria-label="作战轨迹"
      @scroll.passive="onScroll"
    >
      <template v-for="r in rows" :key="r.key">
        <!-- 换天分隔条：跨脊柱横贯，不挂节点 -->
        <div v-if="r.kind === 'divider'" class="tl-divider"><span>{{ r.label }}</span></div>
        <!-- 轨迹行：左脊柱节点（agent 配色）+ 右内容；子代理整行缩进 + 配色竖条 -->
        <div
          v-else
          class="tl-row"
          :class="{ sub: isSubAgent(rowAgent(r)) }"
          :style="{ '--tl-accent': nodeAccent(r) }"
        >
          <div class="tl-rail">
            <span class="tl-node" />
          </div>
          <div class="tl-content">
            <!-- 子代理起始标签：缩进行顶部标一次「谁在干」（侦察/利用），色带已表达归属 -->
            <span v-if="isSubAgent(rowAgent(r))" class="tl-agent-tag">{{ subAgentLabel(rowAgent(r)) }}</span>
            <StepTools v-if="r.kind === 'tools'" :tools="r.tools" />
            <MessageItem v-else :msg="r.msg" :step="r.step" />
          </div>
        </div>
      </template>

      <!-- 流式活动节点：与落定行同布局（脊柱节点 + 缩进），逐字打字机揭示 -->
      <div
        v-if="store.liveReasoning"
        class="tl-row"
        :class="{ sub: liveIsSub }"
        :style="{ '--tl-accent': liveAccent }"
        aria-hidden="true"
      >
        <div class="tl-rail"><span class="tl-node live" /></div>
        <div class="tl-content">
          <span v-if="liveIsSub" class="tl-agent-tag">{{ subAgentLabel(store.liveAgentName) }}</span>
          <ReasoningCard :text="typedReasoning" :agent-name="store.liveAgentName || undefined" streaming />
        </div>
      </div>
    </div>

    <button v-if="!atBottom" class="jump-latest" type="button" @click="scrollToBottom">
      <span v-if="unread > 0" class="jl-count">{{ unread > 99 ? '99+' : unread }} 条新动态</span>
      <span v-else class="jl-count">回到最新</span>
      <span class="jl-arrow">↓</span>
    </button>
  </div>
</template>

<style scoped>
.tl-wrap {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.tl {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 16px 16px 16px 8px;
}
/* 轨迹行：左脊柱轨（含节点圆点）+ 右内容。脊柱靠连续行的 rail 竖线拼成一条时间轴。 */
.tl-row {
  display: flex;
  gap: 12px;
  align-items: stretch;
}
.tl-rail {
  position: relative;
  width: 18px;
  flex-shrink: 0;
  display: flex;
  justify-content: center;
}
/* 脊柱竖线：贯穿每行 rail 的中轴，行与行首尾相接成连续轴线。 */
.tl-rail::before {
  content: '';
  position: absolute;
  top: 0;
  bottom: 0;
  width: 2px;
  background: var(--border);
}
/* 节点圆点：agent 配色，锚在内容顶部对齐处。 */
.tl-node {
  position: relative;
  z-index: 1;
  width: 11px;
  height: 11px;
  margin-top: 6px;
  border-radius: 50%;
  background: var(--tl-accent, var(--muted));
  box-shadow: 0 0 0 3px var(--surface);
}
.tl-node.live {
  animation: tl-pulse 1.4s ease-in-out infinite;
}
@keyframes tl-pulse {
  50% { box-shadow: 0 0 0 5px color-mix(in srgb, var(--tl-accent) 30%, transparent); }
}
.tl-content {
  min-width: 0;
  flex: 1;
  padding-bottom: 14px;
}
/* 子代理行：整行右移缩进 + 内容区左侧 agent 配色竖条，表达「这是 orchestrator 派出去的子代理在干活」。 */
.tl-row.sub {
  margin-left: 26px;
}
.tl-row.sub .tl-content {
  border-left: 2px solid var(--tl-accent);
  padding-left: 12px;
  margin-left: -2px;
}
.tl-agent-tag {
  display: inline-block;
  font-size: 11px;
  font-weight: 600;
  color: var(--tl-accent);
  background: color-mix(in srgb, var(--tl-accent) 14%, transparent);
  border-radius: 4px;
  padding: 1px 8px;
  margin-bottom: 6px;
}
/* 换天分隔条 */
.tl-divider {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 10px 0 10px 30px;
  color: var(--muted);
  font-size: 11.5px;
}
.tl-divider::before,
.tl-divider::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--border);
}
.tl-divider span {
  flex-shrink: 0;
  font-family: var(--mono);
  letter-spacing: 0.02em;
}
/* 跳到最新浮标（与 ChatThread 同款视觉） */
.jump-latest {
  position: absolute;
  bottom: 16px;
  left: 50%;
  transform: translateX(-50%);
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 14px;
  font-size: 12.5px;
  color: #fff;
  background: var(--primary);
  border: none;
  border-radius: 999px;
  box-shadow: var(--shadow, 0 4px 12px rgba(0, 0, 0, 0.18));
  cursor: pointer;
  z-index: 10;
}
.jump-latest:hover { background: var(--primary-hover); }
.jl-count { font-weight: 600; }
.jl-arrow { font-size: 13px; line-height: 1; }
</style>
