<script setup lang="ts">
// 流量分析页：passive 会话的「分析 feed」——每批流量=一张可折叠卡（host · N条 · N findings）。
// 展开一张即载入该会话消息 + 开 SSE + 内联渲染作战轨迹（TimelineThread）+ follow-up 输入框。
// 手风琴：同时只展开一张（业界惯例；且对话 store 是全局单例，多开会串消息）——展开新的先收旧的。
//
// 与渗透会话（ChatView）区别：passive 由流量驱动自动建会话（无「新对话」发起），列表按 host 组织，
// 主视图是「批卡流」而非单会话对话。底层复用同一渲染管道（store/SSE/TimelineThread/Composer）。
import { onMounted, onUnmounted, ref, computed } from 'vue'
import { listConversations, listMessages, abortScan } from '../api/client'
import type { Conversation } from '../api/types'
import { useConversationStore } from '../stores/conversation'
import { openEventStream, type StreamHandle } from '../composables/useEventStream'
import { relativeTime, fullTime } from '../lib/format'
import { scanStatusMeta } from '../lib/scanStatus'
import TimelineThread from '../components/TimelineThread.vue'
import Composer from '../components/Composer.vue'

const store = useConversationStore()
const convs = ref<Conversation[]>([])
const loading = ref(false)
const loadError = ref(false)
const expandedId = ref('') // 当前展开的会话（手风琴：至多一个）
const bodyLoading = ref(false) // 展开项补历史中
let handle: StreamHandle | null = null

// 仅 passive：本页只展示流量驱动的被动会话（active 在「渗透会话」页）。
const passiveConvs = computed(() => convs.value.filter((c) => c.Mode === 'passive'))

async function refresh() {
  loading.value = true
  loadError.value = false
  try {
    convs.value = await listConversations()
  } catch {
    loadError.value = true
  } finally {
    loading.value = false
  }
}

// 自适应轮询：有进行中会话时每 8s 刷新列表（新批流量进来 / 状态翻转）。
let pollTimer: number | undefined
onMounted(() => {
  refresh()
  pollTimer = window.setInterval(() => {
    if (passiveConvs.value.some((c) => c.RunStatus === 'active')) refresh()
  }, 8000)
})
onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
  handle?.close()
})

// host 标题：passive 标题即 host（后端回填）；空则回退短 id。
function hostLabel(c: Conversation): string {
  return (c.Title || '').trim() || c.ID.slice(0, 8)
}
function statusMeta(c: Conversation) {
  return scanStatusMeta(c.RunStatus)
}

// 展开/折叠一张卡：展开 = 收旧的 + 清 store + 载入该会话历史 + 开 SSE 实时续推。
async function toggle(c: Conversation) {
  if (expandedId.value === c.ID) {
    collapse()
    return
  }
  handle?.close()
  store.reset()
  expandedId.value = c.ID
  bodyLoading.value = true
  let history
  try {
    history = await listMessages(c.ID)
  } catch {
    bodyLoading.value = false
    return
  }
  if (expandedId.value !== c.ID) return // 期间又切了别的卡
  bodyLoading.value = false
  for (const m of history) store.ingest(m)
  handle = openEventStream(c.ID, store)
}
function collapse() {
  handle?.close()
  handle = null
  store.reset()
  expandedId.value = ''
}

// follow-up 后拉增量补进 store（api 落的 user 消息不经 SSE）。
async function onAppended(afterSeq: number) {
  if (!expandedId.value) return
  for (const m of await listMessages(expandedId.value, afterSeq)) store.ingest(m)
}
async function stop() {
  if (expandedId.value) await abortScan(expandedId.value)
}

// 展开会话是否运行中（驱动 Composer 的停止/发送态）。
const expandedScanning = computed(() => {
  const c = passiveConvs.value.find((x) => x.ID === expandedId.value)
  return c?.RunStatus === 'active'
})
</script>

