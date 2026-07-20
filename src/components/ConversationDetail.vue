<script setup lang="ts">
// 会话详情主区（主从双栏的「从」）：给定 convId，渲染其作战轨迹 + 顶部状态栏 + 插话框。
// 渗透会话(active) 与 流量分析(passive) 两页共用此组件——统一骨架，右侧渲染同一 TimelineThread
// （active 多代理→脊柱缩进分叉；passive 单代理→自然扁平），差异由数据本身表达，不做两套渲染器。
//
// 生命周期：watch convId 变化→关旧 SSE、清 store、补历史、订新流。convId 空=新建态（仅 active，
// 露空状态 + 可发起的 Composer）。全局 store 单例，故同一时刻只挂一个本组件实例（每页一个）。
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { listMessages, abortScan, getConversationUsage } from '../api/client'
import type { ConversationUsage } from '../api/types'
import { useConversationStore } from '../stores/conversation'
import { openEventStream, type StreamHandle } from '../composables/useEventStream'
import Composer from './Composer.vue'
import TimelineThread from './TimelineThread.vue'
import { compactNumber, humanTokens, humanDuration, fullTime } from '../lib/format'
import { scanStatusMeta } from '../lib/scanStatus'

// convId 空=新建态；mode 驱动空状态文案与 Composer 是否可发起新扫描（passive 由流量驱动，不发起）。
const props = defineProps<{ convId?: string; mode: 'active' | 'passive' }>()
// started：新会话建立→向上抛，父组件负责改选中 + 刷列表。running-changed：运行态翻转→父刷列表状态点。
const emit = defineEmits<{ started: [convID: string]; 'running-changed': [] }>()

const store = useConversationStore()
const loading = ref(false) // 补历史中→骨架屏
const loadError = ref(false) // 补历史失败→错误重试卡
let handle: StreamHandle | null = null
const streamStatus = ref<'connecting' | 'open' | 'reconnecting'>('open')
let stopStatusWatch: (() => void) | null = null

const usage = ref<ConversationUsage | null>(null)
const startedAt = computed(() => store.messages[0]?.CreatedAt ?? '')

async function refreshUsage() {
  const reqConv = props.convId
  if (!reqConv) return
  try {
    const u = await getConversationUsage(reqConv)
    // stale 防护：在途期间用户已切走会话→丢弃旧响应，避免把上个会话的用量短暂写到当前头部。
    if (props.convId === reqConv) usage.value = u
  } catch {
    // 静默：用量是增强信息，拉取失败不打断观察。
  }
}

// 事件驱动刷新：每有新消息落定（seq 增长）防抖 800ms 刷用量，不依赖固定轮询启发。
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

// 运行中兜底轮询：进行时每 4s 拉权威用量，补「最后事件后终态翻转」；终态 running=false 自停。
let timer: number | undefined
onMounted(() => {
  timer = window.setInterval(() => {
    if (scanning.value) refreshUsage()
  }, 4000)
  if (props.convId) open(props.convId)
})
onUnmounted(() => {
  if (timer) clearInterval(timer)
  if (usageDebounce) clearTimeout(usageDebounce)
  stopStatusWatch?.()
  handle?.close()
})

const hasConv = computed(() => !!props.convId)
// 权威运行态：后端 usage.running（active_scan/passive_session 是否仍 active）。
const scanning = computed(() => hasConv.value && (usage.value?.running ?? false))
// 顶部三态（进行中/已完成/已中止）用 usage.status 真实态，与左列表共用 scanStatusMeta。
const topStatus = computed(() => scanStatusMeta(usage.value?.status))
watch(() => store.lastSeq, scheduleUsageRefresh)
// 运行态翻转→通知父刷新左列表状态点（无需手动点 ↻）。
watch(scanning, () => emit('running-changed'))

