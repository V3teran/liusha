<script setup lang="ts">
// 对话页：复用既有对话链路（ConversationList + ChatThread + Composer + SSE）。
// 选中/发起对话切流：关旧 SSE、清 store、补历史、订新流。
// 状态条：用量端点权威 running 字段（active_scan/passive_session 终态）→ "agent 工作中"；
//   不再用"N 秒无活动"启发——避免打开已结束会话因历史回灌误判为工作中。
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { listMessages, abortScan, getConversationUsage } from '../api/client'
import type { ConversationUsage } from '../api/types'
import { useConversationStore } from '../stores/conversation'
import { openEventStream, type StreamHandle } from '../composables/useEventStream'
import ConversationList from '../components/ConversationList.vue'
import Composer from '../components/Composer.vue'
import ChatThread from '../components/ChatThread.vue'
import { compactNumber, humanTokens, humanDuration, fullTime } from '../lib/format'
import { scanStatusMeta } from '../lib/scanStatus'

const store = useConversationStore()
const route = useRoute()
const currentConv = ref<string>('')
// 历史加载态：loading 驱动骨架屏，loadError 驱动错误重试卡。仅覆盖「补历史」阶段，
// 不含 SSE（SSE 断线另有 reconnecting chip）。
const loading = ref(false)
const loadError = ref(false)
let handle: StreamHandle | null = null
// SSE 连接态（驱动「重连中…」chip）；切会话/新建时重置为 open 占位。
const streamStatus = ref<'connecting' | 'open' | 'reconnecting'>('open')
// 上一个流句柄状态 watcher 的停止器——切会话时先停旧的，避免 watcher 累积泄漏。
let stopStatusWatch: (() => void) | null = null

// 本对话权威用量（后端 SUM llm_invocation + tool_invocation）。开对话即取、扫描中轮询。
const usage = ref<ConversationUsage | null>(null)
// 发起时间：首条消息（首次用户提问）的落库时刻。
const startedAt = computed(() => store.messages[0]?.CreatedAt ?? '')

async function refreshUsage() {
  const reqConv = currentConv.value
  if (!reqConv) return
  try {
    const u = await getConversationUsage(reqConv)
    // stale 防护：请求在途期间用户已切走会话 → 丢弃这份旧响应，
    // 否则会把上一个会话的 token/耗时短暂写到当前会话头部（切换闪现旧数据）。
    if (currentConv.value === reqConv) usage.value = u
  } catch {
    // 静默：用量是增强信息，拉取失败不打断对话观察。
  }
}

// 事件驱动刷新（#4）：每有新消息落定（seq 增长）就刷新用量，防抖 800ms 合并突发。
// 不依赖 25s scanning 启发——长静默工具跑完、结果事件一到即刷新，无滞后。
let usageDebounce: number | undefined
function scheduleUsageRefresh() {
  if (usageDebounce) clearTimeout(usageDebounce)
  usageDebounce = window.setTimeout(refreshUsage, 800)
}

const tokenTip = computed(() => {
  const u = usage.value
  if (!u) return ''
  return [
    `输入 ${humanTokens(u.tokens.in)}`,
    `输出 ${humanTokens(u.tokens.out)}`,
    `其中缓存命中 ${humanTokens(u.tokens.cached)}`,
    `合计 ${humanTokens(u.tokens.total)}`,
    `${u.llm_calls} 次 LLM 调用`,
  ].join(' · ')
})
const durationTip = computed(() => {
  const u = usage.value
  if (!u) return ''
  return [
    `墙钟 ${humanDuration(u.duration_ms)}（发起→完成）`,
    `工作时间 ${humanDuration(u.work_ms)}（含子代理并发累加，故 > 墙钟）`,
    `LLM ${humanDuration(u.llm_latency_ms)} · 工具 ${humanDuration(u.tool_duration_ms)} · ${u.tool_calls} 次工具调用`,
  ].join(' · ')
})

// 运行中兜底轮询：扫描进行时每 4s 拉一次权威用量，捕获"最后一个事件后扫描终态翻转"
// （事件驱动刷新覆盖活动期，本轮询补完成时刻）。终态后 running=false 自然停止轮询。
let timer: number | undefined
onMounted(() => {
  timer = window.setInterval(() => {
    if (scanning.value) refreshUsage()
  }, 4000)
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
  if (usageDebounce) clearTimeout(usageDebounce)
  stopStatusWatch?.()
  handle?.close()
})

const hasConv = computed(() => !!currentConv.value)
// 权威运行态：后端 usage.running（active_scan/passive_session 是否仍 active）。
const scanning = computed(() => hasConv.value && (usage.value?.running ?? false))
// 顶部状态栏真实三态（进行中/已完成/已中止）——用 usage.status（active_scan 真实态），
// 与左侧列表共用 scanStatusMeta 映射；修「二元 running 把 aborted 错显示成已完成」的 bug。
const topStatus = computed(() => scanStatusMeta(usage.value?.status))
// 每条新消息落定（seq 增长）→ 防抖刷新用量（事件驱动，见 scheduleUsageRefresh）。
watch(() => store.lastSeq, scheduleUsageRefresh)

