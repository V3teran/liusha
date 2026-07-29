<script setup lang="ts">
// 环形饼图（AntV G2）。替代原 vue-echarts 的饼图，供 Findings/LlmAudit 等页复用。
// data 每项 {name, value, color?}；color 缺省时按 PALETTE 循环。可选 valueFormat 格式化 tooltip 值。
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Chart } from '@antv/g2'

interface DonutDatum {
  name: string
  value: number
  color?: string
}
const props = defineProps<{ data: DonutDatum[]; valueFormat?: (v: number) => string }>()

// 无指定色时的中性配色（按类目循环，Tailwind 400-500 调色板，与全站同源）；深浅主题都可读。
const PALETTE = ['#3b82f6', '#22c55e', '#a78bfa', '#f59e0b', '#fb923c', '#ef4444', '#94a3b8', '#38bdf8']
const LABEL_COLOR = '#94a3b8'

const el = ref<HTMLDivElement | null>(null)
let chart: Chart | null = null

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function spec(): any {
  return {
    type: 'interval',
    autoFit: true,
    data: props.data,
    transform: [{ type: 'stackY' }],
    coordinate: { type: 'theta', innerRadius: 0.6 },
    encode: { y: 'value', color: 'name' },
    scale: { color: { range: props.data.map((d, i) => d.color ?? PALETTE[i % PALETTE.length]) } },
    style: { stroke: 'transparent', lineWidth: 2 },
    legend: { color: { position: 'bottom', itemLabelFill: LABEL_COLOR } },
    tooltip: props.valueFormat ? { items: [{ channel: 'y', valueFormatter: props.valueFormat }] } : undefined,
  }
}

function draw() {
  if (!chart) return
  chart.options(spec())
  void chart.render()
}

onMounted(() => {
  if (!el.value) return
  chart = new Chart({ container: el.value, autoFit: true })
  draw()
})

watch(() => props.data, draw, { deep: true })

onBeforeUnmount(() => {
  chart?.destroy()
  chart = null
})
</script>

<template>
  <div ref="el" class="donut-root" />
</template>

<style scoped>
.donut-root {
  width: 100%;
  height: 100%;
}
</style>
