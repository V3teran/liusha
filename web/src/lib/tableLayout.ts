// 表格列宽的双 regime 模型：columnDef.size 即该列的「设计像素宽度」。
//
//   容器 ≥ 各列设计宽度总和：table-fixed 把列宽当权重，剩余空间按比例
//   分配——宽屏等比拉伸，与旧百分比方案观感一致。
//   容器 < 总和：各列锁死设计像素，表格总宽超出容器，横向滚动每次位移
//   都揭示「完整宽度」的列内容——省略号退化为极端长内容的兜底，而不再
//   是窄屏下的常态。旧方案（百分比 colgroup + 数字 minWidth）的问题正在
//   于列宽随容器无限等比缩：滚到底每列仍是被压扁的比例宽，滚动永远换不
//   回内容，横向滚动条形同虚设。
//
// size 必须全部为正数：0 会被钳到 1px（table-fixed 下 0 宽列内容不可见）。

import type { RowData } from '@tanstack/react-table'
// 列对齐的单一声明点：columnDef.meta.align 声明该列对齐方向，表头渲染器据此与
// 单元格保持同侧——数值列（# 序号 / 长度 / 字节等）表头与值一起靠右（tabular-nums
// 按位对齐便于比大小），文本/徽章列同靠左。此前 traffic/findings 两表的数值列
// 「表头靠左、值靠右」互相错位，即缺这一声明导致的。
declare module '@tanstack/react-table' {
  // eslint-disable-next-line @typescript-eslint/no-empty-object-type
  interface ColumnMeta<TData extends RowData, TValue extends RowData> {
    /** 该列对齐方向：数值列 right（表头与值同靠右），缺省 left。表头渲染器据此与单元格同侧。 */
    align?: 'left' | 'right'
    /** 悬停表头时的解释文案（llm-audit 响应耗时/摘要列使用）。 */
    headerTitle?: string
  }
}

export function columnPixelWidths(sizes: number[]): string[] {
  return sizes.map((s) => `${Math.max(s, 1)}px`)
}

/** 各列设计像素宽度总和 = 表格最小宽度（容器窄于此值时出横向滚动）。 */
export function totalColumnWidth(sizes: number[]): number {
  return sizes.reduce((sum, s) => sum + Math.max(s, 1), 0)
}
