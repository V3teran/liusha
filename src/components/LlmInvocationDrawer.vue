<script setup lang="ts">
// LLM 调用详情抽屉：点审计表某行 → 右侧滑出，展示元信息 KV + 输入消息 + 返回结果原文。
//
// 对齐业界日志详情弹窗（NewAPI details-dialog）：分节 + KV 网格 + 逐项复制，
// 而非把 messages/result 直接 JSON.stringify 成一坨裸 dump。
// messages 是 OpenAI 风格消息数组，按条渲染（role 标签 + 正文）；结构不符时退化为 JSON 展示。
import { computed, ref, watch } from 'vue'
import type { LLMInvocationDetail } from '../api/types'
import { agentAccent } from '../lib/agentColor'
import { fullTime, humanDuration, humanTokens } from '../lib/format'

const props = defineProps<{
  open: boolean
  detail: LLMInvocationDetail | null
  loading: boolean
  error: string
}>()
const emit = defineEmits<{ 'update:open': [v: boolean] }>()
const close = () => emit('update:open', false)

// 一条消息的规范化视图；content 可能是字符串或多模态数组，统一成可读文本。
interface MsgView {
  role: string
  text: string
}

function contentToText(c: unknown): string {
  if (typeof c === 'string') return c
  if (Array.isArray(c)) {
    // 多模态：[{type:'text',text:...}, {type:'image_url',...}] → 取文本片段，非文本标注类型
    return c
      .map((part) => {
        if (typeof part === 'string') return part
        const p = part as Record<string, unknown>
        if (typeof p.text === 'string') return p.text
        return p.type ? `[${String(p.type)}]` : ''
      })
      .filter(Boolean)
      .join('\n')
  }
  if (c == null) return ''
  return JSON.stringify(c, null, 2)
}

// 输入消息：期望数组，逐条转视图；结构不符返回空数组（由模板回退到 JSON 视图）。
const messages = computed<MsgView[]>(() => {
  const raw = props.detail?.messages
  if (!Array.isArray(raw)) return []
  return raw.map((m) => {
    const o = (m ?? {}) as Record<string, unknown>
    const text = contentToText(o.content)
    // 工具调用没有 content，正文落在 tool_calls 上——否则这条消息会显示成空白。
    const calls = o.tool_calls ?? o.function_call
    return {
      role: typeof o.role === 'string' ? o.role : 'unknown',
      text: text || (calls ? JSON.stringify(calls, null, 2) : ''),
    }
  })
})

// 返回结果：正文 + 工具调用分开看（result 是单条 assistant 消息）。
const resultText = computed(() => {
  const r = props.detail?.result
  if (r == null) return ''
  if (typeof r === 'string') return r
  const o = r as Record<string, unknown>
  return contentToText(o.content)
})
const resultCalls = computed(() => {
  const r = props.detail?.result as Record<string, unknown> | null | undefined
  if (!r) return ''
  const calls = r.tool_calls ?? r.function_call
  return calls ? JSON.stringify(calls, null, 2) : ''
})
// 兜底：result 既无 content 也无 tool_calls 时（结构与预期不同），整体 JSON 展示，不让详情页空白。
const resultRaw = computed(() =>
  !resultText.value && !resultCalls.value && props.detail?.result != null
    ? JSON.stringify(props.detail.result, null, 2)
    : '',
)
const messagesRaw = computed(() =>
  !messages.value.length && props.detail?.messages != null
    ? JSON.stringify(props.detail.messages, null, 2)
    : '',
)

// 长对话（实测单次调用可带 139 条历史消息）不能一次全渲染：
// 141 个 <pre> 既压 DOM 又没法扫视。默认只渲染尾部 INITIAL_MSGS 条——
// 尾部才是本次调用的即时上下文，头部是早已压缩过的历史，按需再展开。
const INITIAL_MSGS = 20
const showAllMsgs = ref(false)
const visibleMessages = computed(() =>
  showAllMsgs.value || messages.value.length <= INITIAL_MSGS
    ? messages.value
    : messages.value.slice(-INITIAL_MSGS),
)
const hiddenMsgCount = computed(() => messages.value.length - visibleMessages.value.length)
// 换行/换条目时收起展开态，否则上一条的"已展开"会带到下一条（长对话直接卡）。
watch(
  () => props.detail?.id,
  () => (showAllMsgs.value = false),
)

const roleColor = computed(() => agentAccent(props.detail?.role).accent)

