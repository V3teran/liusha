import type { ToolKind } from '@/api/types'

// 工具种类展示标签（后端枚举值不变，仅前端呈现）。
//   - function：进程内原生函数工具（内部工具集）
//   - cli     ：外置 CLI 工具白名单（外部工具集）
export const KIND_LABEL: Record<ToolKind, string> = {
  function: '内部工具',
  cli: '外部工具',
}

// 分类展示名：后端下发的分类显示名，缺省回落分类码（空值给占位符）。
export function categoryLabel(t: { category: string; category_name?: string }): string {
  return t.category_name || t.category || '未分类'
}