// convId 变化即切会话（含切到 ''=新建态）。immediate 已由 onMounted 覆盖，故仅监听后续变化。
watch(
  () => props.convId,
  (id, prev) => {
    if (id === prev) return
    if (id) open(id)
    else resetToEmpty()
  },
)

async function open(convID: string) {
  handle?.close()
  store.reset()
  usage.value = null
  loadError.value = false
  loading.value = true
  streamStatus.value = 'open'
  let history
  try {
    history = await listMessages(convID)
  } catch {
    if (props.convId === convID) {
      loading.value = false
      loadError.value = true
    }
    return
  }
  if (props.convId !== convID) return // 期间又切了会话→丢弃这批历史
  loading.value = false
  for (const m of history) store.ingest(m)
  handle = openEventStream(convID, store)
  stopStatusWatch?.()
  stopStatusWatch = watch(handle.status, (s) => (streamStatus.value = s), { immediate: true })
  refreshUsage()
}

function resetToEmpty() {
  stopStatusWatch?.()
  stopStatusWatch = null
  handle?.close()
  handle = null
  store.reset()
  usage.value = null
  streamStatus.value = 'open'
  loading.value = false
  loadError.value = false
}

// 新会话发起（仅 active，convId 空时 Composer 可发起）：向上抛，父组件改选中 + 刷列表。
function handleStarted(convID: string) {
  emit('started', convID)
}
// 多轮追加（如"继续"）：api 落的 user 消息不经 SSE，主动拉增量补进 store（游标=发送前 seq 快照）。
async function handleAppended(afterSeq: number) {
  if (!props.convId) return
  for (const m of await listMessages(props.convId, afterSeq)) store.ingest(m)
}
function retryLoad() {
  if (props.convId) open(props.convId)
}
async function stop() {
  if (props.convId) await abortScan(props.convId)
}

// 空状态文案按模式区分（active 可发起、passive 等流量）。
const emptyCopy = computed(() =>
  props.mode === 'passive'
    ? {
        mark: '⇄',
        title: '选择一批流量查看分析',
        desc: '左侧是代理捕获的流量批次，AI 已逐批分析。<br />点开任意一批，实时观察分析轨迹与漏洞产出，可随时插话追问。',
      }
    : {
        mark: '⌖',
        title: '发起一次渗透扫描',
        desc: '在下方选择场景角色、描述目标（URL / 账号 / 测试方向），<br />实时观察 orchestrator 派活、工具调用与漏洞产出。',
      },
)
</script>

<template>
  <section class="cd-main">
    <div v-if="hasConv" class="cd-status">
      <div class="status-left">
        <span class="live" :class="['st-' + topStatus.key, { active: scanning }]">
          <span class="pulse" :style="{ background: topStatus.color }" />
          {{ scanning ? 'agent 工作中…' : topStatus.label }}
        </span>
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

    <div v-if="loadError" class="cd-error">
      <div class="err-card">
        <div class="err-mark">⚠</div>
        <h2>加载对话失败</h2>
        <p>无法拉取历史消息，可能是网络或服务暂时不可用。</p>
        <button class="err-retry" @click="retryLoad">重试</button>
      </div>
    </div>
    <div v-else-if="loading" class="cd-skeleton" aria-busy="true" aria-label="正在加载对话">
      <div v-for="n in 5" :key="n" class="sk-row" :class="n % 2 ? 'sk-left' : 'sk-right'">
        <div class="sk-line sk-w1" />
        <div class="sk-line sk-w2" />
        <div class="sk-line sk-w3" />
      </div>
    </div>
    <TimelineThread v-else-if="hasConv || store.messages.length" />
    <div v-else class="cd-empty">
      <div class="empty-card">
        <div class="empty-mark">{{ emptyCopy.mark }}</div>
        <h2>{{ emptyCopy.title }}</h2>
        <p v-html="emptyCopy.desc" />
      </div>
    </div>

    <!-- passive 无「发起新扫描」：仅当有选中会话时才露插话框；active 允许空态发起。 -->
    <Composer
      v-if="mode === 'active' || hasConv"
      :conv-id="convId || undefined"
      :scanning="scanning"
      @started="handleStarted"
      @appended="handleAppended"
      @stop="stop"
    />
  </section>
