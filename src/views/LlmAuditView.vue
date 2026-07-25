<script setup lang="ts">
// LLM 审计页：选 owner → 拉 llm_invocation（后端按 hunter 分组，id 游标分页）。
// 顶部汇总卡（数据库层聚合，非前端 reduce 全量行）+ Token 环形图（基于已加载页）+
// 每个 hunter 分组的调用明细表（点行钻取完整 messages/result 原文）。
import { computed, ref, watch } from 'vue'
import OwnerPicker from '../components/OwnerPicker.vue'
import DonutChart from '../components/DonutChart.vue'
import { listLLMInvocations, getLLMInvocationStat, getLLMInvocationDetail } from '../api/client'
import { useOwnerResource } from '../composables/useOwnerResource'
import { mergeInvocationPage } from '../lib/llmInvocationPaging'
import type { LLMInvocationDetail, LLMInvocationSummary } from '../api/types'

const { owner, data, loading, error } = useOwnerResource(listLLMInvocations)

// 汇总卡：数据库层 SUM（/stat 端点），与列表分页无关——不管加载了几页，数字都准。
const stat = ref<{ calls: number; in_tokens: number; out_tokens: number; cached_tokens: number } | null>(null)
const statLoading = ref(false)
async function loadStat() {
  stat.value = null
  if (!owner.value) return
  statLoading.value = true
  try {
    stat.value = await getLLMInvocationStat(owner.value)
  } catch {
    // 汇总卡是增强项，失败静默——列表本身仍可用
  } finally {
    statLoading.value = false
  }
}
watch(owner, loadStat, { immediate: true })

// 加载更多：id 游标翻页，纯函数合并进已渲染的分组（同 hunter_id 追加，新 hunter_id 新增一组）。
const loadingMore = ref(false)
async function loadMore() {
  if (!owner.value || !data.value?.has_more || loadingMore.value) return
  loadingMore.value = true
  try {
    const next = await listLLMInvocations(owner.value, data.value.next_after)
    data.value = mergeInvocationPage(data.value, next)
  } finally {
    loadingMore.value = false
  }
}

// Token 按 hunter 分布：基于当前已加载页（未加载全量时如实标注，不冒充完整统计）。
const tokenData = computed(() =>
  (data.value?.groups ?? []).map((g) => ({
    name: g.hunter_id === 'unassigned' ? '未分配' : g.hunter_id.slice(0, 8),
    value: g.invocations.reduce((s, v) => s + (v.in_tokens || 0) + (v.out_tokens || 0), 0),
  })),
)
const loadedCount = computed(() => data.value?.groups.reduce((s, g) => s + g.invocations.length, 0) ?? 0)

// 组内 role 集合：一个 hunter 分组可能混多个 role（如 orchestrator 内联跑 exploitation，
// 不建独立 hunter 行，同 hunter_id 下 role 各异）——分组标题不能只标一个 role，
// 按组内实际出现的 role 去重展示，避免"贴错标签"。
function rolesOf(invocations: LLMInvocationSummary[]): string[] {
  return [...new Set(invocations.map((v) => v.role).filter(Boolean))]
}

const fmtNum = (n: number) => n.toLocaleString()

// 详情钻取：点行拉完整 messages/result 原文。
const detailOpen = ref(false)
const detail = ref<LLMInvocationDetail | null>(null)
const detailLoading = ref(false)
const detailErr = ref('')
async function openDetail(inv: LLMInvocationSummary) {
  detailOpen.value = true
  detail.value = null
  detailErr.value = ''
  detailLoading.value = true
  try {
    detail.value = await getLLMInvocationDetail(owner.value, inv.id)
  } catch (e) {
    detailErr.value = e instanceof Error ? e.message : '加载详情失败'
  } finally {
    detailLoading.value = false
  }
}
</script>