const convList = ref<InstanceType<typeof ConversationList> | null>(null)

// 扫描运行态翻转（进行中 ⇄ 完成）→ 自动刷新左侧列表，让列表项状态点跟随真实态，
// 无需手动点 ↻。覆盖「扫描跑完那一刻列表卡在进行中」的体验缺口。
watch(scanning, () => convList.value?.refresh())

async function open(convID: string) {
  handle?.close()
  store.reset()
  usage.value = null
  currentConv.value = convID
  loadError.value = false
  loading.value = true
  // 分页拉全可能耗时；期间用户又切了会话则丢弃这批历史，避免灌进错误会话的消息。
  let history
  try {
    history = await listMessages(convID)
  } catch {
    if (currentConv.value === convID) {
      loading.value = false
      loadError.value = true // 驱动错误重试卡
    }
    return
  }
  if (currentConv.value !== convID) return
  loading.value = false
  for (const m of history) store.ingest(m)
  handle = openEventStream(convID, store)
  // 把流句柄的连接态镜像到本地 ref（驱动「重连中…」chip）。先停旧 watcher 再绑新的，防累积泄漏。
  stopStatusWatch?.()
  stopStatusWatch = watch(handle.status, (s) => (streamStatus.value = s), { immediate: true })
  refreshUsage()
}
// 新对话发起：打开它 + 刷新左侧列表（否则新对话不出现，要手动点 ↻）。
async function handleStarted(convID: string) {
  await open(convID)
  convList.value?.refresh()
}
// 多轮追加（如"继续"）：api 落的 user 消息不经 SSE（只 scanner 事件 publish），故主动拉增量补进 store。
// 游标用 Composer 发送前的 seq 快照（afterSeq），不用 store.lastSeq——后者会被 SSE 抢先推高、
// 把 user 消息跳过（→ store 缺 user 消息 → ChatThread 步号不重置的竞态 bug）。重叠消息靠 store seqSet 去重。
async function handleAppended(afterSeq: number) {
  if (!currentConv.value) return
  for (const m of await listMessages(currentConv.value, afterSeq)) store.ingest(m)
}
function newConversation() {
  stopStatusWatch?.()
  stopStatusWatch = null
  handle?.close()
  store.reset()
  usage.value = null
  streamStatus.value = 'open'
  loading.value = false
  loadError.value = false
  currentConv.value = ''
}
// 补历史失败重试：重开当前会话（open 内已重置 loadError/loading）。
function retryLoad() {
  if (currentConv.value) open(currentConv.value)
}
// 删除的若是当前打开的对话 → 回到新建态（清空主区）；删别的对话不影响当前视图。
function onConvDeleted(convID: string) {
  if (convID === currentConv.value) newConversation()
}
async function stop() {
  if (currentConv.value) await abortScan(currentConv.value)
}
</script>

