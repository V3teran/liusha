<script setup lang="ts">
// LLM 审计页：选会话 → 服务端筛选 + id 游标分页拉调用明细。
//
// 布局对齐业界日志页（NewAPI usage-logs）与本站漏洞管理页：
//   工具栏统计 chip（非大卡片）+ 筛选栏 + 单张扁平密集表（非按 hunter 拆 N 张表）+ 上下页。
// 单元格走两行密度结构：主值 mono/tabular，副行 muted 补充（in/out 主 + cache 副）。
// 点行右侧抽屉钻取完整 messages/result 原文。
import { computed, ref, watch } from 'vue'
import OwnerPicker from '../components/OwnerPicker.vue'
import LlmInvocationDrawer from '../components/LlmInvocationDrawer.vue'
import {
  listLLMInvocations,
  getLLMInvocationStat,
  getLLMInvocationFacets,
  getLLMInvocationDetail,
} from '../api/client'
import type {
  LLMInvocationDetail,
  LLMInvocationFilters,
  LLMInvocationSummary,
  LLMInvocationStat,
} from '../api/types'
import { agentAccent } from '../lib/agentColor'
import { clockTime, compactNumber, fullTime, humanDuration, humanTokens } from '../lib/format'

const PAGE_SIZE = 100

const owner = ref('')
const rows = ref<LLMInvocationSummary[]>([])
const stat = ref<LLMInvocationStat | null>(null)
const roles = ref<string[]>([])
const models = ref<string[]>([])
const loading = ref(false)
const error = ref('')

// 筛选态：服务端筛（分页下前端筛只会筛到当前页）。改动即回到第一页。
const fRole = ref('')
const fModel = ref('')
const fOnlyErr = ref(false)
const fRange = ref<string[]>([]) // [start, end] ISO；空 = 不限
const filters = computed<LLMInvocationFilters>(() => ({
  role: fRole.value,
  model: fModel.value,
  onlyErr: fOnlyErr.value,
  start: fRange.value[0] ?? '',
  end: fRange.value[1] ?? '',
}))
const hasFilter = computed(
  () => !!(fRole.value || fModel.value || fOnlyErr.value || fRange.value.length),
)

// keyset 游标栈：栈顶是当前页起点，回上一页即弹栈（keyset 不能像 offset 那样直接跳页）。
const cursors = ref<number[]>([0])
const nextAfter = ref(0)
const hasMore = ref(false)
const pageNo = computed(() => cursors.value.length)

async function load() {
  if (!owner.value) {
    rows.value = []
    stat.value = null
    return
  }
  loading.value = true
  error.value = ''
  try {
    const after = cursors.value[cursors.value.length - 1] ?? 0
    // 明细与统计并行拉，且吃同一份筛选——统计跟着筛选变，不会「明细 3 条、合计全量」。
    const [page, s] = await Promise.all([
      listLLMInvocations(owner.value, after, PAGE_SIZE, filters.value),
      getLLMInvocationStat(owner.value, filters.value),
    ])
    rows.value = page.items ?? []
    nextAfter.value = page.next_after
    hasMore.value = page.has_more
    stat.value = s
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
    rows.value = []
  } finally {
    loading.value = false
  }
}

// 换会话：重置筛选与游标，并重拉候选集合（role/model 候选是 per-task 的）。
watch(owner, async () => {
  fRole.value = ''
  fModel.value = ''
  fOnlyErr.value = false
  fRange.value = []
  cursors.value = [0]
  roles.value = []
  models.value = []
  if (owner.value) {
    try {
      const f = await getLLMInvocationFacets(owner.value)
      roles.value = f.roles
      models.value = f.models
    } catch {
      // 候选拉失败不致命：下拉退化为空，筛选仍可用（只是没有候选提示）
    }
  }
  await load()
})

