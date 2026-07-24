<script setup lang="ts">
// 漏洞详情抽屉：点台账某行→右侧滑出，展示完整 evidence(PoC/复现命令/观察)、修复建议、
// CWE/OWASP、聚合扫描次数、组合链，并支持 triage（状态 + 备注，两者解耦可单独存）。
// evidence 是 LLM 自由 jsonb（41 种 key），做通用 KV 渲染——命令类等宽代码块+复制，长文折行。
import { computed, ref, watch } from 'vue'
import type { FindingRow } from '../api/types'
import { severityColor } from '../lib/severity'
import { findingStatusMeta, FINDING_STATUS_OPTIONS } from '../lib/findingStatus'
import { fullTime } from '../lib/format'

const props = defineProps<{ open: boolean; finding: FindingRow | null }>()
const emit = defineEmits<{
  'update:open': [v: boolean]
  // 保存 triage：抛给父组件调 API（父持有列表，便于就地更新 + 乐观回滚）
  save: [payload: { id: string; status: string; severity: string; note: string }]
}>()

// 本地编辑态：抽屉打开/切换 finding 时，从当前 finding 初始化。
const editStatus = ref('')
const editSeverity = ref('')
const editNote = ref('')
watch(
  () => props.finding,
  (f) => {
    editStatus.value = f?.status ?? 'open'
    editSeverity.value = f?.severity ?? 'info'
    editNote.value = f?.triage_note ?? ''
  },
  { immediate: true },
)

const dirty = computed(
  () =>
    !!props.finding &&
    (editStatus.value !== props.finding.status ||
      editSeverity.value !== props.finding.severity ||
      editNote.value !== (props.finding.triage_note ?? '')),
)

const SEV_LABEL: Record<string, string> = {
  critical: '严重',
  high: '高危',
  medium: '中危',
  low: '低危',
  info: '信息',
}
const sevColorVar = computed(() => severityColor[(props.finding?.severity ?? 'info').toLowerCase()] ?? '#6e7681')
const sevLabel = computed(() => {
  const s = (props.finding?.severity ?? '').toLowerCase()
  return SEV_LABEL[s] || props.finding?.severity || ''
})
const statusMeta = computed(() => findingStatusMeta(editStatus.value))
const statusOptions = FINDING_STATUS_OPTIONS

function method(): string {
  return props.finding?.target?.method || ''
}
function path(): string {
  return props.finding?.target?.path || ''
}

// evidence 分类渲染：命令类 key（含 cmd/命令）用代码块 + 复制；对象/数组 JSON 折行；其余纯文本。
interface EvItem {
  key: string
  value: string
  kind: 'code' | 'text' | 'json'
}
const evidenceItems = computed<EvItem[]>(() => {
  const ev = props.finding?.evidence
  if (!ev || typeof ev !== 'object') return []
  const items: EvItem[] = []
  for (const [k, raw] of Object.entries(ev)) {
    if (raw == null || raw === '') continue
    let kind: EvItem['kind'] = 'text'
    let value: string
    if (typeof raw === 'object') {
      kind = 'json'
      value = JSON.stringify(raw, null, 2)
    } else {
      value = String(raw)
      // 命令/请求类 → 等宽代码块（便于复制复现）
      if (/cmd|command|curl|repro|request|payload/i.test(k)) kind = 'code'
    }
    items.push({ key: k, value, kind })
  }
  return items
})

// evidence key → 人类可读中文标签（常见 key 映射，未知 key 原样）。
const EV_LABELS: Record<string, string> = {
  repro_cmd: '复现命令',
  repro_cmd_time: '复现命令（时间盲注）',
  repro_cmd_boolean: '复现命令（布尔盲注）',
  repro_steps: '复现步骤',
  repro_response: '复现响应',
  payload: 'Payload',
  observation: '观察',
  key_observation: '关键观察',
  conclusion: '结论',
  impact: '影响',
  analysis: '分析',
  description: '描述',
  result: '结果',
  issue: '问题',
  time_delay: '时间延迟',
  response_excerpt: '响应片段',
  response_body: '响应体',
  response_status: '响应状态',
  vulnerable_endpoints: '受影响端点',
  affected_users: '受影响用户',
}
function evLabel(k: string): string {
  return EV_LABELS[k] || k
}

const copiedKey = ref('')
async function copy(item: EvItem) {
  try {
    await navigator.clipboard.writeText(item.value)
    copiedKey.value = item.key
    setTimeout(() => {
      if (copiedKey.value === item.key) copiedKey.value = ''
    }, 1500)
  } catch {
    // 剪贴板不可用（非安全上下文）时静默——用户仍可手动选中复制
  }
}

function close() {
  emit('update:open', false)
}
function onSave() {
  if (!props.finding || !dirty.value) return
  emit('save', {
    id: props.finding.id,
    status: editStatus.value,
    severity: editSeverity.value,
    note: editNote.value,
  })
}

