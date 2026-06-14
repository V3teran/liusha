<script setup lang="ts">
// 被动会话页：列出 owner 会话（passive + active），passive 会话可「打开对话流」实时观察 + 插话。
// passive 由流量驱动建会话（conversation_id 绑定），点击跳 /chat 看 agent 分析并插话指导。
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { listSessions, abortSession } from '../api/client'
import type { OwnerSummary } from '../api/types'

const router = useRouter()
const sessions = ref<OwnerSummary[]>([])
const loading = ref(false)
const error = ref('')

onMounted(load)
async function load() {
  loading.value = true
  error.value = ''
  try {
    sessions.value = await listSessions(50)
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

// scope 是 jsonb 原文：passive={"host":...} / active={"brief":...}。取可读标签。
function scopeLabel(s: OwnerSummary): string {
  try {
    const o = JSON.parse(s.scope || '{}')
    return o.host || o.brief || s.scope || '—'
  } catch {
    return s.scope || '—'
  }
}

const statusColor: Record<string, string> = {
  active: 'var(--success)',
  aborted: 'var(--muted)',
  done: 'var(--primary)',
  error: 'var(--error)',
}
function statusText(st: string): string {
  return { active: '运行中', aborted: '已停止', done: '已完成', error: '出错' }[st] || st
}

// 相对时间（简洁）。
function ago(iso?: string): string {
  if (!iso) return ''
  const d = Date.now() - new Date(iso).getTime()
  const m = Math.floor(d / 60000)
  if (m < 1) return '刚刚'
  if (m < 60) return `${m} 分钟前`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h} 小时前`
  return `${Math.floor(h / 24)} 天前`
}

const passiveCount = computed(() => sessions.value.filter((s) => s.mode === 'passive').length)
const activeCount = computed(() => sessions.value.filter((s) => s.mode === 'active').length)

// 打开会话对话流（passive 插话入口）。
function openConversation(s: OwnerSummary) {
  if (!s.conversation_id) return
  router.push({ name: 'chat', query: { conv: s.conversation_id } })
}

async function stop(s: OwnerSummary) {
  try {
    await abortSession(s.id)
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '停止失败'
  }
}
</script>

<template>
  <div class="page">
    <div class="page-toolbar">
      <div class="tb-left">
        <h2 class="tb-title">被动会话</h2>
        <span class="tb-sub">流量驱动的渗透会话 · 可打开对话流插话指导</span>
      </div>
      <div class="tb-stats">
        <span class="stat"><b>{{ passiveCount }}</b> 被动</span>
        <span class="stat"><b>{{ activeCount }}</b> 主动</span>
        <button class="refresh-btn" :disabled="loading" @click="load">↻ 刷新</button>
      </div>
    </div>

    <div class="page-body">
      <div v-if="loading" class="state"><a-spin size="large" /></div>
      <div v-else-if="error" class="state"><span class="state-err">⚠ {{ error }}</span></div>
      <div v-else-if="!sessions.length" class="state">暂无会话——挂代理收流量或发起主动扫描后出现</div>

      <div v-else class="session-list">
        <div
          v-for="s in sessions"
          :key="s.id"
          class="session-card"
          :class="{ clickable: s.mode === 'passive' && s.conversation_id }"
          :style="{ '--st': statusColor[s.status] || 'var(--muted)' }"
          @click="s.mode === 'passive' && s.conversation_id && openConversation(s)"
        >
          <div class="sc-top">
            <span class="mode-chip" :class="s.mode">{{ s.mode === 'passive' ? '被动' : '主动' }}</span>
            <span class="scope">{{ scopeLabel(s) }}</span>
            <span class="status-dot" />
            <span class="status-text">{{ statusText(s.status) }}</span>
          </div>
          <div class="sc-meta">
            <span class="time">{{ ago(s.created_at) }}</span>
            <span v-if="s.error_message" class="err-msg">· {{ s.error_message }}</span>
            <span class="sc-actions" @click.stop>
              <button
                v-if="s.mode === 'passive' && s.conversation_id"
                class="act open"
                @click="openConversation(s)"
              >
                打开对话流 →
              </button>
              <button v-if="s.status === 'active'" class="act stop" @click="stop(s)">停止</button>
            </span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.tb-left { display: flex; flex-direction: column; gap: 2px; }
.tb-title { margin: 0; font-size: 16px; font-weight: 600; }
.tb-sub { font-size: 12px; color: var(--muted); }
.tb-stats { display: flex; align-items: center; gap: 14px; }
.stat { font-size: 13px; color: var(--muted); }
.stat b { color: var(--text); font-size: 16px; font-family: var(--mono); margin-right: 3px; }
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

.session-list { display: flex; flex-direction: column; gap: 10px; max-width: 920px; }
.session-card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-left: 3px solid var(--st);
  border-radius: var(--radius-lg);
  padding: 13px 16px;
  box-shadow: var(--shadow);
  transition: border-color 0.15s, transform 0.1s;
}
.session-card.clickable { cursor: pointer; }
.session-card.clickable:hover {
  border-color: var(--primary);
  border-left-color: var(--st);
  transform: translateY(-1px);
}
.sc-top { display: flex; align-items: center; gap: 10px; }
.mode-chip {
  font-size: 11px;
  font-weight: 700;
  padding: 2px 8px;
  border-radius: 5px;
  flex-shrink: 0;
}
.mode-chip.passive { background: var(--primary-soft); color: var(--primary); }
.mode-chip.active { background: rgba(114, 46, 209, 0.12); color: #722ed1; }
.scope {
  font-family: var(--mono);
  font-size: 13.5px;
  font-weight: 500;
  color: var(--text);
  word-break: break-all;
  flex: 1;
  min-width: 0;
}
.status-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--st); flex-shrink: 0; }
.status-text { font-size: 12px; color: var(--st); flex-shrink: 0; }
.sc-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
  font-size: 12px;
  color: var(--muted);
}
.err-msg { color: var(--error); }
.sc-actions { margin-left: auto; display: flex; gap: 8px; }
.act {
  border: 1px solid var(--border);
  background: transparent;
  border-radius: var(--radius);
  padding: 4px 12px;
  font-size: 12.5px;
  cursor: pointer;
}
.act.open { color: var(--primary); border-color: color-mix(in srgb, var(--primary) 40%, transparent); }
.act.open:hover { background: var(--primary-soft); }
.act.stop { color: var(--error); border-color: color-mix(in srgb, var(--error) 40%, transparent); }
.act.stop:hover { background: color-mix(in srgb, var(--error) 12%, transparent); }
</style>
