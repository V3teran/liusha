/**
 * 探索图节点 kind 视觉档案与内容提取。
 *
 * accent 是"图形角色"用色（色条/图标/Handle/小地图色块/LOD 底色），
 * 硬约束：亮暗两主题背景上均 ≥3.3:1。400 系粉彩在浅色背景仅 1.8–2.6:1
 * 会没入白底，故统一取 500/600 系中间调；色相延续 agentColor.ts 的
 * 四 Agent 谱系（紫/绿/蓝/粉 = 规划/执行/裁决/监察的产出联想）。
 * 文字一律用文本 token（小号彩字在任何主题都难保 4.5:1，不拿色相冒险）。
 */

import type { LucideIcon } from 'lucide-react'
import { Crosshair, Zap, Eye, ShieldCheck, CircleHelp } from 'lucide-react'
import type { GraphNode, NodeKind } from '@/api/explorationgraph'

export interface KindProfile {
  icon: LucideIcon
  label: string
  accent: string
}

export const KIND_PROFILES: Record<NodeKind, KindProfile> = {
  objective: { icon: Crosshair, label: '目标', accent: '#8b5cf6' },
  action: { icon: Zap, label: '动作', accent: '#059669' },
  observation: { icon: Eye, label: '观测', accent: '#0284c7' },
  result: { icon: ShieldCheck, label: '结果', accent: '#ec4899' },
}

const FALLBACK_PROFILE: KindProfile = { icon: CircleHelp, label: '未知', accent: '#64748b' }

export function kindProfile(kind: string): KindProfile {
  return KIND_PROFILES[kind as NodeKind] ?? FALLBACK_PROFILE
}

export const KIND_ORDER: NodeKind[] = ['objective', 'action', 'observation', 'result']

/* ========== 状态机（objective/action） ========== */

export interface StateMeta {
  label: string
  /** 状态点颜色（走语义 token，随主题） */
  dotClass: string
}

export const STATE_META: Record<string, StateMeta> = {
  open: { label: '待处理', dotClass: 'bg-status-neutral' },
  running: { label: '执行中', dotClass: 'bg-status-info animate-pulse' },
  completed: { label: '已完成', dotClass: 'bg-status-success' },
  exhausted: { label: '预算耗尽', dotClass: 'bg-status-warning' },
  failed: { label: '失败', dotClass: 'bg-status-error' },
  aborted: { label: '已中止', dotClass: 'bg-status-neutral' },
}

export function stateMeta(state?: string): StateMeta | null {
  if (!state) return null
  return STATE_META[state] ?? { label: state, dotClass: 'bg-status-neutral' }
}

/* ========== 优先级 / 置信度 / 复杂度 / 动作类型 / 观测分类 ========== */

export interface PriorityMeta {
  /** 领域词汇：优先级代号（与后端 Priority 枚举一一对应） */
  code: string
  label: string
}

export const PRIORITY_META: Record<string, PriorityMeta> = {
  critical: { code: 'P0', label: '关键' },
  high: { code: 'P1', label: '高' },
  medium: { code: 'P2', label: '中' },
  low: { code: 'P3', label: '低' },
}

export const CONFIDENCE_META: Record<string, string> = {
  unverified: '未验证',
  verified: '已验证',
  refuted: '已证伪',
  factual: '事实',
}

export function confidenceMeta(confidence?: string): string | null {
  if (!confidence) return null
  return CONFIDENCE_META[confidence] ?? confidence
}

/**
 * 威胁等级色点（600 系中间调，双主题 ≥2.8:1）。独立于 lib/severity.ts：
 * 那套 400/500 系为深底标签调校，黄/绿在浅色卡片上不足 2.2:1；
 * 色点仅作强化，语义始终由旁边的文字标签承载。
 */
export const SEVERITY_DOTS: Record<string, string> = {
  critical: '#dc2626',
  high: '#ea580c',
  medium: '#ca8a04',
  low: '#16a34a',
  info: '#2563eb',
}

export const COMPLEXITY_META: Record<string, string> = {
  simple: '简单',
  moderate: '中等',
  complex: '复杂',
}

export const ACTION_TYPE_META: Record<string, string> = {
  reconnaissance: '侦察',
  analysis: '分析',
  testing: '测试',
}

export const OBSERVATION_CATEGORY_META: Record<string, string> = {
  entity: '实体',
  access: '访问',
  structure: '结构',
  behavior: '行为',
  issue: '问题',
  note: '笔记',
  hypothesis: '假设',
}

/* ========== 内容提取（content 按 kind 多态） ========== */

function contentOf(node: GraphNode): Record<string, unknown> {
  return node.content && typeof node.content === 'object' ? (node.content as Record<string, unknown>) : {}
}

function pickContent(node: GraphNode, keys: string[]): string {
  const c = contentOf(node)
  for (const k of keys) {
    const v = c[k]
    if (v != null && v !== '') return String(v)
  }
  return ''
}

/** 卡片与详情面板共用的节点主文本（每种 kind 的领域载荷首字段） */
export function nodeTitle(node: GraphNode): string {
  switch (node.kind) {
    case 'objective':
      return pickContent(node, ['description', 'goal', 'title', 'text'])
    case 'action':
      return pickContent(node, ['instruction', 'title', 'text'])
    case 'observation':
      return pickContent(node, ['statement', 'title', 'text'])
    case 'result':
      return pickContent(node, ['summary', 'title', 'text'])
    default:
      return pickContent(node, ['description', 'instruction', 'statement', 'summary', 'title', 'text'])
  }
}

export interface NodeChips {
  /** 状态（objective/action） */
  state?: StateMeta
  /** 动作类型 / 观测分类 */
  category?: string
  /** 威胁等级（observation/result 的 content.severity） */
  severity?: string
  /** 置信度文字标签 */
  confidence?: string
  /** 高优先级（仅 P0/P1 上卡，避免噪音） */
  priority?: { code: string; className: string }
}

/** 卡片底部徽章行的取值（详情面板另有更全的展示） */
export function nodeChips(node: GraphNode): NodeChips {
  const c = contentOf(node)
  const chips: NodeChips = {}

  if (node.state) chips.state = stateMeta(node.state) ?? undefined

  if (node.kind === 'action') {
    const t = typeof c.type === 'string' ? c.type : ''
    chips.category = ACTION_TYPE_META[t] ?? (t || undefined)
  } else if (node.kind === 'observation') {
    const cat = typeof c.category === 'string' ? c.category : ''
    chips.category = OBSERVATION_CATEGORY_META[cat] ?? (cat || undefined)
  }

  if (node.kind === 'observation' || node.kind === 'result') {
    const sev = typeof c.severity === 'string' ? c.severity.toLowerCase() : ''
    if (sev) chips.severity = sev
    chips.confidence = confidenceMeta(node.confidence) ?? undefined
  }
  if (node.priority === 'critical' || node.priority === 'high') {
    chips.priority = {
      code: PRIORITY_META[node.priority].code,
      className: node.priority === 'critical' ? 'text-sev-critical' : 'text-sev-high',
    }
  }

  return chips
}
