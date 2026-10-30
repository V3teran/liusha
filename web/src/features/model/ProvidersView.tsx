import { useEffect, useMemo, useRef, useState } from 'react'
import { useDirtyDraft } from '@/hooks/useDirtyDraft'
import { ChevronRight, Plus, Search, Trash2 } from 'lucide-react'
import type { ProviderConfig } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import { TYPE_LABEL, blankProvider, providerDisplayName, providerInvalid } from './providerForm'
import { ProviderEditor } from './ProviderEditor'
import {
  useProviders,
  useSaveProvider,
  useDeleteProvider,
  useRestoreProviderEnvKey,
} from './useModelQueries'

// 模型提供商分区：设置页内的实体管理。列表 = 表格行；点击行在行下方**下拉展开**编辑表单
// （手风琴式，无弹窗），再点行收起。同一时刻至多展开一行。
// 数据经 useModelQueries；保存/删除 invalidate 后列表自动刷新，与能力路由共享 providers 缓存。
// 草稿模型：展开即拍 baseline，dirty 驱动保存可用/重置/收起与切换确认（不静默丢弃草稿）。
export function ProvidersView() {
  const { data: providers = [], isLoading, isError, error, refetch } = useProviders()
  const saveMutation = useSaveProvider()
  const deleteMutation = useDeleteProvider()
  const restoreMutation = useRestoreProviderEnvKey()

  const [query, setQuery] = useState('')
  const { draft, setDraft, open: openDraftState, close: closeDraftState, isDirty: dirty, commit, reset: resetDraft } =
    useDirtyDraft<ProviderConfig>()
  const [isNew, setIsNew] = useState(false)
  // dirty 时被拦截的收起/切换意图：确认条上「丢弃」按钮据此执行。
  const [pending, setPending] = useState<
    { type: 'collapse' } | { type: 'switch'; p: ProviderConfig | null } | null
  >(null)
  const [actionError, setActionError] = useState('')
  // 行级两段式删除：首击亮「确认删除？」，再击才真正删除；3 秒无操作自动回退防误触。
  const [confirmingDeleteCode, setConfirmingDeleteCode] = useState<string | null>(null)
  const deleteTimer = useRef<number>(0)

  const startEdit = (p: ProviderConfig | null) => {
    openDraftState(p ?? blankProvider())
    setIsNew(!p)
    setPending(null)
    setActionError('')
  }
  // 收起当前展开行（dirty 先拦截）。
  const requestCollapse = () => {
    if (dirty) {
      setPending({ type: 'collapse' })
      return
    }
    collapse()
  }
  const collapse = () => {
    closeDraftState()
    setIsNew(false)
    setPending(null)
    setActionError('')
  }
  // 点另一行：dirty 先拦截（确认条「丢弃并切换」完成被拦动作）。
  const requestSwitch = (p: ProviderConfig) => {
    if (dirty) {
      setPending({ type: 'switch', p })
      return
    }
    startEdit(p)
  }
  const discardPending = () => {
    const intent = pending
    setPending(null)
    if (intent?.type === 'switch') startEdit(intent.p)
    else collapse()
  }

  useEffect(() => () => window.clearTimeout(deleteTimer.current), [])

  const armRowDelete = (code: string) => {
    setConfirmingDeleteCode(code)
    window.clearTimeout(deleteTimer.current)
    deleteTimer.current = window.setTimeout(() => setConfirmingDeleteCode(null), 3000)
  }

  // 行级删除：成功后若删的是展开行则收起；失败则展开该行把原因回显在编辑面板里。
  const onRowDelete = (code: string) => {
    window.clearTimeout(deleteTimer.current)
    setConfirmingDeleteCode(null)
    setActionError('')
    deleteMutation.mutate(code, {
      onSuccess: () => {
        if (draft?.code === code && !isNew) collapse()
      },
      onError: (e) => {
        const target = providers.find((p) => p.code === code)
        if (target && draft?.code !== code) startEdit(target)
        setActionError(e instanceof Error ? e.message : '删除失败')
      },
    })
  }

  // 恢复使用环境变量：服务端清密文后，把返回的密钥字段写回 draft+baseline
  // （其余字段的未保存草稿原样保留，dirty 状态不丢）。
  const onRestoreEnvKey = () => {
    if (!draft) return
    setActionError('')
    restoreMutation.mutate(draft.code, {
      onSuccess: (p) => {
        const keyPatch = {
          api_key: '',
          key_present: p.key_present,
          key_last4: p.key_last4,
          api_key_env: p.api_key_env,
        }
        const restore = (cur: ProviderConfig | null) => (cur ? { ...cur, ...keyPatch } : cur)
        setDraft(restore)
        commit((restore(draft) as ProviderConfig) ?? draft!)
      },
      onError: (e) => setActionError(e instanceof Error ? e.message : '恢复失败'),
    })
  }

  const saving = saveMutation.isPending || deleteMutation.isPending

  const onSave = () => {
    if (!draft) return
    setActionError('')
    saveMutation.mutate(
      { provider: draft, isNew },
      {
        // 行内编辑保存后保持展开（用户常接着测试连接）；baseline 对齐后未保存徽章消失。
        // 新建保存后清空搜索过滤：新行不可能匹配既有过滤词，不清会让刚保存的行
        // 连同编辑面板一起被过滤掉，看起来像「保存了但什么都没发生」。
        onSuccess: (saved) => {
          commit(saved)
          setIsNew(false)
          if (isNew) setQuery('')
        },
        onError: (e) => setActionError(e instanceof Error ? e.message : '保存失败'),
      },
    )
  }

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return providers
    return providers.filter(
      (p) =>
        providerDisplayName(p).toLowerCase().includes(q) ||
        p.code.toLowerCase().includes(q) ||
        p.default_model.toLowerCase().includes(q),
    )
  }, [providers, query])

  return (
    <section aria-label="模型提供商">
      {/* 列表卡：卡内头部（标题+说明 | 搜索+新建）与行同内边距节奏，border-b 分隔；
          点击行在下拉展开编辑面板（手风琴，同刻至多一行） */}
      <div className="overflow-hidden rounded-lg border border-border bg-surface">
        <div className="flex items-center justify-between gap-4 border-b border-border px-4 py-3">
          <div className="min-w-0">
            <h2 className="text-sm font-semibold text-text">模型提供商</h2>
            <p className="mt-0.5 text-xs text-muted">接入哪些 provider，连接参数与能力标志</p>
          </div>
          <div className="flex flex-shrink-0 items-center gap-2">
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-faint" />
              <input
                type="search"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="搜索 provider 或模型名"
                aria-label="搜索 provider"
                spellCheck={false}
                className="w-44 rounded-lg border border-border bg-surface-2 py-1.5 pl-8 pr-2.5 text-xs text-text outline-none placeholder:text-faint focus:border-accent"
              />
            </div>
            <button
              type="button"
              onClick={() => (isNew && draft ? requestCollapse() : startEdit(null))}
              className="flex items-center gap-1.5 rounded-lg bg-accent-soft px-3 py-1.5 text-xs font-semibold text-accent transition-colors hover:bg-accent/10 hover:text-accent-hover"
            >
              <Plus className="h-3.5 w-3.5" />
              新建
            </button>
          </div>
        </div>
        {isLoading ? (
          <div className="tac-cursor py-10 text-center font-mono text-sm text-muted">加载中</div>
        ) : isError ? (
          <div className="flex flex-col items-center gap-2 py-10 text-sm text-sev-critical">
            <span>⚠ {error instanceof Error ? error.message : '加载失败'}</span>
            <button
              type="button"
              onClick={() => void refetch()}
              className="rounded-md border border-border px-2.5 py-1 text-xs text-muted transition-colors hover:border-accent hover:text-accent"
            >
              重试
            </button>
          </div>
        ) : filtered.length === 0 && !isNew ? (
          <div className="py-10 text-center text-sm text-muted">
            {query.trim() ? '无匹配部署' : '暂无部署——点「新建」接入第一个 provider'}
          </div>
        ) : (
          <ul className="flex flex-col divide-y divide-border">
            {isNew && draft && (
              <li>
                <ProviderEditor
                  draft={draft}
                  isNew
                  dirty={dirty}
                  saving={saving}
                  saveDisabled={providerInvalid(draft, true)}
                  actionError={actionError}
                  pendingIntent={pending?.type ?? null}
                  onPatch={(p) => setDraft((d) => (d ? { ...d, ...p } : d))}
                  onSave={onSave}
                  onReset={() => {
                    resetDraft()
                    setActionError('')
                  }}
                  onRestoreEnvKey={onRestoreEnvKey}
                  onDiscard={discardPending}
                  onKeepEditing={() => setPending(null)}
                  onDismissError={() => setActionError('')}
                />
              </li>
            )}
            {filtered.map((p) =>
              // 展开中的行：行头保留（收起入口），编辑面板在行头下方下拉展开。
              draft && !isNew && draft.code === p.code ? (
                <li key={p.code} className="group/row">
                  <div className="flex items-stretch">
                    <RowHeader
                      p={p}
                      expanded
                      dirtySelected={dirty}
                      onToggle={requestCollapse}
                      className="flex-1"
                    />
                    <RowDeleteButton
                      code={p.code}
                      confirming={confirmingDeleteCode === p.code}
                      disabled={saving}
                      onArm={() => armRowDelete(p.code)}
                      onConfirm={() => onRowDelete(p.code)}
                    />
                  </div>
                  <ProviderEditor
                    draft={draft}
                    isNew={false}
                    dirty={dirty}
                    saving={saving}
                    saveDisabled={providerInvalid(draft, false)}
                    actionError={actionError}
                    pendingIntent={pending?.type ?? null}
                    onPatch={(patch) => setDraft((d) => (d ? { ...d, ...patch } : d))}
                    onSave={onSave}
                    onReset={() => {
                      resetDraft()
                      setActionError('')
                    }}
                    onRestoreEnvKey={onRestoreEnvKey}
                    onDiscard={discardPending}
                    onKeepEditing={() => setPending(null)}
                    onDismissError={() => setActionError('')}
                  />
                </li>
              ) : (
                <li key={p.code} className="group/row flex items-stretch">
                  <RowHeader
                    p={p}
                    expanded={false}
                    dirtySelected={false}
                    onToggle={() => requestSwitch(p)}
                    className="flex-1"
                  />
                  <RowDeleteButton
                    code={p.code}
                    confirming={confirmingDeleteCode === p.code}
                    disabled={saving}
                    onArm={() => armRowDelete(p.code)}
                    onConfirm={() => onRowDelete(p.code)}
                  />
                </li>
              ),
            )}
          </ul>
        )}
      </div>
    </section>
  )
}

