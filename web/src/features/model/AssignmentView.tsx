import { useCallback, useEffect, useState } from 'react'
import { AlertTriangle, Check, Loader2 } from 'lucide-react'
import { listProviders, getRouting, saveRoleRoute, deleteRoleRoute } from '@/api/models'
import type { ProviderConfig, RoleRouteConfig } from '@/api/types'
import { KNOWN_ROLES, RESERVED_ROLES, roleMeta, type RoleMeta } from './roles'

// 每行的即时保存态：路由写入是单字段（provider_key），无需抽屉——行内 select 改完即存，
// 保存/成功/失败短暂回显在行尾。saving 期间禁用该行 select 防抖动。
type RowState = 'idle' | 'saving' | 'saved' | 'error'

// 角色指派视图：角色 → provider 直连（无别名层）。
// 消费方只认角色语义名，经此一跳落到具体 provider key，换模型不改代码——
// 改指派即改运行期装配（写经 llmstore 失效广播，下次 For(role) 读到最新）。
//
// 布局：两组分区——业务角色（编排/流量分析/督查…）与保留角色（默认兜底 / 重试备份）。
// 每行行内 select 直选 provider，改完即存，无需抽屉（单字段无需分页表单）。
export function AssignmentView() {
  const [providers, setProviders] = useState<ProviderConfig[]>([])
  const [routeMap, setRouteMap] = useState<Map<string, string>>(new Map())
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [rowState, setRowState] = useState<Map<string, RowState>>(new Map())

  const reload = useCallback(() => {
    setLoading(true)
    setError('')
    Promise.all([listProviders(), getRouting()])
      .then(([provs, routing]) => {
        setProviders(provs)
        setRouteMap(new Map(routing.routes.map((r: RoleRouteConfig) => [r.role, r.provider_key])))
      })
      .catch((e) => setError(e instanceof Error ? e.message : '加载失败'))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => reload(), [reload])

  const setRow = (role: string, s: RowState) =>
    setRowState((m) => new Map(m).set(role, s))

  // 选空 → 删除该 role 路由（回落 __default__）；选具体 provider → upsert。
  const onPick = async (role: string, providerKey: string) => {
    setRow(role, 'saving')
    try {
      if (providerKey === '') {
        await deleteRoleRoute(role)
        setRouteMap((m) => {
          const next = new Map(m)
          next.delete(role)
          return next
        })
      } else {
        await saveRoleRoute(role, providerKey)
        setRouteMap((m) => new Map(m).set(role, providerKey))
      }
      setRow(role, 'saved')
      window.setTimeout(() => setRow(role, 'idle'), 1600)
    } catch (e) {
      setRow(role, 'error')
      window.alert(e instanceof Error ? e.message : '保存失败')
    }
  }

  // 已知角色之外、DB 里还存在的自定义角色也要列出（不丢数据）。
  const knownSet = new Set([...KNOWN_ROLES, ...RESERVED_ROLES].map((m) => m.role))
  const extraRoles: RoleMeta[] = [...routeMap.keys()]
    .filter((r) => !knownSet.has(r))
    .map(roleMeta)

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="border-b border-border px-6 py-4">
        <h1 className="tac-prompt font-mono text-[15px] font-semibold text-text">角色指派</h1>
        <p className="mt-0.5 text-[12.5px] text-muted">
          消费方角色一跳直连 provider，换模型不改代码；改指派即经多级缓存热生效
        </p>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-3xl flex-col gap-6 p-6">
          {loading ? (
            <div className="tac-cursor py-16 text-center font-mono text-[13.5px] text-muted">加载中</div>
          ) : error ? (
            <div className="py-16 text-center font-mono text-[13.5px] text-sev-critical">⚠ {error}</div>
          ) : (
            <>
              <RoleGroup
                title="业务角色"
                hint="各消费方按语义名解析部署；未指派的角色回落「默认兜底」"
                roles={[...KNOWN_ROLES, ...extraRoles]}
                providers={providers}
                routeMap={routeMap}
                rowState={rowState}
                onPick={onPick}
                allowUnset
              />
              <RoleGroup
                title="兜底角色"
                hint="替代原全局别名槽：角色未命中走「默认兜底」，主 provider 重试耗尽走「重试备份」"
                roles={RESERVED_ROLES}
                providers={providers}
                routeMap={routeMap}
                rowState={rowState}
                onPick={onPick}
              />
            </>
          )}
        </div>
      </div>
    </div>
  )
}

// 一组角色分区卡：标题 + 说明 + 若干行。复用 SettingsSection 的卡面语汇（描边 + soft glow）。
function RoleGroup({
  title,
  hint,
  roles,
  providers,
  routeMap,
  rowState,
  onPick,
  allowUnset = false,
}: {
  title: string
  hint: string
  roles: RoleMeta[]
  providers: ProviderConfig[]
  routeMap: Map<string, string>
  rowState: Map<string, RowState>
  onPick: (role: string, providerKey: string) => void
  allowUnset?: boolean
}) {
  // provider key → enabled，用于标注指向已停用/悬空部署。
  const provState = new Map(providers.map((p) => [p.key, p.enabled] as const))

  return (
    <section
      aria-label={title}
      className="rounded-xl border border-border bg-surface shadow-[var(--glow-soft)]"
    >
      <div className="border-b border-border px-5 py-3.5">
        <h2 className="font-mono text-[13.5px] font-semibold text-text">{title}</h2>
        <p className="mt-0.5 text-[12px] text-muted">{hint}</p>
      </div>
      <div className="flex flex-col divide-y divide-border">
        {roles.map((m) => (
          <RoleRow
            key={m.role}
            meta={m}
            value={routeMap.get(m.role) ?? ''}
            providers={providers}
            provState={provState}
            state={rowState.get(m.role) ?? 'idle'}
            onPick={onPick}
            allowUnset={allowUnset}
          />
        ))}
      </div>
    </section>
  )
}

// 单角色行：左侧角色标签 + 职责说明，右侧 provider 选择器 + 即时保存态回显。
function RoleRow({
  meta,
  value,
  providers,
  provState,
  state,
  onPick,
  allowUnset,
}: {
  meta: RoleMeta
  value: string
  providers: ProviderConfig[]
  provState: Map<string, boolean>
  state: RowState
  onPick: (role: string, providerKey: string) => void
  allowUnset: boolean
}) {
  const enabled = value ? provState.get(value) : undefined
  const missing = value !== '' && enabled === undefined
  const disabledProv = value !== '' && enabled === false

  return (
    <div className="flex items-center gap-4 px-5 py-3.5">
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="text-[13.5px] font-medium text-text">{meta.label}</span>
          <code className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-faint">
            {meta.role}
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
          className="w-48 rounded-md border border-border bg-surface px-2.5 py-1.5 text-[13px] text-text outline-none transition-shadow focus:border-accent focus:shadow-[var(--glow-accent)] disabled:opacity-60"
          value={value}
          disabled={state === 'saving'}
          onChange={(e) => onPick(meta.role, e.target.value)}
        >
          {allowUnset && <option value="">（未指派 · 回落兜底）</option>}
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
  )
}

// 行尾即时保存态图标：saving 转圈、saved 打勾（emerald）、error 警示（critical）。
function StateBadge({ state }: { state: RowState }) {
  if (state === 'saving') return <Loader2 className="h-3.5 w-3.5 animate-spin text-muted" />
  if (state === 'saved') return <Check className="h-3.5 w-3.5 text-accent" />
  if (state === 'error') return <AlertTriangle className="h-3.5 w-3.5 text-sev-critical" />
  return <span className="h-3.5 w-3.5" aria-hidden />
}