// 筛选变化：回到第一页重拉。
function applyFilter() {
  cursors.value = [0]
  void load()
}
function resetFilter() {
  fRole.value = ''
  fModel.value = ''
  fOnlyErr.value = false
  fRange.value = []
  applyFilter()
}
function nextPage() {
  if (!hasMore.value) return
  cursors.value = [...cursors.value, nextAfter.value]
  void load()
}
function prevPage() {
  if (cursors.value.length <= 1) return
  cursors.value = cursors.value.slice(0, -1)
  void load()
}

// 输出速率：out_tokens / 延迟，反映该次调用的吐字速度（对齐 NewAPI 的 TPS 列）。
function tps(v: LLMInvocationSummary): string {
  if (v.latency_ms <= 0 || v.out_tokens <= 0) return ''
  return `${(v.out_tokens / (v.latency_ms / 1000)).toFixed(1)} t/s`
}
// role 配色复用全站单一真相源（会话卡/执行图同 agent 同色）。
function roleColor(role: string): string {
  return agentAccent(role).accent
}

// 详情钻取
const drawerOpen = ref(false)
const detail = ref<LLMInvocationDetail | null>(null)
const detailLoading = ref(false)
const detailErr = ref('')
async function openDetail(v: LLMInvocationSummary) {
  drawerOpen.value = true
  detail.value = null
  detailErr.value = ''
  detailLoading.value = true
  try {
    detail.value = await getLLMInvocationDetail(owner.value, v.id)
  } catch (e) {
    detailErr.value = e instanceof Error ? e.message : '加载详情失败'
  } finally {
    detailLoading.value = false
  }
}
</script>

