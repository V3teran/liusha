/**
 * 探索图节点 kind 视觉档案与内容提取。
 *
 * accent 是"图形角色"用色（色条/图标/Handle/小地图色块/LOD 底色），
 * 硬约束：亮暗两主题背景上均 ≥3.3:1。400 系粉彩在浅色背景仅 1.8–2.6:1
 * 会没入白底，故统一取 500/600 系中间调；色相延续 agentColor.ts 的
 * 四 Agent 谱系（紫/绿/蓝/粉 = 规划/执行/评估/监察的产出联想）。
 * 文字一律用文本 token（小号彩字在任何主题都难保 4.5:1，不拿色相冒险）。
 */

import type { LucideIcon } from 'lucide-react'
import { Crosshair, Zap, Eye, ShieldCheck, CircleHelp } from 'lucide-react'
import { PRIORITY_META } from '@/lib/priority'
import type { GraphNode, LeadClosure, LeadSpec, NodeKind, VerificationStatus } from '@/api/explorationgraph'

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
  running: { label: '执行中', dotClass: 'bg-status-info motion-safe:animate-pulse' },
  completed: { label: '已完成', dotClass: 'bg-status-success' },
  // action：执行预算耗尽；派生目标：任务结束时方向未达成——两者都是「已穷尽」
  exhausted: { label: '已穷尽', dotClass: 'bg-status-warning' },
  failed: { label: '失败', dotClass: 'bg-status-error' },
  // 没做完就被收回（任务结束、目标调整），不含对错判断
  aborted: { label: '已中止', dotClass: 'bg-status-neutral' },
  // 监察者或用户判定这一步不该做
  rejected: { label: '已否决', dotClass: 'bg-status-error' },
}

export function stateMeta(state?: string): StateMeta | null {
  if (!state) return null
  return STATE_META[state] ?? { label: state, dotClass: 'bg-status-neutral' }
}

export interface StateOption {
  value: string
  label: string
  count: number
}

/**
 * 状态筛选的选项：只列这张图里实际有的状态，按生命周期的先后，带上节点数——图上没有的状态
 * 不占位。当前选中的状态即使图上已经没有也留着，否则下拉框显示的与生效的筛选对不上。
 */
export function stateOptions(byState: Record<string, number>, current: string): StateOption[] {
  return Object.keys(STATE_META)
    .filter((value) => (byState[value] ?? 0) > 0 || value === current)
    .map((value) => ({ value, label: STATE_META[value].label, count: byState[value] ?? 0 }))
}

/* ========== 优先级 / 验证状态 / 复杂度 / 观察分类 ========== */

/** 假设观察的验证状态（事实观察与结果没有验证状态） */
export const VERIFICATION_META: Record<VerificationStatus, string> = {
  pending: '待验证',
  confirmed: '已坐实',
  refuted: '已证伪',
}

export function verificationMeta(status?: VerificationStatus): string | null {
  if (!status) return null
  return VERIFICATION_META[status] ?? status
}

export const COMPLEXITY_META: Record<string, string> = {
  simple: '简单',
  moderate: '中等',
  complex: '复杂',
}

export const OBSERVATION_CATEGORY_META: Record<string, string> = {
  entity: '实体',
  access: '访问',
  structure: '结构',
  behavior: '行为',
  note: '笔记',
  hypothesis: '假设',
}

/* ========== 线索（事实观察上的信号） ========== */

/**
 * 线索标记的颜色（卡片上沿的脊线与右上角的脚印）。亮暗两主题通用的固定色，取值以「两主题的
 * 画布底与卡片淡底上均 ≥3:1 图形对比度」为硬约束（浅色底 3.4、深色底 5.4）。图上的类别色与
 * 关系线都是冷色（蓝、绿、紫、青、灰），暖色里红归优先级、玫红归结果，橙色留给线索——整张图
 * 上只有线索用它。它刻意不取「跟进」线的颜色：线表达两个节点的关系，标记表达「这条路还没
 * 走到头」，两件事不共用一个颜色。
 */
export const LEAD_ACCENT = '#ea580c'
/** 跟进连线的青色：被认领推进中的线索脚印与描边取它，与跟进线同色 */
export const PURSUES_ACCENT = '#0d9488'

/** 线索关闭类别的展示文案（与后端 explorationgraph.LeadClosureKind 同一词表） */
export const LEAD_CLOSURE_META: Record<string, string> = {
  not_worth: '价值够不上一个动作',
  out_of_scope: '超出任务目标',
  premise_refuted: '前提已证伪',
  exhausted: '已走到头',
  blocked: '受阻',
  concluded: '已有结论',
  dry: '干涸',
  vetoed: '被否决关闭',
}

