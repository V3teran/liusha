<script setup lang="ts">
// Agent 任务页：选 owner → 拉 agent_runs，按 orchestrator_id 拼任务树。
// orchestrator_id='' 为根（orchestrator），子节点为 exploitation/traffic-analysis。
import { computed, ref, watch } from 'vue'
import OwnerPicker from '../components/OwnerPicker.vue'
import { listAgentRuns } from '../api/client'
import type { AgentRun, AgentRunsResponse } from '../api/types'

const owner = ref('')
const data = ref<AgentRunsResponse | null>(null)
const loading = ref(false)
const error = ref('')

watch(owner, load)
async function load() {
  if (!owner.value) return
  loading.value = true
  error.value = ''
  data.value = null
  try {
    data.value = await listAgentRuns(owner.value)
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

// 树：roots = orchestrator_id==''，children 按 orchestrator_id 归组；落单的归「未归属」。
const tree = computed(() => {
  const runs = data.value?.runs ?? []
  const byParent = new Map<string, AgentRun[]>()
  for (const r of runs) {
    const p = r.orchestrator_id || ''
    if (!byParent.has(p)) byParent.set(p, [])
    byParent.get(p)!.push(r)
  }
  const roots = byParent.get('') ?? []
  const rootIds = new Set(roots.map((r) => r.id))
  const nodes = roots.map((r) => ({ run: r, children: byParent.get(r.id) ?? [] }))
  // 父不为空且父不在 roots 里的孤儿，平铺到末尾
  const orphans: AgentRun[] = []
  for (const [p, list] of byParent) {
    if (p && !rootIds.has(p)) orphans.push(...list)
  }
  return { nodes, orphans }
})

const statusCounts = computed(() => {
  const c: Record<string, number> = {}
  for (const r of data.value?.runs ?? []) c[r.status] = (c[r.status] || 0) + 1
  return c
})

const statusColor: Record<string, string> = {
  done: '#2ec27e',
  running: '#58a6ff',
  pending: '#8a92a6',
  error: '#f85149',
  aborted: '#d29922',
}
function tagColor(status: string) {
  const c = statusColor[status] ?? '#8a92a6'
  return { color: c + '22', textColor: c, borderColor: c + '55' }
}
const fmtTime = (s: string) => (s ? new Date(s).toLocaleString() : '—')
</script>

<template>
  <div class="page">
    <div class="page-toolbar">
      <OwnerPicker v-model="owner" />
    </div>

    <div class="page-body">
      <div v-if="loading" class="state"><a-spin size="large" /></div>
      <div v-else-if="error" class="state"><span class="state-err">⚠ {{ error }}</span></div>
      <div v-else-if="!owner" class="state">请选择一个会话查看 Agent 任务树</div>
      <div v-else-if="!data || data.total === 0" class="state">该会话暂无 Agent 任务</div>

      <template v-else>
        <div class="stat-grid">
          <div class="stat-card"><div class="sv">{{ data.total }}</div><div class="sl">总任务数</div></div>
          <div v-for="(n, s) in statusCounts" :key="s" class="stat-card">
            <div class="sv" :style="{ color: statusColor[s] }">{{ n }}</div><div class="sl">{{ s }}</div>
          </div>
        </div>

        <div class="panel">
          <p class="panel-title">任务树</p>
          <div v-for="node in tree.nodes" :key="node.run.id" class="tree-root">
            <div class="run-row root">
              <span class="role-badge orchestrator">{{ node.run.role }}</span>
              <a-tag :color="tagColor(node.run.status).textColor">{{ node.run.status }}</a-tag>
              <span class="run-id mono">{{ node.run.id.slice(0, 8) }}</span>
              <span class="run-time muted">{{ fmtTime(node.run.created_at) }}</span>
            </div>
            <div v-for="child in node.children" :key="child.id" class="run-row child">
              <span class="role-badge">{{ child.role }}</span>
              <a-tag :color="tagColor(child.status).textColor">{{ child.status }}</a-tag>
              <span class="run-id mono">{{ child.id.slice(0, 8) }}</span>
              <span class="run-time muted">{{ fmtTime(child.created_at) }}</span>
            </div>
          </div>

          <template v-if="tree.orphans.length">
            <div class="run-row root" style="margin-top: 10px"><span class="muted">未归属</span></div>
            <div v-for="o in tree.orphans" :key="o.id" class="run-row child">
              <span class="role-badge">{{ o.role }}</span>
              <a-tag :color="tagColor(o.status).textColor">{{ o.status }}</a-tag>
              <span class="run-id mono">{{ o.id.slice(0, 8) }}</span>
              <span class="run-time muted">{{ fmtTime(o.created_at) }}</span>
            </div>
          </template>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.tree-root { margin-bottom: 12px; }
.run-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 0;
}
.run-row.child {
  margin-left: 22px;
  padding-left: 16px;
  border-left: 2px solid var(--border);
}
.role-badge {
  font-size: 12px;
  font-family: var(--mono);
  padding: 2px 9px;
  border-radius: 6px;
  background: var(--surface-2);
  color: var(--muted);
}
.role-badge.orchestrator { background: var(--primary-soft); color: var(--primary); font-weight: 600; }
.run-id { font-size: 12px; color: var(--text); }
.run-time { font-size: 12px; }
</style>
