import { COMPLEXITY_SIMPLE, COMPLEXITY_MEDIUM, COMPLEXITY_COMPLEX } from '@/api/types'

// 复杂度档元信息：给三档配中文标签 + 职责说明，供「能力路由」页可读渲染。
// 归入本档的 agent 不写死——由 AssignmentView 从 agent.complexity 真实数据动态分组，
// 用户在分档页勾选即改 agent.complexity（PATCH 移档，与「智能体」页下拉读写同一份数据）。
export interface ComplexityGroupMeta {
  key: string
  label: string
  desc: string
}

// 三个复杂度档（与后端 llmconfig ComplexitySimple/Medium/Complex 对齐；
// 档名即 llm_role_route.role 路由键）。
// 无保留槽、无任何兜底机制：请求重试耗尽即失败。
export const COMPLEXITY_GROUPS: ComplexityGroupMeta[] = [
  {
    key: COMPLEXITY_COMPLEX,
    label: '深度推理',
    desc: '规划、裁决等长链路推理；强模型承接',
  },
  {
    key: COMPLEXITY_MEDIUM,
    label: '标准推理',
    desc: '常规漏洞检测与工具调用；未显式归档的 agent 也落此档（隐式默认）',
  },
  {
    key: COMPLEXITY_SIMPLE,
    label: '轻量快答',
    desc: '意图分类、摘要与会话历史压缩等旁路轻量调用，轻模型即可省钱',
  },
]

const META_BY_GROUP = new Map<string, ComplexityGroupMeta>(
  COMPLEXITY_GROUPS.map((m) => [m.key, m]),
)

/** 取档位元信息；未知档回退为原样 label（无描述）。 */
export function groupMeta(group: string): ComplexityGroupMeta {
  return META_BY_GROUP.get(group) ?? { key: group, label: group, desc: '自定义档位' }
}
