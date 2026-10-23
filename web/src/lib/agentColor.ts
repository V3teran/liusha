import type { LucideIcon } from 'lucide-react'
import { LayoutGrid, Zap, Scale, Eye, Bot } from 'lucide-react'

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
    accent: '#a78bfa',
    soft: 'rgba(167, 139, 250, 0.14)',
    icon: LayoutGrid,
    label: '规划'
  },
  executor: {
    accent: '#34d399',
    soft: 'rgba(52, 211, 153, 0.14)',
    icon: Zap,
    label: '执行'
  },
  evaluator: {
    accent: '#38bdf8',
    soft: 'rgba(56, 189, 248, 0.14)',
    icon: Scale,
    label: '裁决'
  },
  monitor: {
    accent: '#f472b6',
    soft: 'rgba(244, 114, 182, 0.14)',
    icon: Eye,
    label: '监察'
  },
  assistant: {
    accent: '#8c8c8c',
    soft: 'rgba(140, 140, 140, 0.14)',
    icon: Bot,
    label: '助手'
  },
}

const FALLBACK_PALETTE = ['#f472b6', '#22d3ee', '#a3e635', '#fb923c', '#c084fc']

function hashIndex(s: string, mod: number): number {
  let h = 0
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0
  return h % mod
}

export function agentProfile(name?: string): AgentProfile {
  const n = name?.trim()
  if (!n) return { accent: '#8c8c8c', soft: 'rgba(140, 140, 140, 0.14)', icon: Bot, label: '' }
  if (FIXED[n]) return FIXED[n]
  const c = FALLBACK_PALETTE[hashIndex(n, FALLBACK_PALETTE.length)]
  return { accent: c, soft: c + '24', icon: Bot, label: n }
}

export function agentAccent(name?: string): AgentAccent {
  const profile = agentProfile(name)
  return { accent: profile.accent, soft: profile.soft }
}

export function agentLabel(name?: string): string {
  return agentProfile(name).label
}
