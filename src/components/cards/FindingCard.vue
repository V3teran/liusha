<script setup lang="ts">
// 漏洞卡：write_finding 的 Result 只有 {id}，真正的字段在 Args（LLM 入参）。
// 解析 args → severity/summary/cwe/target(method+path)，按分级配色高亮。
import { computed } from 'vue'
import { severityTagColor } from '../../lib/severity'

const props = defineProps<{ args: string }>()

interface FindingArgs {
  summary?: string
  severity?: string
  cwe_id?: string
  owasp_category?: string
  target?: { method?: string; path?: string }
}
const f = computed<FindingArgs>(() => {
  try {
    return JSON.parse(props.args || '{}')
  } catch {
    return {}
  }
})
const sev = computed(() => (f.value.severity || 'info').toLowerCase())
const tag = computed(() => severityTagColor(sev.value))
</script>

<template>
  <div class="finding-card" data-card="finding" :style="{ '--sev': tag.textColor }">
    <span class="badge" :style="{ background: tag.color, color: tag.textColor, borderColor: tag.borderColor }">
      {{ sev.toUpperCase() }}
    </span>
    <div class="body">
      <div class="summary">{{ f.summary || '(无标题)' }}</div>
      <div class="meta">
        <span v-if="f.target?.method || f.target?.path" class="target">
          <span class="method">{{ f.target?.method || 'GET' }}</span>{{ f.target?.path }}
        </span>
        <span v-if="f.cwe_id" class="tag">{{ f.cwe_id }}</span>
        <span v-if="f.owasp_category" class="tag">{{ f.owasp_category }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.finding-card {
  display: flex;
  gap: 12px;
  align-items: flex-start;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-left: 3px solid var(--sev, var(--sev-high));
  border-radius: var(--radius);
  background: var(--surface);
}
.badge {
  flex-shrink: 0;
  font-family: var(--mono);
  font-size: 11px;
  font-weight: 700;
  padding: 2px 8px;
  border-radius: 5px;
  border: 1px solid;
}
.body { min-width: 0; }
.summary { font-size: 14px; line-height: 1.5; font-weight: 500; }
.meta { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 6px; font-size: 12px; }
.target { font-family: var(--mono); color: var(--muted); word-break: break-all; }
.method { color: var(--accent); font-weight: 700; margin-right: 4px; }
.tag {
  font-family: var(--mono);
  color: var(--muted);
  background: var(--surface-2);
  padding: 1px 7px;
  border-radius: 5px;
}
</style>
