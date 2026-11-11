import { SearchInput } from '@/components/SearchInput'
import { errorText } from '@/api/client'
import { useEffect, useEffectEvent, useMemo, useRef, useState } from 'react'
import { useDirtyDraft } from '@/hooks/useDirtyDraft'
import { Plus } from 'lucide-react'
import type { ProviderConfig } from '@/api/types'
import { blankProvider, providerInvalid } from './providerRules'
import { ProviderEditor } from './ProviderEditor'
import { RowHeader, RowDeleteButton } from './ProviderRowParts'
import {
  useProviders,
  useSaveProvider,
  useDeleteProvider,
  useRestoreProviderEnvKey,
} from './useModelQueries'
import { Button } from '@/components/ui/Button'

/** 自带点击行为的控件：在列表外按下它们不算「点别处」——切换行、新建、搜索、导航等各有其行为。 */
const INTERACTIVE =
  'a, button, input, select, textarea, label, summary, [contenteditable="true"], ' +
  '[role="button"], [role="option"], [role="combobox"], [role="switch"], [role="radio"], [role="tab"], [role="menuitem"]'

/** 按下点落在元素自身的滚动条上（拖动滚动，不是「点别处」）：越出内容区（clientWidth/Height
 *  不含滚动条）。内联元素的 client 尺寸为 0，不参与判定。 */
function onScrollbar(e: PointerEvent, el: Element): boolean {
  return (el.clientWidth > 0 && e.offsetX > el.clientWidth) || (el.clientHeight > 0 && e.offsetY > el.clientHeight)
}

// 模型提供商分区：设置页内的实体管理。列表 = 表格行；点击行在行下方**下拉展开**编辑表单
// （手风琴式，无弹窗），再点行收起。同一时刻至多展开一行。
// 数据经 useModelQueries；保存/删除 invalidate 后列表自动刷新，与能力路由共享 providers 缓存。
// 草稿模型：展开即拍 baseline，dirty 驱动保存可用/重置/收起与切换确认（不静默丢弃草稿）；
// 保存成功即收起回到列表。
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

  // 点别处收起展开中的编辑面板——与点行头、取消同一口径：有未保存更改时先出确认条，
  // 不静默丢弃草稿。「别处」= 编辑面板的内容之外、且不是自带行为的控件：页面空白、卡片
  // 标题区、面板自身的空白布局层（data-editor-surface）都算。只认主键按下；按在滚动条上
  // （拖动滚动）不算。
  const panelRef = useRef<HTMLLIElement>(null)
  const onOutsidePointer = useEffectEvent((e: PointerEvent) => {
    const target = e.target
    if (e.button !== 0 || !(target instanceof Element) || onScrollbar(e, target)) return
    if (!target.hasAttribute('data-editor-surface')) {
      if (panelRef.current?.contains(target)) return // 面板内容
      if (target.closest(INTERACTIVE)) return
    }
    requestCollapse()
  })
  // Esc 同取消（焦点在哪都生效）；控件自己消费了 Esc（如收起下拉）时不处理。
  const onEscape = useEffectEvent((e: KeyboardEvent) => {
    if (e.key === 'Escape' && !e.defaultPrevented) requestCollapse()
  })
  const editing = draft !== null
  useEffect(() => {
    if (!editing) return
    document.addEventListener('pointerdown', onOutsidePointer)
    document.addEventListener('keydown', onEscape)
    return () => {
      document.removeEventListener('pointerdown', onOutsidePointer)
      document.removeEventListener('keydown', onEscape)
    }
  }, [editing])

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
        setActionError(errorText(e, '删除失败'))
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
      onError: (e) => setActionError(errorText(e, '恢复失败')),
    })
  }

  const saving = saveMutation.isPending || deleteMutation.isPending

  const onSave = () => {
    if (!draft) return
    setActionError('')
    saveMutation.mutate(
      { provider: draft, isNew },
      {
        // 保存即完成编辑：收起面板回到列表，行头由 invalidate 后的重拉刷新。
        // 新建保存后清空搜索过滤：新行不可能匹配既有过滤词，不清会让刚保存的行被过滤掉，
        // 看起来像「保存了但什么都没发生」。
        onSuccess: () => {
          collapse()
          if (isNew) setQuery('')
        },
        onError: (e) => setActionError(errorText(e, '保存失败')),
      },
    )
  }

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return providers
    return providers.filter(
      (p) => p.display_name.toLowerCase().includes(q) || p.default_model.toLowerCase().includes(q),
    )
  }, [providers, query])

  return (
    <section aria-label="模型提供商">
      {/* 列表卡：卡内头部（标题+说明 | 搜索+新建）与行同内边距节奏，border-b 分隔；
          点击行在下拉展开编辑面板（手风琴，同刻至多一行） */}
      <div className="overflow-hidden rounded-xl border border-border bg-surface shadow-sm">
        <div className="flex flex-col gap-3 border-b border-border px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
          <div className="min-w-0">
            <h2 className="text-sm font-semibold text-text">模型提供商</h2>
            <p className="mt-0.5 text-xs text-text-secondary">接入哪些 provider，连接参数与能力标志</p>
          </div>
          <div className="flex items-center gap-2 sm:flex-shrink-0">
            <SearchInput
              className="min-w-0 flex-1 sm:w-72 sm:flex-none"
              value={query}
              onChange={setQuery}
              placeholder="搜索 provider 或模型名"
              ariaLabel="搜索 provider"
            />
            <Button size="sm" onClick={() => (isNew && draft ? requestCollapse() : startEdit(null))}>
              <Plus />
              新建
            </Button>
          </div>
        </div>
        {isLoading ? (
          <div className="terminal-cursor py-10 text-center font-mono text-sm text-text-secondary">加载中</div>
        ) : isError ? (
          <div className="flex flex-col items-center gap-2 py-10 text-sm text-sev-critical">
            <span>⚠ {error instanceof Error ? error.message : '加载失败'}</span>
            <Button variant="secondary" size="xs" onClick={() => void refetch()}>
              重试
            </Button>
          </div>
        ) : filtered.length === 0 && !isNew ? (
          <div className="py-10 text-center text-sm text-text-secondary">
            {query.trim() ? '无匹配部署' : '暂无部署——点「新建」接入第一个 provider'}
          </div>
        ) : (
          <ul className="flex flex-col divide-y divide-border">
            {isNew && draft && (
              <li ref={panelRef}>
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
                  onCancel={requestCollapse}
                  onDiscard={discardPending}
                  onKeepEditing={() => setPending(null)}
                  onDismissError={() => setActionError('')}
                />
              </li>
            )}
            {filtered.map((p) =>
              // 展开中的行：行头保留（收起入口），编辑面板在行头下方下拉展开。
              draft && !isNew && draft.code === p.code ? (
                <li key={p.code} ref={panelRef} className="group/row">
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
                    onCancel={requestCollapse}
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