<template>
  <div class="la-page">
    <!-- 工具栏：会话选择 + 统计 chip（紧凑，不占垂直空间） -->
    <div class="la-top">
      <OwnerPicker v-model="owner" />
      <div v-if="stat" class="la-stats">
        <span class="st-chip"><i class="ac-calls" />调用<b>{{ humanTokens(stat.calls) }}</b></span>
        <span class="st-chip" :title="humanTokens(stat.in_tokens)"><i class="ac-in" />输入<b>{{ compactNumber(stat.in_tokens) }}</b></span>
        <span class="st-chip" :title="humanTokens(stat.out_tokens)"><i class="ac-out" />输出<b>{{ compactNumber(stat.out_tokens) }}</b></span>
        <span class="st-chip" :title="humanTokens(stat.cached_tokens)"><i class="ac-cache" />缓存<b>{{ compactNumber(stat.cached_tokens) }}</b></span>
        <span class="st-chip"><i class="ac-lat" />耗时<b>{{ humanDuration(stat.latency_ms) }}</b></span>
      </div>
    </div>

    <div class="la-body">
      <div v-if="!owner" class="la-state">请选择一个会话查看 LLM 调用审计</div>
      <div v-else-if="error" class="la-state err">⚠ {{ error }}</div>

      <template v-else>
        <!-- 筛选栏：全部服务端筛，统计随之变化 -->
        <div class="la-toolbar">
          <a-select v-model:value="fRole" style="width: 156px" size="small" @change="applyFilter">
            <a-select-option value="">全部角色</a-select-option>
            <a-select-option v-for="r in roles" :key="r" :value="r">{{ r }}</a-select-option>
          </a-select>
          <a-select v-model:value="fModel" style="width: 170px" size="small" show-search @change="applyFilter">
            <a-select-option value="">全部模型</a-select-option>
            <a-select-option v-for="m in models" :key="m" :value="m">{{ m }}</a-select-option>
          </a-select>
          <a-range-picker
            v-model:value="fRange"
            size="small"
            show-time
            value-format="YYYY-MM-DDTHH:mm:ss[Z]"
            style="width: 320px"
            @change="applyFilter"
          />
          <label class="la-onlyerr">
            <a-switch v-model:checked="fOnlyErr" size="small" @change="applyFilter" />仅错误
          </label>
          <a-button v-if="hasFilter" size="small" type="text" @click="resetFilter">清空筛选</a-button>
          <span class="la-count">
            第 {{ pageNo }} 页 · 本页 {{ rows.length }} 条<template v-if="stat"> / 共 {{ humanTokens(stat.calls) }}</template>
          </span>
        </div>

        <!-- 单张扁平密集表 -->
        <div class="la-table">
          <div class="lr lr-head">
            <span>时刻</span><span>角色</span><span>模型</span><span>Tokens</span><span>延迟</span><span>结果</span><span></span>
          </div>

          <template v-if="loading">
            <!-- skeleton：占位骨架屏，避免转圈导致的布局跳动 -->
            <div v-for="i in 8" :key="`sk${i}`" class="lr lr-sk">
              <span v-for="c in 7" :key="c" class="sk-bar" />
            </div>
          </template>

          <div v-else-if="!rows.length" class="la-state pad">
            {{ hasFilter ? '当前筛选无匹配调用' : '该会话暂无 LLM 调用记录' }}
          </div>

          <div
            v-for="v in rows"
            v-else
            :key="v.id"
            class="lr"
            :class="{ bad: !!v.error_message }"
            :style="{ '--role': roleColor(v.role) }"
            role="button"
            tabindex="0"
            :aria-label="`查看调用详情 ${v.request_id}`"
            @click="openDetail(v)"
            @keydown.enter.prevent="openDetail(v)"
            @keydown.space.prevent="openDetail(v)"
          >
            <span class="lr-time mono" :title="fullTime(v.created_at)">{{ clockTime(v.created_at) || '—' }}</span>
            <span class="lr-role"><i />{{ v.role || '—' }}</span>
            <span class="lr-model">
              <span class="mono model-main">{{ v.model || '—' }}</span>
              <em class="sub">{{ v.provider }}</em>
            </span>
            <span class="lr-tok">
              <span class="mono tok-main">{{ humanTokens(v.in_tokens) }} / {{ humanTokens(v.out_tokens) }}</span>
              <em v-if="v.cached_tokens > 0" class="sub">缓存↓ {{ humanTokens(v.cached_tokens) }}</em>
            </span>
            <span class="lr-lat">
              <span class="mono">{{ humanDuration(v.latency_ms) }}</span>
              <em v-if="tps(v)" class="sub">{{ tps(v) }}</em>
            </span>
            <span class="lr-fin" :title="v.error_message || v.finish_reason">
              {{ v.error_message || v.finish_reason || '—' }}
            </span>
            <span class="lr-arrow">›</span>
          </div>
        </div>

        <!-- keyset 上下页（游标翻页不能跳页，故不做页码） -->
        <div v-if="rows.length || pageNo > 1" class="la-pager">
          <a-button size="small" :disabled="pageNo <= 1 || loading" @click="prevPage">上一页</a-button>
          <span class="pg-no">第 {{ pageNo }} 页</span>
          <a-button size="small" :disabled="!hasMore || loading" @click="nextPage">下一页</a-button>
        </div>
      </template>
    </div>

    <LlmInvocationDrawer
      v-model:open="drawerOpen"
      :detail="detail"
      :loading="detailLoading"
      :error="detailErr"
    />
  </div>
</template>

<style scoped>
.la-page { height: 100%; display: flex; flex-direction: column; min-height: 0; }
.la-top {
  display: flex;
  align-items: center;
  gap: 18px;
  flex-wrap: wrap;
  padding: 12px 22px;
  border-bottom: 1px solid var(--border);
}
.la-body { flex: 1; min-height: 0; overflow-y: auto; padding: 18px 22px 22px; }
.la-state { text-align: center; color: var(--muted); padding: 60px 0; font-size: 13.5px; }
.la-state.err { color: var(--error, var(--sev-critical)); }
.la-state.pad { padding: 44px 0; }