<template>
  <div class="chat-view">
    <ConversationList
      ref="convList"
      :active-id="currentConv || undefined"
      @select="open"
      @new="newConversation"
      @deleted="onConvDeleted"
    />
    <section class="chat-main">
      <div v-if="hasConv" class="chat-status">
        <div class="status-left">
          <span class="live" :class="['st-' + topStatus.key, { active: scanning }]">
            <span class="pulse" :style="{ background: topStatus.color }" />
            {{ scanning ? 'agent 工作中…' : topStatus.label }}
          </span>
          <!-- SSE 重连中 chip：区分「连接断了在重连」与「正常静默」，长扫描断线用户有感知。 -->
          <span v-if="streamStatus === 'reconnecting'" class="reconnect-chip" title="实时连接断开，正在自动重连…">
            <span class="rc-spinner" />重连中…
          </span>
          <span v-if="startedAt" class="started" :title="'发起于 ' + fullTime(startedAt)">
            发起 {{ fullTime(startedAt) }}
          </span>
        </div>
        <div class="status-right">
          <span v-if="usage && usage.tokens.total > 0" class="metrics">
            <span class="metric" :title="tokenTip">
              <span class="m-label">tokens</span>
              <span class="m-val"><span class="t-in" title="输入">↑{{ compactNumber(usage.tokens.in) }}</span> <span class="t-out" title="输出">↓{{ compactNumber(usage.tokens.out) }}</span></span>
            </span>
            <span class="metric" :title="durationTip">
              <span class="m-label">耗时</span>
              <span class="m-val">{{ humanDuration(usage.duration_ms) }}</span>
            </span>
          </span>
          <button v-if="scanning" class="stop-btn" @click="stop">■ 停止扫描</button>
        </div>
      </div>

      <!-- 补历史失败：错误重试卡（占据主区，一键重试）。 -->
      <div v-if="loadError" class="chat-error">
        <div class="err-card">
          <div class="err-mark">⚠</div>
          <h2>加载对话失败</h2>
          <p>无法拉取历史消息，可能是网络或服务暂时不可用。</p>
          <button class="err-retry" @click="retryLoad">重试</button>
        </div>
      </div>
      <!-- 补历史中：骨架屏（避免空白闪现，给出「正在加载」的确定感）。 -->
      <div v-else-if="loading" class="chat-skeleton" aria-busy="true" aria-label="正在加载对话">
        <div v-for="n in 5" :key="n" class="sk-row" :class="n % 2 ? 'sk-left' : 'sk-right'">
          <div class="sk-line sk-w1" />
          <div class="sk-line sk-w2" />
          <div class="sk-line sk-w3" />
        </div>
      </div>
      <ChatThread v-else-if="hasConv || store.messages.length" />
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

      <Composer
        :conv-id="currentConv || undefined"
        :scanning="scanning"
        @started="handleStarted"
        @appended="handleAppended"
        @stop="stop"
      />
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
.live.active { color: var(--accent); } /* 工作中：accent + 脉冲（见下方 .live.active .pulse） */
/* 终态文字色（非工作中）：已完成蓝 / 已中止灰，与左侧列表统一 */
.live.st-done { color: #38bdf8; }
.live.st-aborted { color: #94a3b8; }
.reconnect-chip {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 11.5px;
  color: var(--sev-medium);
  background: color-mix(in srgb, var(--sev-medium) 12%, transparent);
  border-radius: 999px;
  padding: 2px 9px;
}
.rc-spinner {
  width: 9px;
  height: 9px;
  border: 1.5px solid var(--sev-medium);
  border-top-color: transparent;
  border-radius: 50%;
  animation: rc-spin 0.7s linear infinite;
}
@keyframes rc-spin { to { transform: rotate(360deg); } }
.status-left { display: inline-flex; align-items: center; gap: 14px; min-width: 0; }
.started {
  font-size: 11.5px;
  color: var(--muted);
  font-family: var(--mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.status-right { display: inline-flex; align-items: center; gap: 12px; }
.metrics { display: inline-flex; align-items: center; gap: 8px; }
.metric {
  display: inline-flex;
  align-items: baseline;
  gap: 6px;
  padding: 3px 10px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--surface-2);
  font-size: 12px;
}
.metric .m-label { color: var(--muted); font-size: 11px; }
.metric .m-val { color: var(--text); font-family: var(--mono); font-variant-numeric: tabular-nums; }
.metric .t-in { color: #38bdf8; }
.metric .t-out { color: #34d399; }
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

/* 骨架屏：左右交替的气泡占位，shimmer 扫光。prefers-reduced-motion 下停 shimmer（见文末媒体查询）。 */
.chat-skeleton {
  flex: 1;
  min-height: 0;
  overflow: hidden;
  padding: 20px 16px;
  display: flex;
  flex-direction: column;
  gap: 20px;
}
.sk-row {
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-width: 60%;
}
.sk-row.sk-left { align-self: flex-start; }
.sk-row.sk-right { align-self: flex-end; align-items: flex-end; }
.sk-line {
  height: 12px;
  border-radius: 6px;
  background: linear-gradient(
    90deg,
    var(--surface-2) 0%,
    color-mix(in srgb, var(--surface-2) 40%, var(--border)) 50%,
    var(--surface-2) 100%
  );
  background-size: 200% 100%;
  animation: sk-shimmer 1.4s ease-in-out infinite;
}
.sk-w1 { width: 220px; }
.sk-w2 { width: 300px; }
.sk-w3 { width: 160px; }
@keyframes sk-shimmer {
  0% { background-position: 200% 0; }
  100% { background-position: -200% 0; }
}

/* 加载失败错误卡 */
.chat-error { flex: 1; display: grid; place-items: center; padding: 30px; }
.err-card {
  max-width: 400px;
  text-align: center;
  padding: 32px;
  border: 1px solid var(--border);
  border-radius: var(--radius-lg);
  background: var(--surface);
}
.err-mark { font-size: 40px; line-height: 1; color: var(--sev-high); margin-bottom: 12px; }
.err-card h2 { margin: 0 0 8px; font-size: 18px; }
.err-card p { margin: 0 0 18px; color: var(--muted); font-size: 13px; line-height: 1.6; }
.err-retry {
  padding: 7px 22px;
  background: var(--accent);
  color: #fff;
  border: none;
  border-radius: 8px;
  font-weight: 600;
  font-size: 13px;
  cursor: pointer;
}
.err-retry:hover { background: var(--primary-hover, var(--accent)); }

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
