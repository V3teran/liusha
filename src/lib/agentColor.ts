// agent 配色：每个 agent 一个强调色，贯穿其推理/工具/派发卡（边框 + chip 同色），一眼分清谁在干活。
// 紫=指挥(orchestrator)、青=侦察(reconnaissance)、玫红=利用(exploitation)；冷暖区分语义。
// 未知 agent 用稳定哈希取色（新增子代理无需改代码也能拿到固定区分色）。

export interface AgentAccent {
  accent: string // 边框/文字主色
  soft: string // chip 背景（低透明度同色）
}

const FIXED: Record<string, AgentAccent> = {
  orchestrator: { accent: '#722ed1', soft: 'rgba(114, 46, 209, 0.14)' }, // 紫·指挥
  reconnaissance: { accent: '#13a8a8', soft: 'rgba(19, 168, 168, 0.14)' }, // 青·侦察
  exploitation: { accent: '#eb2f96', soft: 'rgba(235, 47, 150, 0.14)' }, // 玫红·利用（暖色，攻击语义）
  'traffic-analysis': { accent: '#1890ff', soft: 'rgba(24, 144, 255, 0.14)' }, // 蓝·流量分析（passive）
}

// 未知 agent 的备选色盘（避开已占用的紫/青/玫红/蓝 + 语义色红橙黄绿）。
const FALLBACK_PALETTE = ['#9254de', '#36cfc9', '#597ef7', '#9e1068', '#08979c']

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
  orchestrator: '编排',
  reconnaissance: '侦察',
  exploitation: '利用',
  'traffic-analysis': '流量分析',
}

// agentLabel 把 agent 英文 id 映射成中文标签；未知 agent 原样返回（含空串）。
export function agentLabel(name?: string): string {
  const n = name?.trim()
  if (!n) return ''
  return LABELS[n] ?? n
}