// 复制：按 key 记录反馈态。
const copiedKey = ref('')
async function copy(key: string, text: string) {
  try {
    await navigator.clipboard.writeText(text)
    copiedKey.value = key
    setTimeout(() => (copiedKey.value = key === copiedKey.value ? '' : copiedKey.value), 1600)
  } catch {
    // 剪贴板不可用（非 https / 无权限）：静默，不打断查看
  }
}

// 消息 role 配色：区分 system/user/assistant/tool，让长对话可扫视。
const MSG_ROLE_COLOR: Record<string, string> = {
  system: '#94a3b8',
  user: '#38bdf8',
  assistant: '#34d399',
  tool: '#a78bfa',
}
const msgColor = (role: string) => MSG_ROLE_COLOR[role] ?? '#94a3b8'
</script>

<template>
  <a-drawer
    :open="open"
    placement="right"
    :width="620"
    :closable="false"
    :body-style="{ padding: '0' }"
    @close="close"
  >
    <div class="ld">
      <div class="ld-head" :style="{ '--role': roleColor }">
        <div class="ld-head-top">
          <span class="ld-role">{{ detail?.role || '调用详情' }}</span>
          <span v-if="detail?.error_message" class="ld-badge bad">失败</span>
          <span v-else-if="detail" class="ld-badge">{{ detail.finish_reason || '完成' }}</span>
          <button class="ld-close" title="关闭" @click="close">✕</button>
        </div>
        <div v-if="detail" class="ld-model mono">{{ detail.model }}<span class="ld-provider"> · {{ detail.provider }}</span></div>
      </div>

      <div class="ld-body">
        <div v-if="loading" class="ld-state"><a-spin /></div>
        <div v-else-if="error" class="ld-state err">⚠ {{ error }}</div>

        <template v-else-if="detail">
          <!-- 元信息 KV -->
          <section class="ld-sec">
            <h3 class="ld-sec-title">元信息</h3>
            <div class="ld-meta">
              <span class="mk">request_id</span>
              <span class="mv mono copyable">
                {{ detail.request_id }}
                <button class="ld-copy" @click="copy('rid', detail.request_id)">
                  {{ copiedKey === 'rid' ? '✓' : '复制' }}
                </button>
              </span>
              <span class="mk">时间</span><span class="mv mono">{{ fullTime(detail.created_at) }}</span>
              <span class="mk">Tokens</span>
              <span class="mv mono">
                输入 {{ humanTokens(detail.in_tokens) }} · 输出 {{ humanTokens(detail.out_tokens) }}
                <template v-if="detail.cached_tokens > 0"> · 缓存 {{ humanTokens(detail.cached_tokens) }}</template>
              </span>
              <span class="mk">延迟</span><span class="mv mono">{{ humanDuration(detail.latency_ms) }}</span>
              <template v-if="detail.hunter_id">
                <span class="mk">hunter</span><span class="mv mono">{{ detail.hunter_id }}</span>
              </template>
            </div>
          </section>

          <!-- 错误单独成节，最先看到 -->
          <section v-if="detail.error_message" class="ld-sec">
            <h3 class="ld-sec-title">错误</h3>
            <pre class="ld-code bad">{{ detail.error_message }}</pre>
          </section>

          <!-- 输入消息：按条渲染 -->
          <section class="ld-sec">
            <h3 class="ld-sec-title">
              输入消息<span class="muted">{{ messages.length || '' }}{{ messages.length ? ' 条' : '' }}</span>
              <button class="ld-copy right" @click="copy('msgs', JSON.stringify(detail.messages, null, 2))">
                {{ copiedKey === 'msgs' ? '✓ 已复制' : '复制 JSON' }}
              </button>
            </h3>
            <!-- 长对话默认只渲染尾部若干条（尾部=本次调用的即时上下文），头部按需展开 -->
            <button v-if="hiddenMsgCount > 0" class="ld-more" @click="showAllMsgs = true">
              ▾ 展开更早的 {{ hiddenMsgCount }} 条历史
            </button>
            <div v-for="(m, i) in visibleMessages" :key="i" class="ld-msg" :style="{ '--mc': msgColor(m.role) }">
              <div class="ld-msg-head"><span class="ld-msg-role">{{ m.role }}</span></div>
              <pre class="ld-code">{{ m.text || '(空)' }}</pre>
            </div>
            <pre v-if="messagesRaw" class="ld-code">{{ messagesRaw }}</pre>
            <div v-if="!messages.length && !messagesRaw" class="ld-empty">无输入消息</div>
          </section>

          <!-- 返回结果 -->
          <section class="ld-sec">
            <h3 class="ld-sec-title">
              返回结果
              <button class="ld-copy right" @click="copy('res', JSON.stringify(detail.result, null, 2))">
                {{ copiedKey === 'res' ? '✓ 已复制' : '复制 JSON' }}
              </button>
            </h3>
            <pre v-if="resultText" class="ld-code">{{ resultText }}</pre>
            <template v-if="resultCalls">
              <p class="ld-sub-label">工具调用</p>
              <pre class="ld-code">{{ resultCalls }}</pre>
            </template>
            <pre v-if="resultRaw" class="ld-code">{{ resultRaw }}</pre>
            <div v-if="!resultText && !resultCalls && !resultRaw" class="ld-empty">无返回内容</div>
          </section>
        </template>
      </div>
    </div>
  </a-drawer>
