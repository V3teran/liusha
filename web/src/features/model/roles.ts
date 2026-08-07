import { ROLE_DEFAULT, ROLE_FALLBACK } from '@/api/types'

// 角色元信息：给已知消费方角色配中文标签 + 一句职责说明，路由表可读性远高于裸 role 字面。
// 未在此表的 role 原样展示（自定义角色/未来扩展），不阻塞。
export interface RoleMeta {
  role: string
  label: string
  desc: string
  reserved?: boolean // 保留角色（__default__/__fallback__）：语义特殊，单列一组
}

// 消费方角色（与 config.yaml agents 段对齐）。运行期真正走 For(role) 的是
// traffic-analysis / orchestrator / inspector；exploitation 经 deep task 复用 orchestrator。
export const KNOWN_ROLES: RoleMeta[] = [
  { role: 'orchestrator', label: '编排主代理', desc: 'active 主代理，派活决策，含 browser-use 截图（多模态）' },
  { role: 'traffic-analysis', label: '流量分析', desc: 'passive 单代理，流量驱动逐批挖洞（纯文本路径）' },
  { role: 'exploitation', label: '利用子代理', desc: 'active 深挖单点，经 deep task 复用编排主代理模型' },
  { role: 'inspector', label: '督查 / 压缩', desc: '旁路过程督查官与会话历史压缩（轻模型即可）' },
]

// 保留角色：替代原全局别名槽，语义特殊，UI 单列「兜底」一组以示区别。
export const RESERVED_ROLES: RoleMeta[] = [
  { role: ROLE_DEFAULT, label: '默认兜底', desc: '角色未命中任何指派时的兜底 provider', reserved: true },
  { role: ROLE_FALLBACK, label: '重试备份', desc: '主 provider 重试耗尽后切换的备份 provider', reserved: true },
]

const META_BY_ROLE = new Map<string, RoleMeta>(
  [...KNOWN_ROLES, ...RESERVED_ROLES].map((m) => [m.role, m]),
)

/** 取角色元信息；未知角色回退为原样 label（无描述、非保留）。 */
export function roleMeta(role: string): RoleMeta {
  return META_BY_ROLE.get(role) ?? { role, label: role, desc: '自定义角色' }
}

export function isReservedRole(role: string): boolean {
  return role === ROLE_DEFAULT || role === ROLE_FALLBACK
}
