// agent 配色：每个 agent 一个强调色，贯穿其推理/工具/派发卡（边框 + chip 同色），一眼分清谁在干活。
// 认知循环四角色：紫=规划(planner)、翠绿=执行(executor)、天蓝=裁决(evaluator)、玫红=监察(monitor)；
// assistant 是聊天助手（中性灰）。未知 agent 用稳定哈希取色（新增角色无需改代码也能拿到固定区分色）。

export interface AgentAccent {
  accent: string // 边框/文字主色
  soft: string // chip 背景（低透明度同色）
}

// 单一真相源：全站统一调色板（会话卡 + 执行图 canvas 共用，跨页同 agent 同色）。
// planner 紫 / executor 翠绿 / evaluator 天蓝 / monitor 玫红 / assistant 中性灰。
const FIXED: Record<string, AgentAccent> = {
  planner: { accent: '#a78bfa', soft: 'rgba(167, 139, 250, 0.14)' }, // violet·规划
  executor: { accent: '#34d399', soft: 'rgba(52, 211, 153, 0.14)' }, // emerald·执行
  evaluator: { accent: '#38bdf8', soft: 'rgba(56, 189, 248, 0.14)' }, // sky·裁决
  monitor: { accent: '#f472b6', soft: 'rgba(244, 114, 182, 0.14)' }, // rose·监察
  assistant: { accent: '#8c8c8c', soft: 'rgba(140, 140, 140, 0.14)' }, // 聊天助手·中性灰
}

// 未知 agent 的备选色盘（Tailwind，避开已占用的紫/天蓝/翠绿/靛 + 语义色红橙黄）。
const FALLBACK_PALETTE = ['#f472b6', '#22d3ee', '#a3e635', '#fb923c', '#c084fc']

function hashIndex(s: string, mod: number): number {
  let h = 0
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0
  return h % mod
}

// agentAccent 返回某 agent 的配色；空名返回中性灰（老数据 / 未归属）。
export function agentAccent(name?: string): AgentAccent {
  const n = name?.trim()
  if (!n) return { accent: '#8c8c8c', soft: 'rgba(140, 140, 140, 0.14)' }
  if (FIXED[n]) return FIXED[n]
  const c = FALLBACK_PALETTE[hashIndex(n, FALLBACK_PALETTE.length)]
  return { accent: c, soft: c + '24' } // +24 ≈ 14% alpha（8 位十六进制）
}

// agent 英文 id → 中文显示 label（前端展示用，技术 id 保持英文）。未知名回退原始名。
const LABELS: Record<string, string> = {
  planner: '规划',
  executor: '执行',
  evaluator: '裁决',
  monitor: '监察',
  assistant: '助手',
}

// agentLabel 把 agent 英文 id 映射成中文标签；未知 agent 原样返回（含空串）。
export function agentLabel(name?: string): string {
  const n = name?.trim()
  if (!n) return ''
  return LABELS[n] ?? n
}
