import { ROLE_FALLBACK, TIER_HEAVY, TIER_VISION, TIER_LIGHT } from '@/api/types'

// 能力分档元信息：给三档配中文标签 + 职责说明，供「能力分档」页可读渲染。
// 归入本档的 agent agent 不再写死——由 AssignmentView 从 agent.tier 真实数据动态分组，
// 用户在分档页勾选即改 agent.tier（PATCH 移档，与「智能体」页下拉读写同一份数据）。
// builtinKeys 是**代码内建的路由键**（inspector/compactor 等，不在 agent 表、无法改档），
// 仅只读展示——让用户知道这一档还服务哪些非 agent 的旁路调用。
export interface TierMeta {
  tier: string
  label: string
  desc: string
  builtinKeys: string[] // 落本档的内建路由键（llmcfg.agentTierTable 中的非 agent key，只读）
  reserved?: boolean // 保留槽（__fallback__）：非能力档，语义特殊，单列一组
}

// 三个能力档（与后端 llmcfg TierHeavy/TierVision/TierLight 对齐）。
export const TIERS: TierMeta[] = [
  {
    tier: TIER_HEAVY,
    label: '重推理',
    desc: '强文本推理档，编排决策与流量逐批挖洞；未显式归档的 agent 也落此档（隐式默认）',
    builtinKeys: [],
  },
  {
    tier: TIER_VISION,
    label: '多模态',
    desc: '需读图的 active 链路：browser-use 截图驱动的编排与利用',
    builtinKeys: [],
  },
  {
    tier: TIER_LIGHT,
    label: '轻任务',
    desc: '意图分类、摘要与会话历史压缩等旁路轻量调用，轻模型即可省钱',
    builtinKeys: ['inspector', 'compactor'],
  },
]

// 保留槽：唯一的兜底部署，主 provider 重试耗尽后切换。非能力档，UI 单列一组以示区别。
export const RESERVED_TIERS: TierMeta[] = [
  {
    tier: ROLE_FALLBACK,
    label: '兜底部署',
    desc: '任一档主 provider 重试耗尽后切换的备份 provider',
    builtinKeys: [],
    reserved: true,
  },
]

const META_BY_TIER = new Map<string, TierMeta>(
  [...TIERS, ...RESERVED_TIERS].map((m) => [m.tier, m]),
)

/** 取档位元信息；未知档回退为原样 label（无描述、非保留）。 */
export function tierMeta(tier: string): TierMeta {
  return META_BY_TIER.get(tier) ?? { tier, label: tier, desc: '自定义档位', builtinKeys: [] }
}

export function isReservedTier(tier: string): boolean {
  return tier === ROLE_FALLBACK
}
