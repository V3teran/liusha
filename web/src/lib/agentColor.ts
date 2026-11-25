import type { LucideIcon } from 'lucide-react'
import { Compass, Zap, Scale, Eye, Bot, Waypoints, FoldVertical, FlaskConical } from 'lucide-react'
import { catColor, catSoft, type CategoricalHue } from './categoricalColor'

// 智能体身份色引用分类色 token（见 categoricalColor.ts），深浅主题各自校准。

export interface AgentAccent {
  accent: string
  soft: string
}

export interface AgentProfile {
  accent: string
  soft: string
  icon: LucideIcon
  label: string
}

const FIXED: Record<string, AgentProfile> = {
  planner: {
    accent: catColor('violet'),
    soft: catSoft('violet'),
    // 罗盘=方向/战略，规划者的经典隐喻。
    icon: Compass,
    label: '规划'
  },
  executor: {
    accent: catColor('emerald'),
    soft: catSoft('emerald'),
    icon: Zap,
    label: '执行'
  },
  evaluator: {
    accent: catColor('sky'),
    soft: catSoft('sky'),
    icon: Scale,
    label: '评估'
  },
  monitor: {
    accent: catColor('pink'),
    soft: catSoft('pink'),
    icon: Eye,
    label: '监察'
  },
  assistant: {
    accent: catColor('neutral'),
    soft: catSoft('neutral'),
    icon: Bot,
    label: '助手'
  },
  // 不在认知循环里的调用方（没有独立的配置行）：api 侧的意图分类与标题生成、runner 侧的
  // 会话历史压缩。键是后端写进模型调用的角色名（CallMeta.Role），两边的拼写必须一致——
  // 对不上时页面上就会显示英文原样。
  inspector: {
    accent: catColor('cyan'),
    soft: catSoft('cyan'),
    icon: Waypoints,
    label: '路由'
  },
  compaction: {
    accent: catColor('orange'),
    soft: catSoft('orange'),
    icon: FoldVertical,
    label: '压缩'
  },
  // 任务终态经验蒸馏（runner 侧 memory 链路）：推理由 → 经验条目回写语料库。
  distill: {
    accent: catColor('lime'),
    soft: catSoft('lime'),
    icon: FlaskConical,
    label: '提炼'
  },
}

/** 登记过的身份，顺序即它们在名单类界面里的排列顺序（认知循环的四个 agent 在前）。 */
export const KNOWN_AGENTS: readonly string[] = Object.keys(FIXED)

const FALLBACK_PALETTE: CategoricalHue[] = ['pink', 'cyan', 'lime', 'orange', 'violet']

function hashIndex(s: string, mod: number): number {
  let h = 0
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0
  return h % mod
}

export function agentProfile(name?: string): AgentProfile {
  const n = name?.trim()
  if (!n) return { accent: catColor('neutral'), soft: catSoft('neutral'), icon: Bot, label: '' }
  if (FIXED[n]) return FIXED[n]
  const hue = FALLBACK_PALETTE[hashIndex(n, FALLBACK_PALETTE.length)]
  return { accent: catColor(hue), soft: catSoft(hue), icon: Bot, label: n }
}

export function agentAccent(name?: string): AgentAccent {
  const profile = agentProfile(name)
  return { accent: profile.accent, soft: profile.soft }
}

export function agentLabel(name?: string): string {
  return agentProfile(name).label
}
