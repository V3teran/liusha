<script setup lang="ts">
// 对话主线：从 store 读有序消息逐条渲染，末尾挂流式推理活动气泡（逐字打字机）。
// 自动滚底：仅当用户本就贴在底部时，新消息/增量才把视图顶到最新——向上翻看历史时不打扰。
import { computed, nextTick, ref, watch } from 'vue'
import { useConversationStore } from '../stores/conversation'
import { dayKey, dayLabel } from '../lib/format'
import { buildThreadRows } from '../lib/threadRows'
import MessageItem from './MessageItem.vue'
import StepTools from './StepTools.vue'
import ReasoningCard from './cards/ReasoningCard.vue'
import Avatar from './cards/Avatar.vue'
import { useTypewriter } from '../composables/useTypewriter'

const store = useConversationStore()
const el = ref<HTMLElement>()

// 流式推理逐字揭示：后端 reasoning 流经 eino ReAct 图被 ConcatMessageStream 拍平（逐 token 在
// eino 内部即被抽干），到前端时整段 delta 毫秒内涌出。直接绑 liveReasoning 会整块蹦出、无逐字感。
// useTypewriter 把「已到达全文」按稳定节奏本地揭示，与网络到达节奏解耦（业界通行：ChatGPT/Claude UI）。
const typedReasoning = useTypewriter(computed(() => store.liveReasoning))

// 在消息流中按天插入分隔条（今天 / 昨天 / 日期）——跨天对话一眼可辨，内联卡片只显示时分秒。
// step：本次「用户指令」内的全局推理步号——每条 reasoning(想) 递增一步，跨所有 agent 统一计数
// （不按 agent 分组：一个 type 如 exploitation 会被 spawn 多个并发实例，按 type 累计会混淆、
//  按实例又无标识可分；全局序号无歧义）。**每条用户消息重置**：一次指令(发起→结束)是一个计数
// 周期，追加(follow-up)算新指令、步号从头。配合卡片已有的 agent 标签（编排/侦察/利用）定位「谁的第几步」。
// 按步分组渲染：分组逻辑抽到 lib/threadRows（与轨迹版 TimelineThread 共用，避免两处分歧）。
// reasoning(想)/spawn(派发)/finding(漏洞)/对话 独立成卡；紧随某步的普通工具调用折叠成组（StepTools）。
const rows = computed(() => buildThreadRows(store.messages, { dayKey, dayLabel }))

function nearBottom() {
  const e = el.value
  if (!e) return true
  return e.scrollHeight - e.scrollTop - e.clientHeight < 120
}

// atBottom：用户是否贴在底部（驱动「跳到最新」浮标显隐）。scroll 事件更新。
const atBottom = ref(true)
// unread：脱离底部后新增的消息数（浮标上的计数）。回到底部即清零。
const unread = ref(0)

function onScroll() {
  const wasBottom = atBottom.value
  atBottom.value = nearBottom()
  if (atBottom.value && !wasBottom) unread.value = 0 // 滚回底部 → 清未读
}

function scrollToBottom() {
  const e = el.value
  if (e) e.scrollTop = e.scrollHeight
  unread.value = 0
}

async function stickToBottom() {
  const stick = nearBottom()
  await nextTick()
  if (stick && el.value) el.value.scrollTop = el.value.scrollHeight
}

// 消息增减：贴底则自动滚底；脱离底部则累加未读计数（驱动浮标）。
watch(
  () => store.messages.length,
  (n, prev) => {
    if (nearBottom()) stickToBottom()
    else if (n > (prev ?? 0)) unread.value += n - (prev ?? 0)
  },
)
// 流式逐字揭示时贴底跟随滚动，不计未读（增量不是新消息）。跟 typedReasoning（渐进变化）
// 而非 liveReasoning（整块跳变）——让滚动随打字机平滑推进，而不是一次跳到底。
watch(typedReasoning, stickToBottom)
</script>

<template>
  <div class="thread-wrap">
    <!-- aria-live=polite：新落定的消息/工具组会被屏读器播报（不打断当前朗读）。
         aria-relevant=additions：只播报新增节点，忽略滚动引起的移除/重排。 -->
    <div
      ref="el"
      class="thread"
      role="log"
      aria-live="polite"
      aria-relevant="additions"
      aria-label="对话消息"
      @scroll.passive="onScroll"
    >
      <template v-for="r in rows" :key="r.key">
        <div v-if="r.kind === 'divider'" class="day-divider"><span>{{ r.label }}</span></div>
        <StepTools v-else-if="r.kind === 'tools'" :tools="r.tools" />
        <MessageItem v-else :msg="r.msg" :step="r.step" />
      </template>
      <!-- 流式推理活动气泡：套与落定推理卡相同的头像行布局（avatar-slot + 内容），
           并传 agent-name 让配色/标签一致——否则流式为中性灰无头像、落定变彩色带头像，前后两个样子。
           逐字打字机会触发上百次 DOM 变更，aria-hidden 避免屏读器逐字刷屏；
           推理最终帧作为正式消息落定时会被 aria-live 正常播报一次。 -->
      <div v-if="store.liveReasoning" class="msg-row" aria-hidden="true">
        <div class="avatar-slot"><Avatar who="agent" /></div>
        <div class="msg-content">
          <ReasoningCard :text="typedReasoning" :agent-name="store.liveAgentName || undefined" streaming />
        </div>
      </div>
    </div>
    <!-- 跳到最新浮标：脱离底部时出现，带未读计数。滚动直播刷得快，翻看历史后一键回到实时。 -->
    <button v-if="!atBottom" class="jump-latest" type="button" @click="scrollToBottom">
      <span v-if="unread > 0" class="jl-count">{{ unread > 99 ? '99+' : unread }} 条新消息</span>
      <span v-else class="jl-count">回到最新</span>
      <span class="jl-arrow">↓</span>
    </button>
  </div>
</template>

<style scoped>
/* wrapper 接管 .thread 原本的 flex:1 角色，并作为浮标的定位上下文。
   .thread（全局 style.css：flex:1 + overflow-y:auto）在 wrapper 内继续负责滚动。 */
.thread-wrap {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
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
  animation: jl-in var(--duration-fast, 150ms) ease-out;
}
.jump-latest:hover { background: var(--primary-hover); }
.jl-count { font-weight: 600; }
.jl-arrow { font-size: 13px; line-height: 1; }
@keyframes jl-in {
  from { opacity: 0; transform: translate(-50%, 8px); }
  to { opacity: 1; transform: translate(-50%, 0); }
}

.day-divider {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 6px 0;
  color: var(--muted);
  font-size: 11.5px;
}
.day-divider::before,
.day-divider::after {
  content: '';
  flex: 1;
  height: 1px;
  background: var(--border);
}
.day-divider span {
  flex-shrink: 0;
  font-family: var(--mono);
  letter-spacing: 0.02em;
}

/* 流式活动气泡的头像行布局——与 MessageItem 的 agent 侧一致（头像槽 + 内容列），
   让流式与落定的推理卡对齐（同缩进、同头像位）。 */
.msg-row {
  display: flex;
  gap: 10px;
  align-items: flex-start;
}
.msg-row .avatar-slot {
  width: 30px;
  flex-shrink: 0;
}
.msg-row .msg-content {
  min-width: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
}
</style>
