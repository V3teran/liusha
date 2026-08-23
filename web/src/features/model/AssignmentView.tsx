import { useCallback, useEffect, useState } from 'react'
import { AlertTriangle, Check, Loader2 } from 'lucide-react'
import { listProviders, getRouting, saveRoleRoute, deleteRoleRoute } from '@/api/models'
import { listAgentConfigs, saveAgentTier } from '@/api/config'
import type { ProviderConfig, RoleRouteConfig, AgentConfig } from '@/api/types'
import { TIERS, RESERVED_TIERS, tierMeta, type TierMeta } from './roles'

// 每档的即时保存态：路由写入是单字段（provider_key），无需抽屉——行内 select 改完即存，
// 保存/成功/失败短暂回显在行尾。saving 期间禁用该行 select 防抖动。
type RowState = 'idle' | 'saving' | 'saved' | 'error'

// 能力分档视图：agent → tier → provider 两跳。本视图同时读写两处：
//   1. tier → provider：行内 select 直选部署（saveRoleRoute/deleteRoleRoute）。
//   2. agent → tier：每档一个 agent 多选框，勾选即把智能体归入本档（saveAgentTier 移档）。
//      与「智能体」页的能力档下拉读写同一份 agent.tier 数据，只是展示维度不同（按档聚合 vs 单体编辑）。
// inspector/compactor 等内建路由键不在 agent 表、无法改档，仅在所属档只读展示。
export function AssignmentView() {
  const [providers, setProviders] = useState<ProviderConfig[]>([])
  const [routeMap, setRouteMap] = useState<Map<string, string>>(new Map())
  const [agents, setAgents] = useState<AgentConfig[]>([])
  // agentId → tier 的当前归属（乐观移档只改这张表，行渲染由它派生）。
  const [tierBy, setTierBy] = useState<Map<string, string>>(new Map())
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [rowState, setRowState] = useState<Map<string, RowState>>(new Map())
  // 正在移档的 agentId，禁用其复选框防连点。
  const [moving, setMoving] = useState<Set<string>>(new Set())

  const reload = useCallback(() => {
    setLoading(true)
    setError('')
    Promise.all([listProviders(), getRouting(), listAgentConfigs()])
      .then(([provs, routing, hs]) => {
        setProviders(provs)
        setRouteMap(new Map(routing.routes.map((r: RoleRouteConfig) => [r.role, r.provider_key])))
        setAgents(hs)
        setTierBy(new Map(hs.map((h) => [h.id, h.tier || 'heavy'])))
      })
      .catch((e) => setError(e instanceof Error ? e.message : '加载失败'))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => reload(), [reload])

  const setRow = (tier: string, s: RowState) => setRowState((m) => new Map(m).set(tier, s))

  // 选空 → 删除该档路由（重推理档回落即无解析，其余档回落 heavy）；选具体 provider → upsert。
  // 乐观更新：先落 routeMap（select 立即反映新值），失败再回滚到快照并弹错。
  const onPick = async (tier: string, providerKey: string) => {
    const prev = routeMap
    setRow(tier, 'saving')
    setRouteMap((m) => {
      const next = new Map(m)
      if (providerKey === '') next.delete(tier)
      else next.set(tier, providerKey)
      return next
    })
    try {
      if (providerKey === '') await deleteRoleRoute(tier)
      else await saveRoleRoute(tier, providerKey)
      setRow(tier, 'saved')
      window.setTimeout(() => setRow(tier, 'idle'), 1600)
    } catch (e) {
      setRouteMap(prev)
      setRow(tier, 'error')
      window.alert(e instanceof Error ? e.message : '保存失败')
    }
  }

  // 移档：勾选把 agent 归入目标档（离开原档）。乐观改 tierBy，失败回滚并弹错。
  // 单值归属——勾选即隐式移出原档，无需显式取消（原档复选框由 tierBy 派生自动落空）。
  const onMove = async (agentId: string, tier: string) => {
    if (tierBy.get(agentId) === tier) return // 已在本档，无操作
    const prev = tierBy
    setTierBy((m) => new Map(m).set(agentId, tier))
    setMoving((s) => new Set(s).add(agentId))
    try {
      await saveAgentTier(agentId, tier)
    } catch (e) {
      setTierBy(prev)
      window.alert(e instanceof Error ? e.message : '移档失败')
    } finally {
      setMoving((s) => {
        const next = new Set(s)
        next.delete(agentId)
        return next
      })
    }
  }

  // 已知档之外、DB 里还存在的自定义档也要列出（不丢数据，如历史遗留行）。
  const knownSet = new Set([...TIERS, ...RESERVED_TIERS].map((m) => m.tier))
  const extraTiers: TierMeta[] = [...routeMap.keys()]
    .filter((t) => !knownSet.has(t))
    .map(tierMeta)

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-3xl flex-col gap-5 px-6 py-5">
          {loading ? (
            <div className="tac-cursor py-16 text-center font-mono text-[13.5px] text-muted">加载中</div>
          ) : error ? (
            <div className="py-16 text-center font-mono text-[13.5px] text-sev-critical">⚠ {error}</div>
          ) : (
            <>
              <TierGroup
                title="能力档"
                hint="每档绑定一个部署，并勾选归入本档的智能体；重推理为隐式默认档，未显式归档的落此"
                tiers={[...TIERS, ...extraTiers]}
                providers={providers}
                routeMap={routeMap}
                rowState={rowState}
                onPick={onPick}
                agents={agents}
                tierBy={tierBy}
                moving={moving}
                onMove={onMove}
                allowUnset
              />
              <TierGroup
                title="兜底槽"
                hint="任一档主 provider 重试耗尽后切换的兜底部署 provider"
                tiers={RESERVED_TIERS}
                providers={providers}
                routeMap={routeMap}
                rowState={rowState}
                onPick={onPick}
                agents={agents}
                tierBy={tierBy}
                moving={moving}
                onMove={onMove}
              />
            </>
          )}
        </div>
      </div>
    </div>
  )
}