// 行头：标识键 + 协议 + 默认模型 + 状态；展开态 chevron 转下（下拉展开的隐喻）。
function RowHeader({
  p,
  expanded,
  dirtySelected,
  onToggle,
  className,
}: {
  p: ProviderConfig
  expanded: boolean
  dirtySelected: boolean
  onToggle: () => void
  className?: string
}) {
  return (
    <button
      type="button"
      onClick={onToggle}
      aria-expanded={expanded}
      className={cn(
        'flex min-w-0 items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-surface-2',
        expanded && 'bg-surface-2/60',
        !p.enabled && 'opacity-55',
        className,
      )}
    >
      <span className="flex min-w-0 items-center gap-1.5">
        {!p.key_present && (
          <span
            aria-label="密钥未注入"
            title="密钥未注入，调用会失败"
            className="h-1.5 w-1.5 flex-shrink-0 rounded-full bg-sev-high"
          />
        )}
        <span className="min-w-0 truncate text-sm font-medium text-text">
          {providerDisplayName(p)}
        </span>
        {expanded && dirtySelected && (
          <span
            aria-label="有未保存更改"
            title="有未保存更改"
            className="h-1.5 w-1.5 flex-shrink-0 rounded-full bg-accent"
          />
        )}
      </span>
      <Badge variant="outline" className="flex-shrink-0">
        {TYPE_LABEL[p.type]}
      </Badge>
      <span className="max-w-36 flex-shrink truncate font-mono text-xs text-muted">
        {p.default_model}
      </span>
      <span className="min-w-0 flex-1 truncate font-mono text-xs text-faint">{p.base_url}</span>
      {!p.enabled && (
        <Badge variant="outline" className="flex-shrink-0">
          已停用
        </Badge>
      )}
      <ChevronRight
        className={cn(
          'h-3.5 w-3.5 flex-shrink-0 text-faint transition-transform',
          expanded && 'rotate-90',
        )}
      />
    </button>
  )
}

// 行级删除钮：垃圾桶 hover 淡显，首击变红「确认删除？」，再击才删；3 秒回退防误触。
function RowDeleteButton({
  code,
  confirming,
  disabled,
  onArm,
  onConfirm,
}: {
  code: string
  confirming: boolean
  disabled: boolean
  onArm: () => void
  onConfirm: () => void
}) {
  return (
    <button
      type="button"
      onClick={confirming ? onConfirm : onArm}
      disabled={disabled}
      aria-label={confirming ? `确认删除 ${code}` : `删除 provider ${code}`}
      title={confirming ? '再点一次确认删除' : `删除 ${code}`}
      className={cn(
        'flex flex-shrink-0 items-center justify-center border-l border-border px-3 text-faint transition-colors hover:bg-sev-critical/10 hover:text-sev-critical',
        confirming ? 'bg-sev-critical/10 text-sev-critical' : 'opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100',
        disabled && 'cursor-default opacity-40',
      )}
    >
      {confirming ? <span className="text-xs font-medium">确认删除？</span> : <Trash2 className="h-3.5 w-3.5" />}
    </button>
  )
}
