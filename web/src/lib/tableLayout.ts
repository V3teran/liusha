// 表格列宽 → 百分比：让列宽随容器宽度按比例缩放，而非锁死绝对像素。
// 用法：各列 columnDef.size 填「相对权重」而非像素值（数值大小只有相对意义），
// colgroup 用本函数算出的百分比渲染 <col>，配合 table-layout:fixed，
// 视口变化（缩放浏览器、窄屏）时所有列同步等比例缩放，不会出现「某列固定 px，
// 内容一多就把其它列挤到断行」——这正是之前 LlmAuditTable 用绝对 px 时的问题根源。
//
// 权重必须全部为正数：0 权重的列会被算成 0%，在 table-fixed 下等于宽度归零、内容不可见
// （曾经用 size:0 表示"flex 列"，但那是 table-auto 下的语义，搬到这套百分比方案里必须
// 换成一个具体的正数权重）。
export function columnWidthPercents(sizes: number[]): string[] {
  const total = sizes.reduce((sum, s) => sum + s, 0)
  if (total <= 0) return sizes.map(() => 'auto')
  return sizes.map((s) => `${((s / total) * 100).toFixed(3)}%`)
}
