// ECharts 按需注册（控制体积）：只引饼图/柱图 + 必要交互组件 + canvas 渲染器。
// 数据页通过 `import '../lib/echarts'` side-effect 注册后，再用 vue-echarts 的 <VChart>。
import { use } from 'echarts/core'
import { CanvasRenderer } from 'echarts/renderers'
import { PieChart, BarChart } from 'echarts/charts'
import { TooltipComponent, LegendComponent, GridComponent } from 'echarts/components'

use([CanvasRenderer, PieChart, BarChart, TooltipComponent, LegendComponent, GridComponent])

// 图表轴/文字中性色——深浅主题都可读（取自 --muted 区间）。
export const chartTextColor = '#8a92a6'
