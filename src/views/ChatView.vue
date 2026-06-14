<script setup lang="ts">
// 对话页：复用既有对话链路（ConversationList + ChatThread + Composer + SSE）。
// 选中/发起对话切流：关旧 SSE、清 store、补历史、订新流。
// 状态条：lastIngestAt 在 25s 内 → "agent 工作中"；空对话 → 引导空态。
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { listMessages, abortScan } from '../api/client'
import { useConversationStore } from '../stores/conversation'
import { openEventStream, type StreamHandle } from '../composables/useEventStream'
import ConversationList from '../components/ConversationList.vue'
import Composer from '../components/Composer.vue'
import ChatThread from '../components/ChatThread.vue'

const store = useConversationStore()
const route = useRoute()
const currentConv = ref<string>('')
let handle: StreamHandle | null = null

// 每 2s 走一拍，驱动"工作中"判定刷新。
const now = ref(Date.now())
let timer: number | undefined
onMounted(() => {
  timer = window.setInterval(() => (now.value = Date.now()), 2000)
  // 从被动会话页跳来（?conv=xxx）：自动打开该对话流（实时观察 + 插话）。
  if (typeof route.query.conv === 'string' && route.query.conv) open(route.query.conv)
})
// 已在 /chat 时再次跳转（query 变化）也切换到目标对话。
watch(
  () => route.query.conv,
  (c) => {
    if (typeof c === 'string' && c && c !== currentConv.value) open(c)
  }
)
onUnmounted(() => {
  if (timer) clearInterval(timer)
  handle?.close()
})

const hasConv = computed(() => !!currentConv.value)
const scanning = computed(
  () => hasConv.value && store.lastIngestAt > 0 && now.value - store.lastIngestAt < 25000
)

const convList = ref<InstanceType<typeof ConversationList> | null>(null)

async function open(convID: string) {
  handle?.close()
  store.reset()
  currentConv.value = convID
  for (const m of await listMessages(convID)) store.ingest(m)
  handle = openEventStream(convID, store)
}
// 新对话发起：打开它 + 刷新左侧列表（否则新对话不出现，要手动点 ↻）。
async function handleStarted(convID: string) {
  await open(convID)
  convList.value?.refresh()
}
// 多轮追加（如"继续"）：api 落的 user 消息不经 SSE（只 scanner 事件 publish），
// 故主动拉增量补进 store（立即看到自己的"继续"+ 已有新事件）；后续 agent 事件走 SSE。
async function handleAppended() {
  if (!currentConv.value) return
  for (const m of await listMessages(currentConv.value, store.lastSeq)) store.ingest(m)
}
function newConversation() {
  handle?.close()
  store.reset()
  currentConv.value = ''
}
async function stop() {
  if (currentConv.value) await abortScan(currentConv.value)
}
</script>

<template>
  <div class="chat-view">
    <ConversationList ref="convList" @select="open" @new="newConversation" />
    <section class="chat-main">
      <div v-if="hasConv" class="chat-status">
        <span class="live" :class="{ active: scanning }">
          <span class="pulse" />
          {{ scanning ? 'agent 工作中…' : '空闲 / 已完成' }}
        </span>
        <button v-if="scanning" class="stop-btn" @click="stop">■ 停止扫描</button>
      </div>

      <ChatThread v-if="hasConv || store.messages.length" />
      <div v-else class="chat-empty">
        <div class="empty-card">
          <div class="empty-mark">⌖</div>
          <h2>发起一次渗透扫描</h2>
          <p>在下方选择场景角色、描述目标（URL / 账号 / 测试方向），<br />实时观察 orchestrator 派活、工具调用与漏洞产出。</p>
          <ul class="hints">
            <li><b>active</b>：一句话 brief 喂 hunter，自主侦察 + 深挖</li>
            <li><b>passive</b>：挂代理收流量，逐条分析出 finding</li>
          </ul>
        </div>
      </div>

      <Composer :conv-id="currentConv || undefined" @started="handleStarted" @appended="handleAppended" />
    </section>
  </div>
</template>

<style scoped>
.chat-view {
  display: grid;
  grid-template-columns: 256px 1fr;
  height: 100%;
  min-height: 0;
}
.chat-view :deep(.conv-list) { border-right: 1px solid var(--border); }
.chat-main { display: flex; flex-direction: column; min-height: 0; }

.chat-status {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 9px 16px;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.live { display: inline-flex; align-items: center; gap: 8px; font-size: 13px; color: var(--muted); }
.live .pulse { width: 8px; height: 8px; border-radius: 50%; background: var(--sev-low); }
.live.active { color: var(--accent); }
.live.active .pulse {
  background: var(--accent);
  box-shadow: 0 0 0 0 var(--accent);
  animation: pulse 1.4s infinite;
}
@keyframes pulse {
  0% { box-shadow: 0 0 0 0 color-mix(in srgb, var(--accent) 60%, transparent); }
  70% { box-shadow: 0 0 0 7px transparent; }
  100% { box-shadow: 0 0 0 0 transparent; }
}
.stop-btn {
  border: 1px solid var(--sev-critical);
  color: var(--sev-critical);
  background: transparent;
  border-radius: 8px;
  padding: 5px 13px;
  cursor: pointer;
  font-size: 12.5px;
}
.stop-btn:hover { background: color-mix(in srgb, var(--sev-critical) 14%, transparent); }

.chat-empty { flex: 1; display: grid; place-items: center; padding: 30px; }
.empty-card {
  max-width: 460px;
  text-align: center;
  padding: 36px 32px;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background:
    radial-gradient(420px 200px at 50% 0%, var(--primary-soft), transparent),
    var(--surface);
}
.empty-mark {
  font-size: 46px;
  line-height: 1;
  color: var(--primary);
  margin-bottom: 14px;
}
.empty-card h2 { margin: 0 0 10px; font-size: 20px; }
.empty-card p { margin: 0 0 18px; color: var(--muted); font-size: 13.5px; line-height: 1.7; }
.hints { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 8px; text-align: left; }
.hints li {
  font-size: 12.5px;
  color: var(--muted);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 8px 12px;
}
.hints b { color: var(--accent); font-family: var(--mono); }
</style>