// 一组分档分区卡：标题 + 说明 + 若干行。对齐「部署」视图的卡面语汇——
// 平面描边卡（rounded-lg + border，无 glow），tac-prompt font-mono 标题，与 provider 面板同构。
function TierGroup({
  title,
  hint,
  tiers,
  providers,
  routeMap,
  rowState,
  onPick,
  agents,
  tierBy,
  moving,
  onMove,
  allowUnset = false,
}: {
  title: string
  hint: string
  tiers: TierMeta[]
  providers: ProviderConfig[]
  routeMap: Map<string, string>
  rowState: Map<string, RowState>
  onPick: (tier: string, providerKey: string) => void
  agents: AgentConfig[]
  tierBy: Map<string, string>
  moving: Set<string>
  onMove: (agentId: string, tier: string) => void
  allowUnset?: boolean
}) {
  const provState = new Map(providers.map((p) => [p.key, p.enabled] as const))

  return (
    <section aria-label={title} className="overflow-hidden rounded-lg border border-border bg-surface">
      <div className="border-b border-border px-5 py-3.5">
        <h2 className="tac-prompt font-mono text-[14px] font-semibold text-text">{title}</h2>
        <p className="mt-0.5 text-[12px] text-muted">{hint}</p>
      </div>
      <div className="flex flex-col divide-y divide-border">
        {tiers.map((m) => (
          <TierRow
            key={m.tier}
            meta={m}
            value={routeMap.get(m.tier) ?? ''}
            providers={providers}
            provState={provState}
            state={rowState.get(m.tier) ?? 'idle'}
            onPick={onPick}
            agents={agents}
            tierBy={tierBy}
            moving={moving}
            onMove={onMove}
            allowUnset={allowUnset}
          />
        ))}
      </div>
    </section>
  )
}

