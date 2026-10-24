import { useMemo, useState } from 'react'
import { AlertTriangle, Check, Loader2, X } from 'lucide-react'
import type { ProviderConfig, AgentConfig } from '@/api/types'
import { COMPLEXITY_GROUPS, RESERVED_GROUPS, groupMeta, type ComplexityGroupMeta } from './roles'
import {
  useProviders,
  useRouting,
  useAgentConfigs,
  useSaveRoleRoute,
  useSaveAgentComplexity,
} from './useModelQueries'

// 能力分档视图：agent → complexity → provider 两跳。本视图同时读写两处：
//   1. complexity → provider：行内 select 直选部署（useSaveRoleRoute，空值=删路由）。
//   2. agent → 档：每档一排归属 chip，点未归属 chip 即把智能体归入本档
//      （useSaveAgentComplexity 移档，与「智能体」页复杂度下拉读写同一份数据）。
// inspector/compactor 等内建路由键不在 agent 表、无法改档，仅在所属档只读展示。
// 乐观更新在 useModelQueries 的 mutation 层（onMutate 写缓存 / onError 回滚 / onSettled 失效），
// 组件只消费 query data 派生视图，不再自管回滚快照。
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

  // 路由/移档失败的页面内回显（乐观更新已自动回滚视觉，这里补失败原因），下次操作即清。
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
  const knownSet = new Set([...COMPLEXITY_GROUPS, ...RESERVED_GROUPS].map((m) => m.key))
  const extraGroups: ComplexityGroupMeta[] = [...routeMap.keys()]
    .filter((g) => !knownSet.has(g))
    .map(groupMeta)

  const moving = useMemo(() => new Set(moveMutation.isPending ? [moveMutation.variables?.agentId ?? ''] : []), [moveMutation.isPending, moveMutation.variables])

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-3xl flex-col gap-5 px-6 py-5">
          {loading ? (
            <div className="tac-cursor py-16 text-center font-mono text-sm text-muted">加载中</div>
          ) : loadError ? (
            <div className="py-16 text-center font-mono text-sm text-sev-critical">
              ⚠ {loadError instanceof Error ? loadError.message : '加载失败'}
            </div>
          ) : (
            <>
              <ComplexityGroup
                title="能力分配"
                hint="为每个复杂度档位分配模型；标准推理为默认档，未显式分配的智能体使用此档"
                groups={[...COMPLEXITY_GROUPS, ...extraGroups]}
                providers={providers}
                routeMap={routeMap}
                routeSavingRole={routeMutation.isPending ? routeMutation.variables?.role : null}
                onPick={onPick}
                agents={agents}
                complexityBy={complexityBy}
                moving={moving}
                onMove={onMove}
                allowUnset
              />
              <ComplexityGroup
                title="兜底槽"
                hint="任一档主 provider 重试耗尽后切换的兜底部署；未绑定时无兜底（各档耗尽即失败）"
                groups={RESERVED_GROUPS}
                providers={providers}
                routeMap={routeMap}
                routeSavingRole={routeMutation.isPending ? routeMutation.variables?.role : null}
                onPick={onPick}
                agents={agents}
                complexityBy={complexityBy}
                moving={moving}
                onMove={onMove}
              />
            </>
          )}
        </div>
      </div>

      {actionError && (
        <div className="flex flex-shrink-0 items-start gap-2 border-t border-sev-critical/40 bg-sev-critical/5 px-6 py-3">
          <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0 text-sev-critical" />
          <p className="min-w-0 flex-1 break-all text-sm text-sev-critical">{actionError}</p>
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
    </div>
  )
}

// 一组分档分区卡：标题 + 说明 + 若干行。对齐「部署」视图的卡面语汇——
// 平面描边卡（rounded-lg + border，无 glow），tac-prompt font-mono 标题，与 provider 面板同构。
function ComplexityGroup({
  title,
  hint,
  groups,
  providers,
  routeMap,
  routeSavingRole,
  onPick,
  agents,
  complexityBy,
  moving,
  onMove,
  allowUnset = false,
}: {
  title: string
  hint: string
  groups: ComplexityGroupMeta[]
  providers: ProviderConfig[]
  routeMap: Map<string, string>
  routeSavingRole: string | null
  onPick: (group: string, providerKey: string) => void
  agents: AgentConfig[]
  complexityBy: Map<string, string>
  moving: Set<string>
  onMove: (agentId: string, group: string) => void
  allowUnset?: boolean
}) {
  const provState = new Map(providers.map((p) => [p.code, p.enabled] as const))

  return (
    <section aria-label={title} className="overflow-hidden rounded-lg border border-border bg-surface">
      <div className="border-b border-border px-5 py-4">
        <h2 className="tac-prompt font-mono text-sm font-semibold text-text">{title}</h2>
        <p className="mt-0.5 text-xs text-muted">{hint}</p>
      </div>
      <div className="flex flex-col divide-y divide-border">
        {groups.map((m) => (
          <ComplexityRow
            key={m.key}
            meta={m}
            value={routeMap.get(m.key) ?? ''}
            providers={providers}
            provState={provState}
            saving={routeSavingRole === m.key}
            onPick={onPick}
            agents={agents}
            complexityBy={complexityBy}
            moving={moving}
            onMove={onMove}
            allowUnset={allowUnset}
          />
        ))}
      </div>
    </section>
  )
}

