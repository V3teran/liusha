<script setup lang="ts">
// LLM 审计页：选 owner → 拉 llm_invocation（后端按 hunter 分组）。
// 顶部汇总卡 + Token 环形图 + 每个 hunter 分组的调用明细表。
import { computed } from 'vue'
import VChart from 'vue-echarts'
import '../lib/echarts'
import { chartTextColor } from '../lib/echarts'
import OwnerPicker from '../components/OwnerPicker.vue'
import { listLLMInvocations } from '../api/client'
import { useOwnerResource } from '../composables/useOwnerResource'

const { owner, data, loading, error } = useOwnerResource(listLLMInvocations)

const totals = computed(() => {
  const inv = data.value?.groups.flatMap((g) => g.invocations) ?? []
  const inTok = inv.reduce((s, v) => s + (v.in_tokens || 0), 0)
  const outTok = inv.reduce((s, v) => s + (v.out_tokens || 0), 0)
  return {
    count: data.value?.total ?? 0,
    tokens: inTok + outTok,
    inTok,
    outTok,
  }
})

// Token 按 hunter 分布（in+out）：只统计 token 用量。
const tokenChart = computed(() => ({
  tooltip: { trigger: 'item', valueFormatter: (v: number) => v.toLocaleString() },
  legend: { bottom: 0, textStyle: { color: chartTextColor }, type: 'scroll' },
  series: [
    {
      type: 'pie',
      radius: ['45%', '70%'],
      itemStyle: { borderColor: 'transparent', borderWidth: 2 },
      label: { color: chartTextColor },
      data: (data.value?.groups ?? []).map((g) => ({
        name: g.hunter_id === 'unassigned' ? '未分配' : g.hunter_id.slice(0, 8),
        value: g.invocations.reduce((s, v) => s + (v.in_tokens || 0) + (v.out_tokens || 0), 0),
      })),
    },
  ],
}))

const fmtNum = (n: number) => n.toLocaleString()
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
          <div class="stat-card"><div class="sv">{{ totals.count }}</div><div class="sl">总调用次数</div></div>
          <div class="stat-card"><div class="sv">{{ fmtNum(totals.tokens) }}</div><div class="sl">总 tokens</div></div>
          <div class="stat-card"><div class="sv">{{ fmtNum(totals.inTok) }}</div><div class="sl">输入 tokens</div></div>
          <div class="stat-card"><div class="sv">{{ fmtNum(totals.outTok) }}</div><div class="sl">输出 tokens</div></div>
        </div>

        <div class="panel">
          <p class="panel-title">Token 按 hunter 分布</p>
          <VChart class="chart" :option="tokenChart" autoresize />
        </div>

        <div v-for="g in data.groups" :key="g.hunter_id" class="panel">
          <p class="panel-title">
            <span>hunter <span class="mono">{{ g.hunter_id === 'unassigned' ? '未分配' : g.hunter_id.slice(0, 12) }}</span></span>
            <span class="muted">{{ g.count }} 次</span>
          </p>
          <table class="dtable">
            <thead>
              <tr><th>模型</th><th>in</th><th>out</th><th>cached</th><th>延迟</th><th>结束原因</th></tr>
            </thead>
            <tbody>
              <tr v-for="v in g.invocations" :key="v.id">
                <td class="mono">{{ v.model || '—' }}</td>
                <td>{{ fmtNum(v.in_tokens) }}</td>
                <td>{{ fmtNum(v.out_tokens) }}</td>
                <td>{{ fmtNum(v.cached_tokens) }}</td>
                <td>{{ v.latency_ms }}ms</td>
                <td :class="{ 'state-err': !!v.error_message }">{{ v.error_message || v.finish_reason || '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </div>
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
.dtable tbody tr:hover { background: var(--surface-2); }
.dtable .state-err { color: var(--sev-critical); }
</style>