// 单档行：左侧档位标签 + 职责说明 + provider 选择器；下方一行 agent 复选框（勾选即移入本档）。
// 兜底槽（reserved）无 agent 归属语义，不渲染多选区。
function TierRow({
  meta,
  value,
  providers,
  provState,
  state,
  onPick,
  agents,
  tierBy,
  moving,
  onMove,
  allowUnset,
}: {
  meta: TierMeta
  value: string
  providers: ProviderConfig[]
  provState: Map<string, boolean>
  state: RowState
  onPick: (tier: string, providerKey: string) => void
  agents: AgentConfig[]
  tierBy: Map<string, string>
  moving: Set<string>
  onMove: (agentId: string, tier: string) => void
  allowUnset: boolean
}) {
  const enabled = value ? provState.get(value) : undefined
  const missing = value !== '' && enabled === undefined
  const disabledProv = value !== '' && enabled === false

  return (
    <div className="flex flex-col gap-3 px-5 py-3.5 transition-colors hover:bg-surface-2/50">
      <div className="flex items-center gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[13.5px] font-medium text-text">{meta.label}</span>
            <code className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-faint">
              {meta.tier}
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
          <p className="mt-0.5 truncate text-[12px] text-muted">{meta.desc}</p>
        </div>

        <div className="flex flex-shrink-0 items-center gap-2">
          <StateBadge state={state} />
          <select
            aria-label={`${meta.label} 绑定部署`}
            className="w-48 rounded-lg border border-border bg-surface-2 px-2.5 py-1.5 text-[13px] text-text outline-none transition-colors focus:border-accent disabled:opacity-60"
            value={value}
            disabled={state === 'saving'}
            onChange={(e) => onPick(meta.tier, e.target.value)}
          >
            {allowUnset && <option value="">（未配置 · 回落重推理档）</option>}
            {!allowUnset && value === '' && <option value="">（未绑定）</option>}
            {providers.map((p) => (
              <option key={p.key} value={p.key}>
                {p.key}
                {p.enabled ? '' : '（已停用）'}
              </option>
            ))}
          </select>
        </div>
      </div>

      {!meta.reserved && (
        <AgentPicker
          tier={meta.tier}
          builtinKeys={meta.builtinKeys}
          agents={agents}
          tierBy={tierBy}
          moving={moving}
          onMove={onMove}
        />
      )}
    </div>
  )
}

// 单档的 agent 归属多选区：每个 agent 一个复选框，勾选即移入本档（saveAgentTier）。
// 单值归属——不属于本档的 agent 复选框空勾，勾上即从原档移出。builtinKeys 只读 chip。
function AgentPicker({
  tier,
  builtinKeys,
  agents,
  tierBy,
  moving,
  onMove,
}: {
  tier: string
  builtinKeys: string[]
  agents: AgentConfig[]
  tierBy: Map<string, string>
  moving: Set<string>
  onMove: (agentId: string, tier: string) => void
}) {
  return (
    <div className="flex flex-wrap items-center gap-1.5 rounded-md bg-surface-2/40 px-3 py-2">
      <span className="mr-1 text-[11px] text-faint">智能体</span>
      {agents.map((h) => {
        const inTier = (tierBy.get(h.id) ?? 'heavy') === tier
        const busy = moving.has(h.id)
        return (
          <label
            key={h.id}
            className={`flex cursor-pointer items-center gap-1.5 rounded px-1.5 py-0.5 text-[12px] transition-colors ${
              inTier ? 'bg-accent/15 text-accent' : 'text-muted hover:bg-surface-2'
            } ${busy ? 'opacity-50' : ''}`}
          >
            <input
              type="checkbox"
              className="h-3 w-3 accent-accent"
              checked={inTier}
              disabled={busy || inTier}
              onChange={() => onMove(h.id, tier)}
              aria-label={`${h.name} 归入 ${tier}`}
            />
            {h.name}
          </label>
        )
      })}
      {builtinKeys.map((k) => (
        <code
          key={k}
          title="内建路由键（不在智能体表，不可改档）"
          className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[10.5px] text-faint"
        >
          {k}
        </code>
      ))}
    </div>
  )
}

// 行尾即时保存态图标：saving 转圈、saved 打勾、error 警示。
function StateBadge({ state }: { state: RowState }) {
  if (state === 'saving') return <Loader2 className="h-3.5 w-3.5 animate-spin text-muted" />
  if (state === 'saved') return <Check className="h-3.5 w-3.5 text-accent" />
  if (state === 'error') return <AlertTriangle className="h-3.5 w-3.5 text-sev-critical" />
  return <span className="h-3.5 w-3.5" aria-hidden />
}