// severity 选项（人工可覆盖 LLM 定级）。
const SEVERITY_OPTIONS = ['critical', 'high', 'medium', 'low', 'info']
</script>

<template>
  <a-drawer
    :open="open"
    placement="right"
    :width="560"
    :closable="false"
    :body-style="{ padding: '0' }"
    @close="close"
  >
    <div v-if="finding" class="fd">
      <!-- 头部：severity + summary + 模式 + 定位 -->
      <div class="fd-head" :style="{ '--sev': sevColorVar }">
        <div class="fd-head-top">
          <span class="fd-sev" :class="'sev-' + finding.severity.toLowerCase()">{{ sevLabel }}</span>
          <span class="fd-mode" :class="'mode-' + finding.mode">{{ finding.mode === 'passive' ? '流量分析' : '渗透会话' }}</span>
          <button class="fd-close" title="关闭" @click="close">✕</button>
        </div>
        <h2 class="fd-summary">{{ finding.summary }}</h2>
        <div class="fd-loc mono">
          <span v-if="method()" class="fd-method">{{ method() }}</span>
          {{ finding.host }}{{ path() }}
        </div>
      </div>

      <div class="fd-body">
        <!-- Triage 区：状态 + 备注（解耦，dirty 才亮保存） -->
        <section class="fd-sec fd-triage">
          <h3 class="fd-sec-title">处置</h3>
          <div class="fd-triage-row">
            <label class="fd-field-label">状态</label>
            <span class="fd-dot" :style="{ background: statusMeta.color }" />
            <a-select v-model:value="editStatus" style="width: 140px">
              <a-select-option v-for="o in statusOptions" :key="o.value" :value="o.value">{{ o.label }}</a-select-option>
            </a-select>
          </div>
          <div class="fd-triage-row">
            <label class="fd-field-label">严重度</label>
            <a-select v-model:value="editSeverity" style="width: 140px">
              <a-select-option v-for="s in SEVERITY_OPTIONS" :key="s" :value="s">{{ s }}</a-select-option>
            </a-select>
            <span class="fd-sev-hint">可覆盖扫描定级</span>
          </div>
          <a-textarea
            v-model:value="editNote"
            :rows="2"
            placeholder="处置备注（如误报原因、修复责任人、验证方式…）"
            class="fd-note-input"
          />
          <div class="fd-triage-actions">
            <span v-if="finding.triaged_at" class="fd-triaged-at">最后处置 {{ fullTime(finding.triaged_at) }}</span>
            <button class="fd-save" :disabled="!dirty" @click="onSave">保存</button>
          </div>
        </section>

        <!-- Evidence：通用 KV 渲染 -->
        <section v-if="evidenceItems.length" class="fd-sec">
          <h3 class="fd-sec-title">证据 / 复现<span class="muted">{{ evidenceItems.length }} 项</span></h3>
          <div v-for="item in evidenceItems" :key="item.key" class="fd-ev">
            <div class="fd-ev-head">
              <span class="fd-ev-key">{{ evLabel(item.key) }}</span>
              <button v-if="item.kind === 'code'" class="fd-copy" @click="copy(item)">
                {{ copiedKey === item.key ? '✓ 已复制' : '复制' }}
              </button>
            </div>
            <pre v-if="item.kind === 'code' || item.kind === 'json'" class="fd-code">{{ item.value }}</pre>
            <div v-else class="fd-ev-text">{{ item.value }}</div>
          </div>
        </section>

        <!-- 修复建议 -->
        <section v-if="finding.remediation" class="fd-sec">
          <h3 class="fd-sec-title">修复建议</h3>
          <div class="fd-remediation">{{ finding.remediation }}</div>
        </section>

        <!-- 元信息 -->
        <section class="fd-sec">
          <h3 class="fd-sec-title">元信息</h3>
          <div class="fd-meta-grid">
            <template v-if="finding.cwe_id"><span class="fd-mk">CWE</span><span class="fd-mv mono">{{ finding.cwe_id }}</span></template>
            <template v-if="finding.owasp_category"><span class="fd-mk">OWASP</span><span class="fd-mv mono">{{ finding.owasp_category }}</span></template>
            <span class="fd-mk">首次发现</span><span class="fd-mv mono">{{ fullTime(finding.created_at) }}</span>
          </div>
        </section>
      </div>
    </div>
  </a-drawer>
</template>