export interface LeadMeta {
  /** 已关闭（规划层判定不再跟进，或由跟进的结局结算） */
  closed: boolean
  /** 关闭原因全文：类别加理由（仅已关闭、且记下了原因时有） */
  detail: string
  /** 线索的三样：下一步、要弄清、能换来什么。线索成为结构化字段之前落图的没有（都是空串） */
  spec: LeadSpec
}

/** 线索的三样里有没有写得出的（旧线索三样皆空） */
export function leadHasSpec(spec: LeadSpec): boolean {
  return spec.next !== '' || spec.question !== '' || spec.potential !== ''
}

/** 线索的悬停说明：三样各一行；旧线索只有一句通用的说明 */
export function leadHint(meta: LeadMeta): string {
  const generic = '执行者已踩通、尚未推进到结论的路径'
  if (!leadHasSpec(meta.spec)) return `线索：${generic}`
  return ['线索：' + generic, `下一步：${meta.spec.next}`, `要弄清：${meta.spec.question}`, `能换来：${meta.spec.potential}`].join('\n')
}

function metadataOf(node: GraphNode): Record<string, unknown> {
  return node.metadata && typeof node.metadata === 'object' ? (node.metadata as Record<string, unknown>) : {}
}

/**
 * 线索是事实观察带的延伸说明（节点的顶层字段 lead）：这条路走得通、还没走到结论，连同
 * 下一步做什么、要弄清什么、能换来什么。假设观察不成线索；规划层关闭它时记下类别与理由
 * （lead_closed）。
 */
export function leadMeta(node: GraphNode): LeadMeta | null {
  if (node.kind !== 'observation' || node.verification_status || !node.lead) return null
  const spec = node.lead
  const closure: LeadClosure | undefined = node.lead_closed
  if (!closure) return { closed: false, detail: '', spec }
  const kindLabel = LEAD_CLOSURE_META[closure.kind] ?? closure.kind
  const reasonText = closure.reason.trim()
  if (kindLabel && reasonText) return { closed: true, detail: `${kindLabel}：${reasonText}`, spec }
  return { closed: true, detail: kindLabel || reasonText, spec }
}

/** 动作否决的范围（metadata.rejected_scope）：只否决这一步，还是连它跟进的线索一起。用户否决一律只否决这一步。 */
export function rejectionScope(node: GraphNode): 'step' | 'lead' | null {
  if (node.state !== 'rejected') return null
  return metadataOf(node).rejected_scope === 'lead' ? 'lead' : 'step'
}

/* ========== 内容提取（content 按 kind 多态） ========== */

function contentOf(node: GraphNode): Record<string, unknown> {
  return node.content && typeof node.content === 'object' ? (node.content as Record<string, unknown>) : {}
}

/** 各 kind 的主文本字段（领域载荷的首字段） */
const TITLE_FIELD: Record<string, string> = {
  objective: 'description',
  action: 'instruction',
  observation: 'statement',
  result: 'statement',
}

/** 卡片与详情面板共用的节点主文本 */
export function nodeTitle(node: GraphNode): string {
  const field = TITLE_FIELD[node.kind]
  const v = field ? contentOf(node)[field] : undefined
  return v != null && v !== '' ? String(v) : ''
}

export interface NodeChips {
  /** 状态（objective/action） */
  state?: StateMeta
  /** 观察分类 */
  category?: string
  /** 验证状态文字标签（仅假设观察） */
  verification?: string
  /** 高优先级（假设与结果的优先级即严重度；仅 P0/P1 上卡，避免噪音） */
  priority?: { code: string; className: string }
  /** 加急（规划者标的，一轮至多一个）：空位不够时先派 */
  urgent?: true
  /** 线索（事实观察带的 lead）：卡片上的标记见 ExplorationNodeCard */
  lead?: { label: string; closed: boolean; detail: string; spec: LeadSpec }
}

/** 卡片底部徽章行的取值（详情面板另有更全的展示） */
export function nodeChips(node: GraphNode): NodeChips {
  const c = contentOf(node)
  const chips: NodeChips = {}

  if (node.state) chips.state = stateMeta(node.state) ?? undefined

  if (node.kind === 'observation') {
    const cat = typeof c.category === 'string' ? c.category : ''
    chips.category = OBSERVATION_CATEGORY_META[cat] ?? (cat || undefined)
  }

  if (node.kind === 'observation' || node.kind === 'result') {
    chips.verification = verificationMeta(node.verification_status) ?? undefined
  }
  const lead = leadMeta(node)
  if (lead) chips.lead = { label: lead.closed ? '已关闭' : '线索', closed: lead.closed, detail: lead.detail, spec: lead.spec }

  if (node.kind === 'action' && node.urgent) chips.urgent = true

  if (node.priority === 'critical' || node.priority === 'high') {
    chips.priority = {
      code: PRIORITY_META[node.priority].code,
      className: node.priority === 'critical' ? 'text-sev-critical' : 'text-sev-high',
    }
  }

  return chips
}