// 单档行：左侧档位标签 + 职责说明 + provider 选择器；下方一行 agent 归属 chip。
// 兜底槽（reserved）无 agent 归属语义，不渲染归属区。
function ComplexityRow({
  meta,
  value,
  providers,
  provState,
  saving,
  onPick,
  agents,
  complexityBy,
  moving,
  onMove,
  allowUnset,
}: {
  meta: ComplexityGroupMeta
  value: string
  providers: ProviderConfig[]
  provState: Map<string, boolean>
  saving: boolean
  onPick: (group: string, providerKey: string) => void
  agents: AgentConfig[]
  complexityBy: Map<string, string>
  moving: Set<string>
  onMove: (agentId: string, group: string) => void
  allowUnset: boolean
}) {
  const enabled = value ? provState.get(value) : undefined
  const missing = value !== '' && enabled === undefined
  const disabledProv = value !== '' && enabled === false

  return (
    <div className="flex flex-col gap-3 px-5 py-4 transition-colors hover:bg-surface-2/50">
      <div className="flex items-center gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium text-text">{meta.label}</span>
            <code className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-xs text-faint">
              {meta.key}
            </code>
            {missing && (
              <span className="flex items-center gap-1 text-xs text-sev-critical">
                <AlertTriangle className="h-3 w-3" />
                部署缺失
              </span>
            )}
            {disabledProv && (
              <span className="flex items-center gap-1 text-xs text-sev-high">
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
            className="w-56 rounded-lg border border-border bg-surface-2 px-2.5 py-1.5 text-sm text-text outline-none transition-colors focus:border-accent disabled:opacity-60"
            value={value}
            disabled={saving}
            onChange={(e) => onPick(meta.key, e.target.value)}
          >
            {allowUnset && <option value="">（未配置 · 回落标准推理档）</option>}
            {!allowUnset && value === '' && <option value="">（未绑定 · 无兜底）</option>}
            {providers.map((p) => (
              <option key={p.code} value={p.code}>
                {p.code}
                {p.enabled ? '' : '（已停用）'}
              </option>
            ))}
          </select>
        </div>
      </div>

      {!meta.reserved && (
        <AgentPicker
          group={meta.key}
          groupLabel={meta.label}
          builtinKeys={meta.builtinKeys}
          agents={agents}
          complexityBy={complexityBy}
          moving={moving}
          onMove={onMove}
        />
      )}
    </div>
  )
}

// 单档的 agent 归属 chip 区：归属本档 = accent 实心（Check 标记，只表达状态），
// 未归属 = 幽灵 chip 可点击移入（单值归属——移出原档由派生数据自动落空）。
// 用 chip 而非 checkbox：checkbox 的多选视觉与「单值归属」语义冲突，且本档内
// 勾选态 disabled 的复选框看起来像控件损坏；实心 chip 是明确的归属标记。
// builtinKeys 只读 code chip（内建路由键，不在智能体表）。
function AgentPicker({
  group,
  groupLabel,
  builtinKeys,
  agents,
  complexityBy,
  moving,
  onMove,
}: {
  group: string
  groupLabel: string
  builtinKeys: string[]
  agents: AgentConfig[]
  complexityBy: Map<string, string>
  moving: Set<string>
  onMove: (agentId: string, group: string) => void
}) {
  return (
    <div className="flex flex-wrap items-center gap-1.5 rounded-md bg-surface-2/40 px-3 py-2">
      <span className="mr-1 text-xs text-faint">智能体</span>
      {agents.map((h) => {
        const inGroup = (complexityBy.get(h.id) ?? 'medium') === group
        const busy = moving.has(h.id)
        return inGroup ? (
          <span
            key={h.id}
            title={`已归入${groupLabel}；改档请在目标档点击`}
            className="flex items-center gap-1 rounded border border-accent/50 bg-accent/15 px-1.5 py-0.5 text-xs text-accent"
          >
            <Check className="h-3 w-3" />
            {h.name}
          </span>
        ) : (
          <button
            key={h.id}
            type="button"
            onClick={() => onMove(h.id, group)}
            disabled={busy}
            aria-label={`${h.name} 归入 ${group}`}
            title={`点击把 ${h.name} 归入${groupLabel}`}
            className="rounded border border-transparent px-1.5 py-0.5 text-xs text-muted transition-colors hover:border-accent/40 hover:bg-accent/10 hover:text-accent disabled:opacity-50"
          >
            {busy && <Loader2 className="mr-1 inline h-3 w-3 animate-spin" />}
            {h.name}
          </button>
        )
      })}
      {builtinKeys.map((k) => (
        <code
          key={k}
          title="内建路由键（不在智能体表，不可改档）"
          className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-xs text-faint"
        >
          {k}
        </code>
      ))}
    </div>
  )
}