<template>
  <div class="feed-page">
    <div class="feed-toolbar">
      <div class="tb-left">
        <h2 class="tb-title">流量分析</h2>
        <span class="tb-sub">挂代理收流量 · AI 逐批分析挖洞 · 展开可看轨迹并插话</span>
      </div>
      <button class="refresh-btn" :disabled="loading" @click="refresh">↻ 刷新</button>
    </div>

    <div class="feed-body">
      <div v-if="loading && !passiveConvs.length" class="state">加载中…</div>
      <div v-else-if="loadError" class="state state-err">⚠ 加载失败，点刷新重试</div>
      <div v-else-if="!passiveConvs.length" class="state">
        暂无流量分析会话——挂代理（passive 8888）收到流量后，AI 自动逐批分析
      </div>

      <div v-else class="feed-list">
        <!-- 每批流量一张可折叠卡：头部 host + 状态 + findings 摘要，展开是作战轨迹 + 插话框 -->
        <div
          v-for="c in passiveConvs"
          :key="c.ID"
          class="batch-card"
          :class="{ expanded: expandedId === c.ID }"
          :style="{ '--st': statusMeta(c).color }"
        >
          <button class="bc-head" @click="toggle(c)">
            <span class="bc-caret">{{ expandedId === c.ID ? '▼' : '▶' }}</span>
            <span class="bc-host">{{ hostLabel(c) }}</span>
            <span class="bc-status" :class="'st-' + statusMeta(c).key">
              <span class="bc-dot" :class="'dot-' + statusMeta(c).key" />
              {{ statusMeta(c).label }}
            </span>
            <span v-if="c.FindingCount" class="bc-findings">🐛 {{ c.FindingCount }}</span>
            <span class="bc-time" :title="fullTime(c.CreatedAt)">{{ relativeTime(c.CreatedAt) }}</span>
          </button>

          <div v-if="expandedId === c.ID" class="bc-body">
            <div v-if="bodyLoading" class="bc-loading">载入分析轨迹…</div>
            <template v-else>
              <TimelineThread />
              <Composer
                :conv-id="c.ID"
                :scanning="expandedScanning"
                @appended="onAppended"
                @stop="stop"
              />
            </template>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.feed-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
.feed-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 20px;
  border-bottom: 1px solid var(--border);
  flex-shrink: 0;
}
.tb-left { display: flex; flex-direction: column; gap: 2px; }
.tb-title { margin: 0; font-size: 16px; font-weight: 600; }
.tb-sub { font-size: 12px; color: var(--muted); }
.refresh-btn {
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  padding: 6px 12px;
  font-size: 13px;
  color: var(--text);
  cursor: pointer;
}
.refresh-btn:hover:not(:disabled) { border-color: var(--primary); color: var(--primary); }
.refresh-btn:disabled { opacity: 0.5; cursor: default; }

.feed-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 16px 20px;
}
.state { color: var(--muted); font-size: 13px; padding: 40px 0; text-align: center; }
.state-err { color: var(--error); }

.feed-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-width: 1000px;
  margin: 0 auto;
}
/* 批卡：默认收起一行摘要；展开露出轨迹 + 插话框。左缘状态色条。 */
.batch-card {
  border: 1px solid var(--border);
  border-left: 3px solid var(--st);
  border-radius: var(--radius-lg);
  background: var(--surface);
  box-shadow: var(--shadow);
  overflow: hidden;
  transition: border-color 0.15s;
}
.batch-card.expanded { border-color: color-mix(in srgb, var(--st) 40%, var(--border)); }
.bc-head {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 13px 16px;
  background: transparent;
  border: none;
  cursor: pointer;
  text-align: left;
  font-size: 14px;
  color: var(--text);
}
.bc-head:hover { background: var(--surface-2); }
.bc-caret { font-size: 10px; color: var(--muted); width: 10px; flex-shrink: 0; }
.bc-host {
  font-family: var(--mono);
  font-weight: 600;
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.bc-status { display: inline-flex; align-items: center; gap: 5px; font-size: 12px; flex-shrink: 0; }
.bc-dot { width: 7px; height: 7px; border-radius: 50%; }
.dot-active { background: #34d399; animation: bc-pulse 1.6s ease-in-out infinite; }
.dot-done { background: #38bdf8; }
.dot-aborted { background: #94a3b8; }
.dot-idle { background: #64748b; }
@keyframes bc-pulse { 50% { opacity: 0.4; } }
.st-active { color: #34d399; }
.st-done { color: #38bdf8; }
.st-aborted { color: #94a3b8; }
.st-idle { color: #64748b; }
.bc-findings {
  font-size: 12px;
  color: var(--sev-high, #f59e0b);
  background: color-mix(in srgb, var(--sev-high, #f59e0b) 12%, transparent);
  border-radius: 5px;
  padding: 1px 8px;
  flex-shrink: 0;
}
.bc-time { font-size: 11.5px; color: var(--muted); font-family: var(--mono); flex-shrink: 0; }
/* 展开体：轨迹 + 插话框。限高避免单卡撑满整屏，内部滚动。 */
.bc-body {
  border-top: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  max-height: 70vh;
  min-height: 0;
}
.bc-body :deep(.tl-wrap) { flex: 1; min-height: 200px; }
.bc-loading { padding: 24px; text-align: center; color: var(--muted); font-size: 13px; }
</style>


