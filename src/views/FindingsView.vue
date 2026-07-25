<script setup lang="ts">
// 漏洞管理页（全局台账）：跨 task/host 展示所有漏洞（active + passive），支持 triage 处置流转。
// 布局：顶部 severity 堆叠占比汇总条（大总数 + 可点图例筛选）+ 搜索/筛选栏 + 密集表格行。
// 每行整行可点 → 右侧详情抽屉（evidence/PoC/修复建议/状态·严重度·备注编辑）；行尾 › 展开提示。
// 修复历史缺陷：旧版走 /sitemap（仅 active）+ 必选 owner，2/3 的 passive 漏洞不可见。
import { computed, onMounted, ref } from 'vue'
import FindingDrawer from '../components/FindingDrawer.vue'
import { listFindings, updateFindingTriage } from '../api/client'
import type { FindingRow, FindingFilters } from '../api/types'
import { severityColor, severityRank } from '../lib/severity'
import { findingStatusMeta, FINDING_STATUS_OPTIONS } from '../lib/findingStatus'

const rows = ref<FindingRow[]>([])
const loading = ref(false)
const error = ref('')

// 筛选态（空=不筛该维度）。改任一即重拉（后端筛，避免前端持有全量再过滤的状态漂移）。
const fHost = ref('')
const fSeverity = ref('')
const fStatus = ref('')
const fMode = ref('')
const query = ref('') // 前端标题/host/path 模糊搜索（列表已全量，纯前端过滤够用）

