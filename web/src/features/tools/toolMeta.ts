import type { ToolKind } from '@/api/types'

// 工具种类展示标签（后端枚举值不变，仅前端呈现）。
//   - function：进程内原生函数工具（内部工具集）
//   - cli     ：外置 CLI 工具白名单（外部工具集）
export const KIND_LABEL: Record<ToolKind, string> = {
  function: '内部函数',
  cli: '外部 CLI',
}

// 分类 code → 展示名。与 einotools 的 FunctionToolCategory 常量对齐；
// CLI 工具的 category 源出 tools.yaml，可能不在此表内——未命中则原样展示。
const CATEGORY_LABEL: Record<string, string> = {
  findings: '漏洞记录',
  credentials: '凭证共享',
  corpus: '知识库',
  lead: '情报黑板',
  traffic: '流量分析',
  sandbox: '沙箱执行',
  skill: '技能手册',
  control: '流程控制',
}

// 分类展示名：命中映射表则用中文名，否则原样返回（空值给占位符）。
export function categoryLabel(category: string): string {
  if (!category) return '未分类'
  return CATEGORY_LABEL[category] ?? category
}
