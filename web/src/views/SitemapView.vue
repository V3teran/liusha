<script setup lang="ts">
// 攻击面页：选 owner（仅 active 模式有数据）→ 拉 sitemap 树。
// 渲染 root → domain → endpoint(method+path) → findings(severity tag + summary)。
import { computed } from 'vue'
import OwnerPicker from '../components/OwnerPicker.vue'
import { getSitemap } from '../api/client'
import type { SitemapNode } from '../api/types'
import { severityTagColor } from '../lib/severity'
import { useOwnerResource } from '../composables/useOwnerResource'

// notActive：getSitemap 对 passive owner 返 404，统一映射为"无攻击面数据"提示而非错误。
const { owner, data, loading, error, notActive } = useOwnerResource(getSitemap)

// root.children = domains；每个 domain.children = endpoints。
const domains = computed<SitemapNode[]>(() => data.value?.root?.children ?? [])
const endpointCount = computed(() =>
  domains.value.reduce((s, d) => s + (d.children?.length ?? 0), 0)
)
const findingCount = computed(() =>
  domains.value.reduce(
    (s, d) => s + (d.children ?? []).reduce((t, e) => t + (e.findings?.length ?? 0), 0),
    0
  )
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
      <div v-else-if="notActive" class="state">攻击面树仅 <b>active</b> 模式扫描可用（该 owner 无攻击面数据）</div>
      <div v-else-if="!owner" class="state">请选择一个 active 扫描查看攻击面</div>
      <div v-else-if="!domains.length" class="state">该扫描暂无攻击面数据</div>

      <template v-else>
        <div class="stat-grid">
          <div class="stat-card"><div class="sv">{{ domains.length }}</div><div class="sl">域名</div></div>
          <div class="stat-card"><div class="sv">{{ endpointCount }}</div><div class="sl">端点</div></div>
          <div class="stat-card"><div class="sv">{{ findingCount }}</div><div class="sl">漏洞</div></div>
        </div>

        <div v-for="(domain, di) in domains" :key="di" class="panel">
          <p class="panel-title"><span class="mono">{{ domain.name }}</span><span class="muted">{{ domain.children?.length ?? 0 }} 端点</span></p>
          <div v-for="(ep, ei) in domain.children ?? []" :key="ei" class="ep">
            <div class="ep-head">
              <span class="method">{{ ep.method || 'GET' }}</span>
              <span class="path mono">{{ ep.path || ep.name }}</span>
            </div>
            <div v-if="ep.findings?.length" class="ep-findings">
              <div v-for="f in ep.findings" :key="f.id" class="finding-row">
                <a-tag :color="severityTagColor(f.severity).textColor">{{ f.severity }}</a-tag>
                <span class="f-summary">{{ f.summary }}</span>
                <span v-if="f.cwe_id" class="f-cwe mono">{{ f.cwe_id }}</span>
              </div>
            </div>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.ep { padding: 8px 0; border-bottom: 1px solid var(--border); }
.ep:last-child { border-bottom: none; }
.ep-head { display: flex; align-items: center; gap: 10px; }
.method {
  font-size: 11px;
  font-weight: 700;
  font-family: var(--mono);
  color: var(--accent);
  min-width: 44px;
}
.path { font-size: 13px; color: var(--text); word-break: break-all; }
.ep-findings { margin: 8px 0 4px 54px; display: flex; flex-direction: column; gap: 6px; }
.finding-row { display: flex; align-items: center; gap: 10px; }
.f-summary { font-size: 13px; }
.f-cwe { font-size: 11px; color: var(--muted); }
</style>