<style scoped>
.fd { display: flex; flex-direction: column; height: 100%; }
/* 头部：severity 色驱动的左边框 + 渐隐染色 */
.fd-head {
  padding: 18px 20px 16px;
  border-bottom: 1px solid var(--border);
  border-left: 4px solid var(--sev, var(--sev-high));
  background: linear-gradient(90deg, color-mix(in srgb, var(--sev) 8%, transparent), transparent 60%);
}
.fd-head-top { display: flex; align-items: center; gap: 10px; }
/* severity 渐变实心 pill（对齐台账风格），critical 带轻微发光 */
.fd-sev {
  font-size: 12px;
  font-weight: 700;
  padding: 3px 12px;
  border-radius: 999px;
  color: #fff;
  letter-spacing: 0.02em;
}
.fd-sev.sev-critical { background: linear-gradient(135deg, var(--sev-critical), #b91c1c); box-shadow: 0 0 12px color-mix(in srgb, var(--sev-critical) 45%, transparent); }
.fd-sev.sev-high { background: linear-gradient(135deg, var(--sev-high), #c2410c); }
.fd-sev.sev-medium { background: linear-gradient(135deg, var(--sev-medium), #a16207); color: #1a1400; }
.fd-sev.sev-low { background: var(--sev-low); }
.fd-sev.sev-info { background: var(--sev-info); }
.fd-mode { font-size: 11px; font-weight: 600; border-radius: 4px; padding: 1px 8px; }
.mode-active { color: var(--mode-active); background: color-mix(in srgb, var(--mode-active) 14%, transparent); }
.mode-passive { color: var(--mode-passive); background: color-mix(in srgb, var(--mode-passive) 14%, transparent); }
.fd-close {
  margin-left: auto;
  background: transparent;
  border: none;
  color: var(--muted);
  font-size: 16px;
  cursor: pointer;
  line-height: 1;
}
.fd-close:hover { color: var(--text); }
.fd-summary { margin: 12px 0 8px; font-size: 16px; line-height: 1.5; font-weight: 600; color: var(--text); }
.fd-loc { font-size: 12.5px; color: var(--muted); word-break: break-all; }
.fd-method { color: var(--accent); font-weight: 700; margin-right: 5px; }

.fd-body { flex: 1; min-height: 0; overflow-y: auto; padding: 18px 20px; display: flex; flex-direction: column; gap: 22px; }
.fd-sec-title {
  margin: 0 0 10px;
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
  display: flex;
  align-items: baseline;
  gap: 8px;
}
.fd-sec-title .muted { font-size: 11px; font-weight: 400; color: var(--muted); }

/* Triage 区 */
.fd-triage { background: var(--surface-2); border-radius: var(--radius-lg, 12px); padding: 14px; }
.fd-triage-row { display: flex; align-items: center; gap: 8px; margin-bottom: 10px; }
.fd-field-label { font-size: 12px; color: var(--muted); width: 44px; flex-shrink: 0; }
.fd-sev-hint { font-size: 11px; color: var(--muted); }
.fd-dot { width: 9px; height: 9px; border-radius: 50%; flex-shrink: 0; }
.fd-note-input { font-size: 13px; }
.fd-triage-actions { display: flex; align-items: center; justify-content: space-between; margin-top: 10px; gap: 12px; }
.fd-triaged-at { font-size: 11.5px; color: var(--muted); font-family: var(--mono); }
.fd-save {
  margin-left: auto;
  background: var(--primary);
  color: #fff;
  border: none;
  border-radius: var(--radius, 8px);
  padding: 6px 18px;
  font-size: 13px;
  cursor: pointer;
}
.fd-save:hover:not(:disabled) { background: var(--primary-hover); }
.fd-save:disabled { opacity: 0.4; cursor: default; }

/* Evidence */
.fd-ev { margin-bottom: 12px; }
.fd-ev:last-child { margin-bottom: 0; }
.fd-ev-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 4px; }
.fd-ev-key { font-size: 12px; font-weight: 600; color: var(--muted); }
.fd-copy {
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 5px;
  color: var(--muted);
  font-size: 11px;
  padding: 1px 8px;
  cursor: pointer;
}
.fd-copy:hover { border-color: var(--primary); color: var(--primary); }
.fd-code {
  margin: 0;
  padding: 10px 12px;
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius, 8px);
  font-family: var(--mono);
  font-size: 12px;
  line-height: 1.55;
  color: var(--text);
  white-space: pre-wrap;
  word-break: break-all;
  overflow-x: auto;
}
.fd-ev-text { font-size: 13px; line-height: 1.6; color: var(--text); white-space: pre-wrap; }

.fd-remediation {
  font-size: 13px;
  line-height: 1.65;
  color: var(--text);
  padding: 12px 14px;
  background: color-mix(in srgb, var(--sev-info, #38bdf8) 8%, transparent);
  border-left: 3px solid var(--sev-info, #38bdf8);
  border-radius: var(--radius, 8px);
}

.fd-meta-grid { display: grid; grid-template-columns: auto 1fr; gap: 6px 16px; font-size: 12.5px; }
.fd-mk { color: var(--muted); }
.fd-mv { color: var(--text); }

</style>
