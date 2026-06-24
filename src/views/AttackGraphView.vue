<script setup lang="ts">
// 执行图页：选 active owner → 解析其对话（思维链来源）→ 拉执行图。
// 渲染两条链：思维链（想/做+得/派子代理，按时序）+ 成果链（漏洞 + depends_on 依赖）。
// 注：当前为可读的结构化呈现；ELK 分层力导图为后续打磨项（见后端 docs/attack-graph-design.md §12）。
import { computed, ref, watch } from 'vue'
import OwnerPicker from '../components/OwnerPicker.vue'
import { getAttackGraph, listConversations } from '../api/client'
import type { AttackGraph, AttackGraphNode } from '../api/types'
import { severityTagColor } from '../lib/severity'

const owner = ref('')
const data = ref<AttackGraph | null>(null)
const loading = ref(false)
const error = ref('')

watch(owner, load)
async function load() {
  if (!owner.value) return
  loading.value = true
  error.value = ''
  data.value = null
  try {
    // owner → conv：找 scan_id === owner 的对话（思维链来源）；无对话不致命，只出成果链。
    let conv = ''
    try {
      const convs = await listConversations()
      conv = convs.find((c) => c.ScanID === owner.value)?.ID ?? ''
    } catch {
      conv = ''
    }
    data.value = await getAttackGraph(owner.value, conv)
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

const nodes = computed<AttackGraphNode[]>(() => data.value?.nodes ?? [])
const traceNodes = computed(() => nodes.value.filter((n) => n.kind !== 'finding'))
const findingNodes = computed(() => nodes.value.filter((n) => n.kind === 'finding'))
const edges = computed(() => data.value?.edges ?? [])

// finding id → 依赖的前置 finding id 列表（成果链）。
const depsByFinding = computed<Record<string, string[]>>(() => {
  const m: Record<string, string[]> = {}
  for (const e of edges.value) {
    if (e.type === 'depends_on') (m[e.to] ??= []).push(e.from)
  }
  return m
})
const titleByID = computed<Record<string, string>>(() => {
  const m: Record<string, string> = {}
  for (const n of nodes.value) m[n.id] = n.title
  return m
})

function kindLabel(k: string): string {
  return k === 'reasoning' ? '想' : k === 'action' ? '做' : k === 'agent' ? '派' : k
}
</script>

<template>
  <div class="page">
    <div class="page-toolbar">
      <OwnerPicker v-model="owner" mode-filter="active" />
    </div>

    <div class="page-body">
      <div v-if="loading" class="state"><a-spin size="large" /></div>
      <div v-else-if="error" class="state"><span class="state-err">⚠ {{ error }}</span></div>
      <div v-else-if="!owner" class="state">请选择一个 active 扫描查看执行图</div>
      <div v-else-if="!nodes.length" class="state">该扫描暂无执行图数据</div>

      <template v-else>
        <div class="stat-grid">
          <div class="stat-card"><div class="sv">{{ traceNodes.length }}</div><div class="sl">思维链节点</div></div>
          <div class="stat-card"><div class="sv">{{ findingNodes.length }}</div><div class="sl">漏洞</div></div>
          <div class="stat-card"><div class="sv">{{ edges.length }}</div><div class="sl">边</div></div>
        </div>

        <div class="panel">
          <p class="panel-title">思维链<span class="muted">想 → 做 → 得（按时序）</span></p>
          <div class="chain">
            <div
              v-for="n in traceNodes"
              :key="n.id"
              class="trace-node"
              :class="[`k-${n.kind}`, { err: n.status === 'error' }]"
            >
              <span class="chip">{{ kindLabel(n.kind) }}</span>
              <span class="t-title">{{ n.title }}</span>
              <span v-if="n.status === 'error'" class="t-err">死路</span>
            </div>
          </div>
        </div>

        <div v-if="findingNodes.length" class="panel">
          <p class="panel-title">成果链<span class="muted">漏洞 + 组合依赖</span></p>
          <div v-for="f in findingNodes" :key="f.id" class="finding-row">
            <a-tag :color="severityTagColor(f.severity || 'info').textColor">{{ f.severity || 'info' }}</a-tag>
            <span class="f-summary">{{ f.title }}</span>
            <span v-if="depsByFinding[f.id]?.length" class="f-deps">
              ← 依赖
              <span v-for="dep in depsByFinding[f.id]" :key="dep" class="dep mono">{{ titleByID[dep] || dep }}</span>
            </span>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.chain { display: flex; flex-direction: column; gap: 6px; }
.trace-node {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 10px;
  border-left: 3px solid var(--border);
  background: var(--surface, transparent);
}
.trace-node.k-reasoning { border-left-color: var(--accent); }
.trace-node.k-action { border-left-color: var(--primary); }
.trace-node.k-agent { border-left-color: #b07cff; }
.trace-node.err { border-left-color: #e5484d; }
.chip {
  font-size: 11px;
  font-weight: 700;
  min-width: 22px;
  text-align: center;
  color: var(--muted);
}
.t-title { font-size: 13px; color: var(--text); word-break: break-all; }
.t-err { font-size: 11px; color: #e5484d; margin-left: auto; }
.finding-row { display: flex; align-items: center; gap: 10px; padding: 7px 0; border-bottom: 1px solid var(--border); }
.finding-row:last-child { border-bottom: none; }
.f-summary { font-size: 13px; }
.f-deps { font-size: 12px; color: var(--muted); display: inline-flex; gap: 6px; align-items: center; }
.dep { font-size: 11px; color: var(--accent); }
</style>
