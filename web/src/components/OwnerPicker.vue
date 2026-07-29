<script setup lang="ts">
// owner 选择器：拉最近会话/扫描列表，下拉选一个 owner_id（v-model）。数据页共用。
// 可选 modeFilter 只显示某模式（active/passive）。挂载时自动选第一个。
import { computed, onMounted, ref } from 'vue'
import { listTasks } from '../api/client'
import type { OwnerSummary } from '../api/types'

const model = defineModel<string>()
const props = defineProps<{ modeFilter?: string }>()

const tasks = ref<OwnerSummary[]>([])
const loading = ref(false)
const error = ref('')

const options = computed(() =>
  tasks.value.map((s) => ({
    label: `${s.mode || '?'} · ${s.id.slice(0, 8)} · ${s.status}`,
    value: s.id,
  }))
)

async function load() {
  loading.value = true
  error.value = ''
  try {
    let list = await listTasks(50)
    if (props.modeFilter) list = list.filter((s) => s.mode === props.modeFilter)
    tasks.value = list
    if (!model.value && list.length) model.value = list[0].id
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载会话失败'
  } finally {
    loading.value = false
  }
}
onMounted(load)
</script>

<template>
  <div class="owner-picker">
    <span class="op-label">会话</span>
    <a-select
      v-model:value="model"
      :options="options"
      :loading="loading"
      placeholder="选择会话 / 扫描"
      size="small"
      show-search
      option-filter-prop="label"
      class="op-select"
    />
    <button class="op-refresh" title="刷新会话列表" @click="load">↻</button>
    <span v-if="error" class="op-err">{{ error }}</span>
  </div>
</template>

<style scoped>
.owner-picker {
  display: flex;
  align-items: center;
  gap: 10px;
}
.op-label { font-size: 13px; color: var(--muted); }
.op-select { width: 360px; }
.op-refresh {
  width: 30px;
  height: 30px;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: var(--surface);
  color: var(--muted);
  cursor: pointer;
}
.op-refresh:hover { color: var(--primary); border-color: var(--primary); }
.op-err { font-size: 12px; color: var(--sev-critical); }
</style>
