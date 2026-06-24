<script setup lang="ts">
// 漏洞发现页：选 owner（active）→ 从 sitemap 树扁平化所有 endpoint.findings。
// 分级饼图 + 按严重度排序的漏洞列表（带所属端点）。
// 说明：后端无独立 /findings 端点，漏洞内嵌在攻击面树里，故复用 getSitemap。
import { computed, ref, watch } from 'vue'
import OwnerPicker from '../components/OwnerPicker.vue'
import DonutChart from '../components/DonutChart.vue'
import { getSitemap } from '../api/client'
import type { SitemapView, FindingSummary } from '../api/types'
import { severityTagColor, severityColor, severityRank } from '../lib/severity'

const owner = ref('')
const data = ref<SitemapView | null>(null)
const loading = ref(false)
const error = ref('')
const notActive = ref(false)

watch(owner, load)
async function load() {
  if (!owner.value) return
  loading.value = true
  error.value = ''
  notActive.value = false
  data.value = null
  try {
    data.value = await getSitemap(owner.value)
  } catch (e) {
    const msg = e instanceof Error ? e.message : '加载失败'
    if (msg.includes('404')) notActive.value = true
    else error.value = msg
  } finally {
    loading.value = false
  }
}

interface FlatFinding {
  f: FindingSummary
  method: string
  path: string
  domain: string
}
const flat = computed<FlatFinding[]>(() => {
  const out: FlatFinding[] = []
  for (const d of data.value?.root?.children ?? []) {
    for (const ep of d.children ?? []) {
      for (const f of ep.findings ?? []) {
        out.push({ f, method: ep.method || 'GET', path: ep.path || ep.name, domain: d.name })
      }
    }
  }
  out.sort((a, b) => severityRank(a.f.severity) - severityRank(b.f.severity))
  return out
})

const sevCounts = computed(() => {
  const c: Record<string, number> = {}
  for (const x of flat.value) {
    const k = x.f.severity.toLowerCase()
    c[k] = (c[k] || 0) + 1
  }
  return c
})

const sevData = computed(() =>
  Object.entries(sevCounts.value).map(([sev, n]) => ({
    name: sev,
    value: n,
    color: severityColor[sev] ?? '#6e7681',
  }))
)
</script>

<template>
  <div class="page">
    <div class="page-toolbar">
      <OwnerPicker v-model="owner" mode-filter="active" />
    </div>

    <div class="page-body">
      <div v-if="loading" class="state"><a-spin size="large" /></div>
      <div v-else-if="error" class="state"><span class="state-err">⚠ {{ error }}</span></div>
      <div v-else-if="notActive" class="state">漏洞列表来自 active 扫描攻击面（该 owner 无数据）</div>
      <div v-else-if="!owner" class="state">请选择一个 active 扫描查看漏洞</div>
      <div v-else-if="!flat.length" class="state">该扫描暂未发现漏洞</div>

      <template v-else>
        <div class="layout">
          <div class="panel chart-panel">
            <p class="panel-title">严重度分布<span class="muted">{{ flat.length }} 个</span></p>
            <DonutChart class="chart" :data="sevData" />
          </div>
          <div class="panel list-panel">
            <p class="panel-title">漏洞列表</p>
            <div v-for="(x, i) in flat" :key="i" class="finding-item">
              <a-tag :color="severityTagColor(x.f.severity).textColor">{{ x.f.severity }}</a-tag>
              <div class="fi-body">
                <div class="fi-summary">{{ x.f.summary }}</div>
                <div class="fi-meta mono">
                  <span class="fi-method">{{ x.method }}</span>
                  {{ x.domain }}{{ x.path }}
                  <span v-if="x.f.cwe_id" class="fi-cwe">· {{ x.f.cwe_id }}</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.layout { display: grid; grid-template-columns: 340px 1fr; gap: 16px; align-items: start; }
.chart-panel { position: sticky; top: 0; }
.finding-item {
  display: flex;
  gap: 12px;
  padding: 12px 0;
  border-bottom: 1px solid var(--border);
}
.finding-item:last-child { border-bottom: none; }
.fi-body { min-width: 0; }
.fi-summary { font-size: 14px; line-height: 1.5; }
.fi-meta { font-size: 12px; color: var(--muted); margin-top: 5px; word-break: break-all; }
.fi-method { color: var(--accent); font-weight: 700; margin-right: 4px; }
.fi-cwe { color: var(--muted); }
@media (max-width: 900px) {
  .layout { grid-template-columns: 1fr; }
  .chart-panel { position: static; }
}
</style>