<template>
  <div class="page">
    <div class="page-toolbar">
      <OwnerPicker v-model="owner" />
    </div>

    <div class="page-body">
      <div v-if="loading" class="state"><a-spin size="large" /></div>
      <div v-else-if="error" class="state"><span class="state-err">⚠ {{ error }}</span></div>
      <div v-else-if="!owner" class="state">请选择一个会话查看 LLM 调用审计</div>
      <div v-else-if="!data || data.total === 0" class="state">该会话暂无 LLM 调用记录</div>

      <template v-else>
        <div class="stat-grid">
          <div class="stat-card"><div class="sv">{{ statLoading ? '…' : fmtNum(stat?.calls ?? 0) }}</div><div class="sl">总调用次数</div></div>
          <div class="stat-card"><div class="sv">{{ statLoading ? '…' : fmtNum((stat?.in_tokens ?? 0) + (stat?.out_tokens ?? 0)) }}</div><div class="sl">总 tokens</div></div>
          <div class="stat-card"><div class="sv">{{ statLoading ? '…' : fmtNum(stat?.in_tokens ?? 0) }}</div><div class="sl">输入 tokens</div></div>
          <div class="stat-card"><div class="sv">{{ statLoading ? '…' : fmtNum(stat?.out_tokens ?? 0) }}</div><div class="sl">输出 tokens</div></div>
        </div>

        <div class="panel">
          <p class="panel-title">
            <span>Token 按 hunter 分布</span>
            <span v-if="data.has_more" class="muted small">仅统计已加载 {{ loadedCount }}/{{ stat?.calls ?? '?' }} 条</span>
          </p>
          <DonutChart class="chart" :data="tokenData" :value-format="fmtNum" />
        </div>

        <div v-for="g in data.groups" :key="g.hunter_id" class="panel">
          <p class="panel-title">
            <span>
              hunter <span class="mono">{{ g.hunter_id === 'unassigned' ? '未分配' : g.hunter_id.slice(0, 12) }}</span>
              <span v-for="r in rolesOf(g.invocations)" :key="r" class="role-tag">{{ r }}</span>
            </span>
            <span class="muted">{{ g.count }} 次</span>
          </p>
          <table class="dtable">
            <thead>
              <tr><th>模型</th><th>角色</th><th>in</th><th>out</th><th>cached</th><th>延迟</th><th>结束原因</th></tr>
            </thead>
            <tbody>
              <tr
                v-for="v in g.invocations"
                :key="v.id"
                role="button"
                tabindex="0"
                @click="openDetail(v)"
                @keydown.enter.prevent="openDetail(v)"
                @keydown.space.prevent="openDetail(v)"
              >
                <td class="mono">{{ v.model || '—' }}</td>
                <td>{{ v.role || '—' }}</td>
                <td>{{ fmtNum(v.in_tokens) }}</td>
                <td>{{ fmtNum(v.out_tokens) }}</td>
                <td>{{ fmtNum(v.cached_tokens) }}</td>
                <td>{{ v.latency_ms }}ms</td>
                <td :class="{ 'state-err': !!v.error_message }">{{ v.error_message || v.finish_reason || '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <div v-if="data.has_more" class="load-more">
          <a-button :loading="loadingMore" @click="loadMore">加载更多（已加载 {{ loadedCount }} 条）</a-button>
        </div>
      </template>
    </div>

    <a-drawer v-model:open="detailOpen" title="调用详情" width="480" placement="right">
      <div v-if="detailLoading" class="state"><a-spin /></div>
      <div v-else-if="detailErr" class="state-err">⚠ {{ detailErr }}</div>
      <template v-else-if="detail">
        <p class="d-meta">
          <span class="mono">{{ detail.model }}</span> · {{ detail.role || '—' }} ·
          request_id <span class="mono">{{ detail.request_id }}</span>
        </p>
        <p class="d-label">输入消息</p>
        <pre class="d-json">{{ JSON.stringify(detail.messages, null, 2) }}</pre>
        <p class="d-label">返回结果</p>
        <pre class="d-json">{{ JSON.stringify(detail.result, null, 2) }}</pre>
      </template>
    </a-drawer>
  </div>
</template>

<style scoped>
.dtable { width: 100%; border-collapse: collapse; font-size: 12.5px; }
.dtable th {
  text-align: left;
  color: var(--muted);
  font-weight: 600;
  padding: 6px 10px;
  border-bottom: 1px solid var(--border);
}
.dtable td { padding: 7px 10px; border-bottom: 1px solid var(--border); }
.dtable tbody tr { cursor: pointer; }
.dtable tbody tr:hover { background: var(--surface-2); }
.dtable tbody tr:focus-visible { outline: 2px solid var(--primary); outline-offset: -2px; }
.dtable .state-err { color: var(--sev-critical); }
.role-tag {
  display: inline-block;
  margin-left: 6px;
  padding: 1px 7px;
  font-size: 10.5px;
  font-weight: 600;
  border-radius: 5px;
  background: var(--surface-2);
  color: var(--muted);
}
.small { font-size: 11.5px; font-weight: 400; }
.load-more { display: flex; justify-content: center; padding: 12px 0 4px; }
.d-meta { font-size: 12.5px; color: var(--muted); margin-bottom: 14px; }
.d-label { font-size: 12px; font-weight: 600; color: var(--muted); margin: 12px 0 6px; }
.d-json {
  max-height: 320px;
  overflow: auto;
  margin: 0;
  padding: 8px 10px;
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: 6px;
  font-size: 11.5px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
