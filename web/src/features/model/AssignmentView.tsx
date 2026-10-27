import { useMemo, useState } from 'react'
import { AlertTriangle, Loader2, X } from 'lucide-react'
import type { ProviderConfig, AgentConfig } from '@/api/types'
import { COMPLEXITY_GROUPS, groupMeta, type ComplexityGroupMeta } from './roles'
import { providerDisplayName } from './providerForm'
import {
  useProviders,
  useRouting,
  useAgentConfigs,
  useSaveRoleRoute,
  useSaveAgentComplexity,
} from './useModelQueries'

// 能力路由分区：agent → complexity → provider 两跳的绑定表单（非实体 CRUD）。无任何兜底机制。
// 同时读写两处：
//   1. complexity → provider：行内 select 直选部署（useSaveRoleRoute，空值=删路由）。
//   2. agent → 档：每档一排归属 chip，点未归属 chip 即把智能体归入本档
//      （useSaveAgentComplexity 移档，与「智能体」页复杂度下拉读写同一份数据）。
// inspector/compactor 等内建路由键不在 agent 表、无法改档，仅在所属档只读展示。
// 排版：与「模型提供商」分区同构——同一分区头解剖 + 同宽单卡表格行（三档），
// 文档流排布随页面滚动。乐观更新在 useModelQueries 的 mutation 层（onMutate 写缓存 /
// onError 回滚 / onSettled 失效），组件只消费 query data 派生视图。
export function AssignmentView() {
  const providersQuery = useProviders()
  const routingQuery = useRouting()
  const agentsQuery = useAgentConfigs()
  const routeMutation = useSaveRoleRoute()
  const moveMutation = useSaveAgentComplexity()

  const providers = providersQuery.data ?? []
  const agents = agentsQuery.data ?? []
  const loading = providersQuery.isLoading || routingQuery.isLoading || agentsQuery.isLoading
  const loadError =
    providersQuery.error ?? routingQuery.error ?? agentsQuery.error ?? null

  const routeMap = useMemo(
    () => new Map((routingQuery.data?.routes ?? []).map((r) => [r.role, r.provider_code])),
    [routingQuery.data],
  )
  // 归属表由 agents 数据派生；乐观移档经 mutation onMutate 已写入缓存，此处即时反映。
  const complexityBy = useMemo(
    () => new Map(agents.map((h) => [h.id, h.complexity || 'medium'])),
    [agents],
  )

  // 路由/移档失败的分区脚注回显（乐观更新已自动回滚视觉，这里补失败原因），下次操作即清。
  const [actionError, setActionError] = useState('')
  const onError = (prefix: string) => (e: unknown) =>
    setActionError(`${prefix}：${e instanceof Error ? e.message : '操作失败'}`)

  // 选空 → 删除该档路由（该档回落标准推理档解析）；选具体 provider → upsert。
  const onPick = (group: string, providerKey: string) => {
    setActionError('')
    routeMutation.mutate(
      { role: group, providerKey },
      { onError: onError('保存绑定失败') },
    )
  }

  // 移档：点未归属 chip 把 agent 归入目标档（单值归属——移出原档由派生自动落空）。
  const onMove = (agentId: string, group: string) => {
    if (complexityBy.get(agentId) === group) return
    setActionError('')
    moveMutation.mutate(
      { agentId, complexity: group },
      { onError: onError('移档失败') },
    )
  }

  // 已知档之外、DB 里还存在的自定义档也要列出（不丢数据，如历史遗留行）。
  // __fallback__ 是已废弃的保留 role（兜底机制已删）——瞬态残留行不展示。
  const knownSet = new Set(COMPLEXITY_GROUPS.map((m) => m.key))
  const extraGroups: ComplexityGroupMeta[] = [...routeMap.keys()]
    .filter((g) => !knownSet.has(g) && g !== '__fallback__')
    .map(groupMeta)

  const moving = useMemo(() => new Set(moveMutation.isPending ? [moveMutation.variables?.agentId ?? ''] : []), [moveMutation.isPending, moveMutation.variables])

  const rows: ComplexityGroupMeta[] = [...COMPLEXITY_GROUPS, ...extraGroups]

  return (
    <section aria-label="能力路由">
      {/* 卡内头部与行同内边距节奏（与模型提供商卡同构）；回落细节在标准推理行说明中 */}
      <div className="overflow-hidden rounded-lg border border-border bg-surface">
        <div className="border-b border-border px-4 py-3">
          <h2 className="text-sm font-semibold text-text">能力路由</h2>
          <p className="mt-0.5 text-xs text-muted">为每个复杂度档位分配模型，并把智能体归入对应档</p>
        </div>
        {loading ? (
          <div className="tac-cursor py-10 text-center font-mono text-sm text-muted">加载中</div>
        ) : loadError ? (
          <div className="py-10 text-center font-mono text-sm text-sev-critical">
            ⚠ {loadError instanceof Error ? loadError.message : '加载失败'}
          </div>
        ) : (
        <div className="flex flex-col divide-y divide-border">
          {rows.map((m) => (
            <ComplexityRow
              key={m.key}
              meta={m}
              value={routeMap.get(m.key) ?? ''}
              providers={providers}
              routeSavingRole={routeMutation.isPending ? routeMutation.variables?.role : null}
              onPick={onPick}
              agents={agents}
              complexityBy={complexityBy}
              moving={moving}
              onMove={onMove}
            />
          ))}
        </div>
        )}
      </div>

      {actionError && (
        <div className="mt-2 flex items-start gap-2 rounded-lg border border-sev-critical/40 bg-sev-critical/5 px-4 py-2.5">
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-sev-critical" />
          <p className="min-w-0 flex-1 break-all text-xs text-sev-critical">{actionError}</p>
          <button
            type="button"
            onClick={() => setActionError('')}
            aria-label="关闭错误提示"
            className="flex-shrink-0 text-faint transition-colors hover:text-text"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        </div>
      )}
    </section>
  )
}