</template>

<style scoped>
.ld { display: flex; flex-direction: column; height: 100%; }
.ld-head {
  padding: 16px 20px 14px;
  border-bottom: 1px solid var(--border);
  border-left: 4px solid var(--role, var(--primary));
  background: linear-gradient(90deg, color-mix(in srgb, var(--role) 8%, transparent), transparent 60%);
}
.ld-head-top { display: flex; align-items: center; gap: 10px; }
.ld-role { font-size: 14px; font-weight: 700; color: var(--role); }
.ld-badge {
  font-size: 11px;
  font-weight: 600;
  padding: 1px 8px;
  border-radius: 5px;
  color: var(--muted);
  background: var(--surface-2);
}
.ld-badge.bad { color: var(--sev-critical); background: color-mix(in srgb, var(--sev-critical) 14%, transparent); }
.ld-close { margin-left: auto; background: transparent; border: none; color: var(--muted); font-size: 16px; cursor: pointer; line-height: 1; }
.ld-close:hover { color: var(--text); }
.ld-model { margin-top: 8px; font-size: 12.5px; color: var(--text); }
.ld-provider { color: var(--muted); }

.ld-body { flex: 1; min-height: 0; overflow-y: auto; padding: 18px 20px; display: flex; flex-direction: column; gap: 22px; }
.ld-state { text-align: center; color: var(--muted); padding: 40px 0; font-size: 13px; }
.ld-state.err { color: var(--sev-critical); }
.ld-sec-title {
  margin: 0 0 10px;
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
  display: flex;
  align-items: baseline;
  gap: 8px;
}
.ld-sec-title .muted { font-size: 11px; font-weight: 400; color: var(--muted); }
.ld-sub-label { margin: 10px 0 5px; font-size: 11.5px; color: var(--muted); }
.ld-empty { font-size: 12.5px; color: var(--muted); opacity: 0.7; }

/* 元信息 KV 网格 */
.ld-meta { display: grid; grid-template-columns: 84px 1fr; gap: 7px 12px; align-items: baseline; }
.mk { font-size: 12px; color: var(--muted); }
.mv { font-size: 12.5px; color: var(--text); word-break: break-all; }
.copyable { display: flex; align-items: center; gap: 8px; }

.mono { font-family: var(--mono); font-variant-numeric: tabular-nums; }

/* 消息条：左侧 role 色条 */
.ld-msg { margin-bottom: 10px; border-left: 3px solid var(--mc); padding-left: 10px; }
.ld-msg:last-child { margin-bottom: 0; }
.ld-msg-head { margin-bottom: 4px; }
.ld-msg-role { font-size: 11px; font-weight: 700; color: var(--mc); text-transform: uppercase; letter-spacing: 0.04em; }

.ld-code {
  margin: 0;
  padding: 9px 11px;
  max-height: 320px;
  overflow: auto;
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius, 8px);
  font-family: var(--mono);
  font-size: 11.5px;
  line-height: 1.55;
  color: var(--text);
  white-space: pre-wrap;
  word-break: break-word;
}
.ld-code.bad { color: var(--sev-critical); border-color: color-mix(in srgb, var(--sev-critical) 40%, var(--border)); }

.ld-copy {
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 5px;
  color: var(--muted);
  font-size: 11px;
  padding: 1px 8px;
  cursor: pointer;
  flex-shrink: 0;
}
.ld-copy:hover { border-color: var(--primary); color: var(--primary); }
.ld-copy.right { margin-left: auto; }

/* 长对话历史展开入口 */
.ld-more {
  width: 100%;
  margin-bottom: 10px;
  padding: 5px 0;
  background: var(--surface-2);
  border: 1px dashed var(--border);
  border-radius: var(--radius, 8px);
  color: var(--muted);
  font-size: 11.5px;
  cursor: pointer;
}
.ld-more:hover { color: var(--primary); border-color: var(--primary); }
</style>
