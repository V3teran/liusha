import { COMPLEXITY_SIMPLE, COMPLEXITY_MEDIUM, COMPLEXITY_COMPLEX } from '@/api/types'

// 复杂度档元信息：给三档配中文标签，供「能力路由」页与智能体页可读渲染。
// 归入本档的 agent 不写死——由 AssignmentView 从 agent.complexity 真实数据动态分组，
// 用户在分档页勾选即改 agent.complexity（PATCH 移档，与「智能体」页下拉读写同一份数据）。
export interface ComplexityGroupMeta {
  key: string
  label: string
}

// 三个复杂度档（与后端 llmconfig ComplexitySimple/Medium/Complex 对齐；
// 档名即 llm_role_route.role 路由键）。
// 无保留槽、无任何兜底机制：请求重试耗尽即失败。
export const COMPLEXITY_GROUPS: ComplexityGroupMeta[] = [
  {
    key: COMPLEXITY_COMPLEX,
    label: '深度推理',
  },
  {
    key: COMPLEXITY_MEDIUM,
    label: '标准推理',
  },
  {
    key: COMPLEXITY_SIMPLE,
    label: '轻量快答',
  },
]

const META_BY_GROUP = new Map<string, ComplexityGroupMeta>(
  COMPLEXITY_GROUPS.map((m) => [m.key, m]),
)

/** 取档位元信息；未知档回退为原样 label。 */
export function groupMeta(group: string): ComplexityGroupMeta {
  return META_BY_GROUP.get(group) ?? { key: group, label: group }
}
