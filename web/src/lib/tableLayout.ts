// 表格列宽 → 百分比：让列宽随容器宽度按比例缩放，而非锁死绝对像素。
// 用法：各列 columnDef.size 填「相对权重」而非像素值（数值大小只有相对意义），
// colgroup 用本函数算出的百分比渲染 <col>，配合 table-layout:fixed，
// 视口变化（缩放浏览器、窄屏）时所有列同步等比例缩放，不会出现「某列固定 px，
// 内容一多就把其它列挤到断行」——这正是之前 LlmAuditTable 用绝对 px 时的问题根源。
//
// 权重必须全部为正数：0 权重的列会被算成 0%，在 table-fixed 下等于宽度归零、内容不可见
// （曾经用 size:0 表示"flex 列"，但那是 table-auto 下的语义，搬到这套百分比方案里必须
// 换成一个具体的正数权重）。

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

export function columnWidthPercents(sizes: number[]): string[] {
  const total = sizes.reduce((sum, s) => sum + s, 0)
  if (total <= 0) return sizes.map(() => 'auto')
  return sizes.map((s) => `${((s / total) * 100).toFixed(3)}%`)
}
