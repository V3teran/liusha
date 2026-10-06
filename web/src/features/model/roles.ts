import { ROLE_FALLBACK, COMPLEXITY_SIMPLE, COMPLEXITY_MEDIUM, COMPLEXITY_COMPLEX } from '@/api/types'

// 复杂度档元信息：给三档配中文标签 + 职责说明，供「能力分档」页可读渲染。
// 归入本档的 agent 不写死——由 AssignmentView 从 agent.complexity 真实数据动态分组，
// 用户在分档页勾选即改 agent.complexity（PATCH 移档，与「智能体」页下拉读写同一份数据）。
// builtinKeys 是**代码内建的路由键**（inspector/compactor 等，不在 agent 表、无法改档），
// 仅只读展示——让用户知道这一档还服务哪些非 agent 的旁路调用。
export interface ComplexityGroupMeta {
  key: string
  label: string
  desc: string
  builtinKeys: string[] // 落本档的内建路由键（llmcfg.agentComplexityTable 中的非 agent key，只读）
  reserved?: boolean // 保留槽（__fallback__）：非档位，语义特殊，单列一组
}

// 三个复杂度档（与后端 llmcfg ComplexitySimple/Medium/Complex 对齐；
// 档名即 llm_role_route.role 路由键）。
export const COMPLEXITY_GROUPS: ComplexityGroupMeta[] = [
  {
    key: COMPLEXITY_COMPLEX,
    label: '深度推理',
    desc: '规划、裁决等长链路推理；强模型承接',
    builtinKeys: [],
  },
  {
    key: COMPLEXITY_MEDIUM,
    label: '标准推理',
    desc: '常规漏洞检测与工具调用；未显式归档的 agent 也落此档（隐式默认）',
    builtinKeys: [],
  },
  {
    key: COMPLEXITY_SIMPLE,
    label: '轻量快答',
    desc: '意图分类、摘要与会话历史压缩等旁路轻量调用，轻模型即可省钱',
    builtinKeys: ['inspector', 'compactor'],
  },
]

// 保留槽：唯一的兜底部署，主 provider 重试耗尽后切换。非档位，UI 单列一组以示区别。
export const RESERVED_GROUPS: ComplexityGroupMeta[] = [
  {
    key: ROLE_FALLBACK,
    label: '兜底部署',
    desc: '任一档主 provider 重试耗尽后切换的备份 provider',
    builtinKeys: [],
    reserved: true,
  },
]

const META_BY_GROUP = new Map<string, ComplexityGroupMeta>(
  [...COMPLEXITY_GROUPS, ...RESERVED_GROUPS].map((m) => [m.key, m]),
)

/** 取档位元信息；未知档回退为原样 label（无描述、非保留）。 */
export function groupMeta(group: string): ComplexityGroupMeta {
  return META_BY_GROUP.get(group) ?? { key: group, label: group, desc: '自定义档位', builtinKeys: [] }
}

export function isReservedGroup(group: string): boolean {
  return group === ROLE_FALLBACK
}