</template>

<style scoped>
.cd-main {
  display: flex;
  flex-direction: column;
  min-height: 0;
  height: 100%;
}
.cd-status {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 9px 16px;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.status-left,
.status-right { display: inline-flex; align-items: center; gap: 12px; }
.live { display: inline-flex; align-items: center; gap: 8px; font-size: 13px; color: var(--muted); }
.pulse { width: 8px; height: 8px; border-radius: 50%; }
.live.active .pulse { animation: cd-pulse 1.6s ease-in-out infinite; }
@keyframes cd-pulse { 50% { opacity: 0.35; } }
.reconnect-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: var(--sev-med, #f59e0b);
}
.rc-spinner {
  width: 11px;
  height: 11px;
  border: 2px solid color-mix(in srgb, var(--sev-med, #f59e0b) 40%, transparent);
  border-top-color: var(--sev-med, #f59e0b);
  border-radius: 50%;
  animation: rc-spin 0.7s linear infinite;
}
@keyframes rc-spin { to { transform: rotate(360deg); } }
.started { font-size: 11.5px; color: var(--muted); font-family: var(--mono); }
.metrics { display: inline-flex; align-items: center; gap: 14px; }
.metric { display: inline-flex; align-items: baseline; gap: 6px; font-size: 12px; }
.m-label { color: var(--muted); }
.m-val { font-family: var(--mono); color: var(--text); }
.t-in { color: var(--sev-info, #38bdf8); }
.t-out { color: var(--sev-high, #f59e0b); }
.stop-btn {
  background: color-mix(in srgb, var(--error) 12%, transparent);
  border: 1px solid var(--error);
  color: var(--error);
  border-radius: var(--radius, 8px);
  padding: 5px 12px;
  font-size: 12.5px;
  cursor: pointer;
}
.stop-btn:hover { background: color-mix(in srgb, var(--error) 20%, transparent); }

/* 错误重试卡 / 空状态：居中占据主区 */
.cd-error,
.cd-empty {
  flex: 1;
  min-height: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
}
.err-card,
.empty-card {
  text-align: center;
  max-width: 420px;
  color: var(--muted);
}
.err-mark,
.empty-mark {
  font-size: 40px;
  line-height: 1;
  margin-bottom: 14px;
  color: var(--primary);
}
.err-card h2,
.empty-card h2 { margin: 0 0 8px; font-size: 17px; color: var(--text); }
.err-card p,
.empty-card p { margin: 0; font-size: 13px; line-height: 1.7; }
.err-retry {
  margin-top: 16px;
  background: var(--primary);
  color: #fff;
  border: none;
  border-radius: var(--radius, 8px);
  padding: 8px 20px;
  font-size: 13px;
  cursor: pointer;
}
.err-retry:hover { background: var(--primary-hover); }

/* 骨架屏 */
.cd-skeleton {
  flex: 1;
  min-height: 0;
  overflow: hidden;
  padding: 20px 16px;
  display: flex;
  flex-direction: column;
  gap: 18px;
}
.sk-row { display: flex; flex-direction: column; gap: 7px; max-width: 60%; }
.sk-row.sk-right { align-self: flex-end; align-items: flex-end; }
.sk-line {
  height: 11px;
  border-radius: 6px;
  background: linear-gradient(90deg, var(--surface-2) 25%, var(--border) 37%, var(--surface-2) 63%);
  background-size: 400% 100%;
  animation: sk-shimmer 1.4s ease infinite;
}
.sk-w1 { width: 180px; }
.sk-w2 { width: 260px; }
.sk-w3 { width: 120px; }
@keyframes sk-shimmer { 0% { background-position: 100% 50%; } 100% { background-position: 0 50%; } }
</style>