async function load() {
  loading.value = true
  error.value = ''
  try {
    const filters: FindingFilters = {}
    if (fHost.value) filters.host = fHost.value
    if (fSeverity.value) filters.severity = fSeverity.value
    if (fStatus.value) filters.status = fStatus.value
    if (fMode.value) filters.mode = fMode.value
    rows.value = await listFindings(filters)
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}
onMounted(load)

// host 下拉选项来自一份全量快照（不受当前筛选收窄影响，保持稳定）。
const allHosts = ref<string[]>([])
async function primeHosts() {
  try {
    const all = await listFindings({})
    allHosts.value = [...new Set(all.map((f) => f.host))].sort()
  } catch {
    // 静默：host 下拉是增强项，失败则退化为空
  }
}
onMounted(primeHosts)

const SEVS = ['critical', 'high', 'medium', 'low', 'info'] as const
const SEV_LABEL: Record<string, string> = {
  critical: '严重',
  high: '高危',
  medium: '中危',
  low: '低危',
  info: '信息',
}

// 前端搜索过滤后的行（后端已按维度筛，这里叠加标题/host/path 模糊）。
const visible = computed(() => {
  const q = query.value.trim().toLowerCase()
  const base = q
    ? rows.value.filter((f) =>
        `${f.summary}${f.host}${f.target?.path ?? ''}`.toLowerCase().includes(q),
      )
    : rows.value
  return [...base].sort((a, b) => severityRank(a.severity) - severityRank(b.severity))
})

// severity 计数（堆叠条 + 图例），基于当前可见集。
const sevCounts = computed(() =>
  SEVS.map((s) => ({ sev: s, n: visible.value.filter((f) => f.severity.toLowerCase() === s).length })),
)

// 行动摘要：总数 / 待处理 / 已确认。
const summary = computed(() => ({
  total: visible.value.length,
  open: visible.value.filter((f) => f.status === 'open').length,
  confirmed: visible.value.filter((f) => f.status === 'confirmed').length,
}))

const statusOptions = FINDING_STATUS_OPTIONS

// 点堆叠条段 / 图例 = 按该 severity 筛选（后端重拉；再点取消）。
function toggleSevFilter(sev: string) {
  fSeverity.value = fSeverity.value === sev ? '' : sev
  load()
}

const savingId = ref('')
function applyUpdated(updated: FindingRow) {
  rows.value = rows.value.map((x) => (x.id === updated.id ? { ...x, ...updated } : x))
  if (drawerFinding.value?.id === updated.id) drawerFinding.value = { ...drawerFinding.value, ...updated }
  if (fStatus.value && fStatus.value !== updated.status) load()
}

// —— 详情抽屉 ——
const drawerOpen = ref(false)
const drawerFinding = ref<FindingRow | null>(null)
function openDrawer(f: FindingRow) {
  drawerFinding.value = f
  drawerOpen.value = true
}
// 抽屉内保存 triage（状态 + 严重度 + 备注一起）：乐观更新 + 用返回行覆盖 / 失败回滚。
async function saveTriage(payload: { id: string; status: string; severity: string; note: string }) {
  const before = rows.value.find((x) => x.id === payload.id)
  savingId.value = payload.id
  rows.value = rows.value.map((x) =>
    x.id === payload.id
      ? {
          ...x,
          status: payload.status,
          severity: payload.severity,
          triage_note: payload.note,
          triaged_at: new Date().toISOString(),
        }
      : x,
  )
  try {
    applyUpdated(await updateFindingTriage(payload.id, payload.status, payload.severity, payload.note))
  } catch {
    if (before) rows.value = rows.value.map((x) => (x.id === payload.id ? before : x))
    window.alert('处置保存失败，请重试')
  } finally {
    savingId.value = ''
  }
}

function methodOf(f: FindingRow): string {
  return f.target?.method || ''
}
function pathOf(f: FindingRow): string {
  return f.target?.path || ''
}
function sevVar(sev: string): string {
  return severityColor[sev.toLowerCase()] ?? '#6e7681'
}
</script>

<template>
  <div class="fm-page">
    <div class="fm-body">
      <div v-if="loading" class="fm-state"><a-spin size="large" /></div>
      <div v-else-if="error" class="fm-state err">⚠ {{ error }}</div>
      <div v-else-if="!rows.length" class="fm-state">
        {{ fHost || fSeverity || fStatus || fMode ? '当前筛选无匹配漏洞' : '暂无漏洞——发起扫描或挂代理收流量后，AI 挖到的漏洞会汇总到此' }}
      </div>

      <template v-else>
        <!-- 顶部汇总条：大总数 + severity 堆叠占比条 + 可点图例 -->
        <div class="fm-summary">
          <div class="sm-total">
            <span class="sm-total-n">{{ summary.total }}</span>
            <span class="sm-total-l">漏洞总数<br /><em>{{ summary.open }} 待处理 · {{ summary.confirmed }} 已确认</em></span>
          </div>
          <div class="sm-bar-wrap">
            <div class="sm-bar">
              <div
                v-for="x in sevCounts.filter((c) => c.n)"
                :key="x.sev"
                class="sm-seg"
                :class="{ on: fSeverity === x.sev }"
                :style="{ flex: x.n, background: sevVar(x.sev), color: sevVar(x.sev) }"
                :title="`${SEV_LABEL[x.sev]} ${x.n}`"
                @click="toggleSevFilter(x.sev)"
              />
            </div>
            <div class="sm-legend">
              <span
                v-for="x in sevCounts"
                :key="x.sev"
                class="lg"
                :class="{ on: fSeverity === x.sev, empty: !x.n }"
                @click="x.n && toggleSevFilter(x.sev)"
              >
                <i :style="{ background: sevVar(x.sev), color: sevVar(x.sev) }" />{{ SEV_LABEL[x.sev] }} <b>{{ x.n }}</b>
              </span>
            </div>
          </div>
        </div>

        <!-- 搜索 + 筛选栏 -->
        <div class="fm-toolbar">
          <div class="fm-search">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="7" /><path d="m21 21-4.3-4.3" /></svg>
            <input v-model="query" type="search" placeholder="搜索漏洞标题 / host / 路径…" spellcheck="false" />
          </div>
          <a-select v-model:value="fMode" style="width: 120px" @change="load">
            <a-select-option value="">全部来源</a-select-option>
            <a-select-option value="active">渗透会话</a-select-option>
            <a-select-option value="passive">流量分析</a-select-option>
          </a-select>
          <a-select v-model:value="fStatus" style="width: 120px" @change="load">
            <a-select-option value="">全部状态</a-select-option>
            <a-select-option v-for="o in statusOptions" :key="o.value" :value="o.value">{{ o.label }}</a-select-option>
          </a-select>
          <a-select v-model:value="fHost" style="width: 180px" show-search @change="load">
            <a-select-option value="">全部 host</a-select-option>
            <a-select-option v-for="h in allHosts" :key="h" :value="h">{{ h }}</a-select-option>
          </a-select>
          <span class="fm-count">{{ visible.length }} 条</span>
        </div>

        <!-- 密集表格 -->
        <div class="fm-table">
          <div class="fr fr-head">
            <span>严重度</span><span>漏洞</span><span>位置</span><span>来源</span><span>状态</span><span></span>
          </div>
          <div
            v-for="f in visible"
            :key="f.id"
            class="fr"
            :class="{ saving: savingId === f.id }"
            :style="{ '--sev': sevVar(f.severity) }"
            role="button"
            tabindex="0"
            :aria-label="`查看漏洞详情：${f.summary}`"
            @click="openDrawer(f)"
            @keydown.enter.prevent="openDrawer(f)"
            @keydown.space.prevent="openDrawer(f)"
          >
            <span class="fr-sev"><i :style="{ background: sevVar(f.severity), color: sevVar(f.severity) }" />{{ SEV_LABEL[f.severity.toLowerCase()] || f.severity }}</span>
            <span class="fr-sum">
              {{ f.summary }}
              <span v-if="f.triage_note" class="fr-note" title="有处置备注">📝</span>
            </span>
            <span class="fr-loc mono">{{ methodOf(f) }} {{ f.host }}{{ pathOf(f) }}<template v-if="f.cwe_id"> · {{ f.cwe_id }}</template></span>
            <span class="fr-mode" :class="f.mode">{{ f.mode === 'passive' ? '流量' : '渗透' }}</span>
            <span class="fr-st" :style="{ '--st': findingStatusMeta(f.status).color }"><i />{{ findingStatusMeta(f.status).label }}</span>
            <span class="fr-arrow">›</span>
          </div>
        </div>
      </template>
    </div>

    <FindingDrawer v-model:open="drawerOpen" :finding="drawerFinding" @save="saveTriage" />
  </div>
</template>

<style scoped>
.fm-page { height: 100%; display: flex; flex-direction: column; min-height: 0; }
.fm-body { flex: 1; min-height: 0; overflow-y: auto; padding: 22px; }
.fm-state { text-align: center; color: var(--muted); padding: 60px 0; font-size: 13.5px; }
.fm-state.err { color: var(--error); }

/* 顶部汇总条 */
.fm-summary {
  display: flex;
  align-items: center;
  gap: 32px;
  padding: 18px 22px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 18px;
  box-shadow: var(--shadow);
  margin-bottom: 16px;
}
.sm-total { display: flex; align-items: center; gap: 14px; flex-shrink: 0; }
/* 大数字：纯色（去掉渐变文字装饰，纯色更清晰） */
.sm-total-n {
  font-size: 48px;
  font-weight: 800;
  line-height: 1;
  letter-spacing: -0.03em;
  font-variant-numeric: tabular-nums;
  color: var(--text);
}
.sm-total-l { font-size: 13px; color: var(--muted); line-height: 1.5; }
.sm-total-l em { font-style: normal; font-size: 12px; color: var(--faint, var(--muted)); }
.sm-bar-wrap { flex: 1; min-width: 0; }
.sm-bar { display: flex; height: 16px; border-radius: 999px; overflow: hidden; gap: 3px; background: color-mix(in srgb, var(--surface-2) 60%, transparent); }
/* severity 色段带辉光——deep 玻璃主题的关键视觉 */
.sm-seg { transition: flex 0.3s, opacity 0.15s; cursor: pointer; border-radius: 3px; box-shadow: 0 0 12px -2px currentColor; }
.sm-seg:hover { opacity: 0.82; }
.sm-seg.on { box-shadow: inset 0 0 0 2px var(--text), 0 0 12px -2px currentColor; }
.sm-legend { display: flex; flex-wrap: wrap; gap: 8px 18px; margin-top: 12px; font-size: 12.5px; color: var(--muted); }
.sm-legend .lg { cursor: pointer; padding: 2px 8px; border-radius: 7px; transition: 0.14s; user-select: none; }
.sm-legend .lg:hover { background: var(--surface-2); }
.sm-legend .lg.on { background: var(--surface-2); color: var(--text); box-shadow: inset 0 0 0 1px var(--border-strong); }
.sm-legend .lg.empty { opacity: 0.4; cursor: default; }
.sm-legend i { display: inline-block; width: 9px; height: 9px; border-radius: 3px; margin-right: 6px; vertical-align: -1px; box-shadow: 0 0 8px -1px; }
.sm-legend b { color: var(--text); font-variant-numeric: tabular-nums; }

/* 搜索 + 筛选栏 */
.fm-toolbar { display: flex; align-items: center; gap: 10px; margin-bottom: 14px; }
.fm-search {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 12px;
  height: 36px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 10px;
}
.fm-search svg { width: 16px; height: 16px; color: var(--muted); flex-shrink: 0; }
.fm-search input { flex: 1; background: none; border: none; outline: none; color: var(--text); font-size: 13.5px; }
.fm-search input::placeholder { color: var(--muted); opacity: 0.7; }
.fm-count { font-size: 12.5px; color: var(--muted); font-variant-numeric: tabular-nums; padding-left: 2px; flex-shrink: 0; }

/* 密集表格 */
.fm-table { background: var(--surface); border: 1px solid var(--border); border-radius: 18px; overflow: hidden; box-shadow: var(--shadow); }
.fr {
  display: grid;
  grid-template-columns: 76px 1fr 280px 60px 96px 20px;
  gap: 14px;
  align-items: center;
  padding: 12px 18px;
  border-bottom: 1px solid var(--border);
  border-left: 3px solid transparent;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.12s, border-color 0.12s, opacity 0.15s;
}
.fr:last-child { border-bottom: none; }
.fr:not(.fr-head):hover { background: var(--surface-2); border-left-color: var(--sev); }
.fr:not(.fr-head):focus-visible { outline: 2px solid var(--primary); outline-offset: -2px; }
.fr.saving { opacity: 0.55; }
.fr-head {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--muted);
  opacity: 0.7;
  cursor: default;
  font-weight: 600;
}
.fr-sev { display: flex; align-items: center; gap: 7px; font-size: 12px; font-weight: 600; }
.fr-sev i { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; box-shadow: 0 0 8px -1px; }
.fr-sum { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; display: flex; align-items: center; gap: 6px; }
.fr-note { font-size: 11px; flex-shrink: 0; }
.fr-loc { font-size: 11.5px; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.fr-mode { font-size: 10.5px; font-weight: 600; padding: 1px 7px; border-radius: 5px; justify-self: start; }
.fr-mode.active { color: var(--mode-active); background: color-mix(in srgb, var(--mode-active) 14%, transparent); }
.fr-mode.passive { color: var(--mode-passive); background: color-mix(in srgb, var(--mode-passive) 14%, transparent); }
.fr-st { display: flex; align-items: center; gap: 6px; font-size: 12px; color: var(--st); font-weight: 600; }
.fr-st i { width: 7px; height: 7px; border-radius: 50%; background: var(--st); flex-shrink: 0; box-shadow: 0 0 8px -1px var(--st); }
.fr-arrow { color: var(--muted); opacity: 0.5; font-size: 18px; text-align: center; transition: 0.14s; }
.fr:hover .fr-arrow { color: var(--primary); opacity: 1; transform: translateX(2px); }

@media (max-width: 900px) {
  .fm-summary { flex-direction: column; align-items: stretch; gap: 16px; }
  .fr { grid-template-columns: 66px 1fr 70px 20px; }
  .fr > :nth-child(3), .fr > :nth-child(5) { display: none; }
}

</style>