// 单档行：档位标签 + 职责说明 + provider 选择器；下方一行 agent 归属 chip。
// 选空 = 删除该档绑定（回落标准推理档解析）。
function ComplexityRow({
  meta,
  value,
  providers,
  routeSavingRole,
  onPick,
  agents,
  complexityBy,
  moving,
  onMove,
}: {
  meta: ComplexityGroupMeta
  value: string
  providers: ProviderConfig[]
  routeSavingRole: string | null
  onPick: (group: string, providerKey: string) => void
  agents: AgentConfig[]
  complexityBy: Map<string, string>
  moving: Set<string>
  onMove: (agentId: string, group: string) => void
}) {
  const provState = new Map(providers.map((p) => [p.code, p.enabled] as const))
  const enabled = value ? provState.get(value) : undefined
  const missing = value !== '' && enabled === undefined
  const disabledProv = value !== '' && enabled === false
  const saving = routeSavingRole === meta.key

  return (
    <div data-testid={`complexity-row-${meta.key}`} className="flex flex-col gap-3 px-4 py-3.5 transition-colors hover:bg-surface-2/50">
      <div className="flex items-center gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium text-text">{meta.label}</span>
            <code className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-faint">
              {meta.key}
            </code>
            {missing && (
              <span className="flex items-center gap-1 text-[11.5px] text-sev-critical">
                <AlertTriangle className="h-3 w-3" />
                部署缺失
              </span>
            )}
            {disabledProv && (
              <span className="flex items-center gap-1 text-[11.5px] text-sev-high">
                <AlertTriangle className="h-3 w-3" />
                部署停用
              </span>
            )}
          </div>
          <p className="mt-0.5 truncate text-xs text-muted">{meta.desc}</p>
        </div>

        <div className="flex flex-shrink-0 items-center gap-2">
          {saving && <Loader2 className="h-3.5 w-3.5 animate-spin text-muted" aria-label="保存中" />}
          <select
            aria-label={`${meta.label} 绑定部署`}
            className="w-40 rounded-lg border border-border bg-surface-2 px-2.5 py-1.5 text-sm text-text outline-none transition-colors focus:border-accent disabled:opacity-60"
            value={value}
            disabled={saving}
            onChange={(e) => onPick(meta.key, e.target.value)}
          >
            <option value="">（未配置 · 回落标准推理档）</option>
            {providers.map((p) => (
              <option key={p.code} value={p.code}>
                {providerDisplayName(p)}
                {p.enabled ? '' : '（已停用）'}
              </option>
            ))}
          </select>
        </div>
      </div>

      <AgentPicker
          group={meta.key}
          groupLabel={meta.label}
          agents={agents}
          complexityBy={complexityBy}
          moving={moving}
          onMove={onMove}
        />
    </div>
  )
}

// 单档的 agent 归属胶囊区（与工具模块装配胶囊同一设计：同尺寸、仅配色区分——
// 归属本档 = accent 高亮，未归属 = 幽灵可点移入；单值归属，归属档点击无操作）。
// 全部渲染为 button + aria-pressed 表达归属态；头部带「已归入 N/M」计数。
function AgentPicker({
  group,
  groupLabel,
  agents,
  complexityBy,
  moving,
  onMove,
}: {
  group: string
  groupLabel: string
  agents: AgentConfig[]
  complexityBy: Map<string, string>
  moving: Set<string>
  onMove: (agentId: string, group: string) => void
}) {
  const inCount = agents.filter((h) => (complexityBy.get(h.id) ?? 'medium') === group).length
  return (
    <div
      role="group"
      aria-label={`${groupLabel} 归属`}
      className="flex flex-wrap items-center gap-1.5 rounded-md bg-surface-2/40 px-3 py-2"
    >
      <span className="mr-1 text-[11px] text-faint">
        智能体 · 已归入 {inCount}/{agents.length}
      </span>
      {agents.map((h) => {
        const inGroup = (complexityBy.get(h.id) ?? 'medium') === group
        const busy = moving.has(h.id)
        return (
          <button
            key={h.id}
            type="button"
            aria-pressed={inGroup}
            disabled={busy || inGroup}
            onClick={() => onMove(h.id, group)}
            className={
              'rounded-md border px-2.5 py-1 text-sm transition-colors disabled:cursor-default ' +
              (inGroup
                ? 'border-accent bg-accent/15 text-text'
                : 'border-border bg-background text-muted hover:border-accent/50 hover:text-text')
            }
          >
            {busy && <Loader2 className="mr-1 inline h-3 w-3 animate-spin" />}
            {h.name}
          </button>
        )
      })}
    </div>
  )
}