/* 统计 chip：紧凑一排，细色条 + 等宽数字（取代 4 个大卡片） */
.la-stats { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.st-chip {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 28px;
  padding: 0 10px;
  font-size: 12px;
  color: var(--muted);
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 8px;
}
.st-chip i { width: 2px; height: 14px; border-radius: 999px; flex-shrink: 0; }
.st-chip b { color: var(--text); font-weight: 600; font-variant-numeric: tabular-nums; font-family: var(--font-mono, ui-monospace, monospace); }
.ac-calls { background: var(--primary); }
.ac-in { background: #38bdf8; }
.ac-out { background: #34d399; }
.ac-cache { background: #a78bfa; }
.ac-lat { background: #94a3b8; }

/* 筛选栏 */
.la-toolbar { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-bottom: 14px; }
.la-onlyerr { display: inline-flex; align-items: center; gap: 6px; font-size: 12.5px; color: var(--muted); }
.la-count { margin-left: auto; font-size: 12.5px; color: var(--muted); font-variant-numeric: tabular-nums; }

/* 密集表：与漏洞管理页同一套视觉语言（18px 圆角卡 + grid 行 + 左侧色条 hover） */
.la-table { background: var(--surface); border: 1px solid var(--border); border-radius: 18px; overflow: hidden; box-shadow: var(--shadow); }
.lr {
  display: grid;
  grid-template-columns: 78px 128px 1fr 148px 92px 150px 20px;
  gap: 14px;
  align-items: center;
  padding: 9px 18px;
  border-bottom: 1px solid var(--border);
  border-left: 3px solid transparent;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.12s, border-color 0.12s;
}
.lr:last-child { border-bottom: none; }
.lr:not(.lr-head):not(.lr-sk):hover { background: var(--surface-2); border-left-color: var(--role); }
.lr:not(.lr-head):focus-visible { outline: 2px solid var(--primary); outline-offset: -2px; }
/* 失败行整行染色（不只把文字标红），异常在扫视时就能定位 */
.lr.bad { background: color-mix(in srgb, var(--sev-critical) 8%, transparent); }
.lr.bad:hover { background: color-mix(in srgb, var(--sev-critical) 14%, transparent); }
.lr-head {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--muted);
  opacity: 0.7;
  cursor: default;
  font-weight: 600;
}
.mono { font-family: var(--font-mono, ui-monospace, SFMono-Regular, Menlo, monospace); font-variant-numeric: tabular-nums; }
/* 两行单元格：主值一行，副信息更小更淡 */
.sub { display: block; font-style: normal; font-size: 10.5px; color: var(--muted); opacity: 0.75; margin-top: 1px; }
.lr-time { font-size: 12px; color: var(--muted); }
.lr-role { display: flex; align-items: center; gap: 7px; font-size: 12px; font-weight: 600; color: var(--role); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.lr-role i { width: 8px; height: 8px; border-radius: 50%; background: var(--role); flex-shrink: 0; }
.lr-model { overflow: hidden; }
.model-main { font-size: 12.5px; display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tok-main { font-size: 12.5px; }
.lr-lat { font-size: 12.5px; }
.lr-fin { font-size: 11.5px; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.lr.bad .lr-fin { color: var(--sev-critical); }
.lr-arrow { color: var(--muted); opacity: 0.5; font-size: 18px; text-align: center; transition: 0.14s; }
.lr:hover .lr-arrow { color: var(--primary); opacity: 1; transform: translateX(2px); }

/* skeleton 骨架屏 */
.lr-sk { cursor: default; }
.sk-bar { height: 10px; border-radius: 4px; background: var(--surface-2); animation: sk 1.2s ease-in-out infinite; }
@keyframes sk { 0%, 100% { opacity: 0.45; } 50% { opacity: 0.85; } }

.la-pager { display: flex; align-items: center; justify-content: center; gap: 14px; padding: 14px 0 4px; }
.pg-no { font-size: 12.5px; color: var(--muted); font-variant-numeric: tabular-nums; }

@media (max-width: 1100px) {
  .lr { grid-template-columns: 74px 110px 1fr 130px 20px; }
  .lr > :nth-child(5), .lr > :nth-child(6) { display: none; }
}
</style>
