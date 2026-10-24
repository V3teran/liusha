import { useMemo, useState } from 'react'
import { Boxes } from 'lucide-react'
import type { ProviderConfig } from '@/api/types'
import { ProviderListPanel } from './ProviderListPanel'
import { ProviderDetailPanel } from './ProviderDetailPanel'
import { blankProvider, providerInvalid } from './providerForm'
import { useProviders, useSaveProvider, useDeleteProvider } from './useModelQueries'

// 部署视图：左列表 + 右常驻详情面板的主从分栏。provider 部署量小、改动频繁——
// 同屏对照列表与表单，选中/新建即在右侧原地展开（对齐「对话」页分栏范式）。
// 数据经 useModelQueries（React Query）：保存/删除 invalidate 后列表自动刷新，
// 与「能力分档」tab 共享 providers 缓存。
// 草稿模型：打开即拍 baseline 快照，draft 与快照比对得 dirty——
// dirty 是保存可用、切换守卫、重置、列表未存点四处的唯一事实源；
// 保存成功后用服务端返回值同步刷新 draft + baseline（key_present 等只读字段归位）。
export function ProvidersView() {
  const { data: providers = [], isLoading, isError, error, refetch } = useProviders()
  const saveMutation = useSaveProvider()
  const deleteMutation = useDeleteProvider()

  const [draft, setDraft] = useState<ProviderConfig | null>(null)
  const [baseline, setBaseline] = useState<ProviderConfig | null>(null)
  const [isNew, setIsNew] = useState(false)
  // dirty 时被拦截的切换目标：确认条上「丢弃并切换」用它完成被拦下的动作。
  const [pendingSwitch, setPendingSwitch] = useState<{
    p: ProviderConfig | null
    asNew: boolean
  } | null>(null)
  // 保存/删除失败的页面内回显（替代 window.alert），下次操作即清。
  const [actionError, setActionError] = useState('')

  const dirty = useMemo(
    () => !!draft && !!baseline && JSON.stringify(draft) !== JSON.stringify(baseline),
    [draft, baseline],
  )

  const patch = (p: Partial<ProviderConfig>) =>
    setDraft((d) => (d ? { ...d, ...p } : d))

  // 打开目标（点选 asNew=false / 新建 asNew=true）。dirty 时先拦下目标弹确认条，不静默丢弃。
  const tryOpen = (p: ProviderConfig | null, asNew: boolean) => {
    if (dirty) {
      setPendingSwitch({ p, asNew })
      return
    }
    setDraft(p)
    setBaseline(p)
    setIsNew(asNew)
    setActionError('')
  }

  const discardAndSwitch = () => {
    if (!pendingSwitch) return
    const { p, asNew } = pendingSwitch
    setDraft(p)
    setBaseline(p)
    setIsNew(asNew)
    setPendingSwitch(null)
    setActionError('')
  }

  const resetDraft = () => {
    setDraft(baseline)
    setActionError('')
  }

  const saving = saveMutation.isPending || deleteMutation.isPending

  const onSave = () => {
    if (!draft) return
    setActionError('')
    saveMutation.mutate(
      { provider: draft, isNew },
      {
        onSuccess: (saved) => {
          setDraft(saved)
          setBaseline(saved)
          setIsNew(false)
        },
        onError: (e) => setActionError(e instanceof Error ? e.message : '保存失败'),
      },
    )
  }

  const onDelete = () => {
    if (!draft || isNew) return
    setActionError('')
    deleteMutation.mutate(draft.code, {
      onSuccess: () => {
        setDraft(null)
        setBaseline(null)
      },
      onError: (e) => setActionError(e instanceof Error ? e.message : '删除失败'),
    })
  }

  return (
    <div className="grid h-full min-h-0 grid-cols-[300px_1fr]">
      <ProviderListPanel
        providers={providers}
        loading={isLoading}
        error={isError ? (error instanceof Error ? error.message : '加载失败') : ''}
        selectedKey={isNew ? '__new__' : draft?.code ?? ''}
        dirtySelected={dirty}
        onSelect={(p) => tryOpen(p, false)}
        onNew={() => tryOpen(blankProvider(), true)}
        onRetry={refetch}
      />
      {draft ? (
        <ProviderDetailPanel
          key={isNew ? '__new__' : draft.code}
          draft={draft}
          isNew={isNew}
          dirty={dirty}
          saving={saving}
          saveDisabled={providerInvalid(draft, isNew)}
          actionError={actionError}
          pendingSwitchLabel={pendingSwitch ? pendingSwitch.p?.code ?? '新部署' : null}
          onPatch={patch}
          onSave={onSave}
          onReset={resetDraft}
          onDelete={onDelete}
          onDiscardAndSwitch={discardAndSwitch}
          onKeepEditing={() => setPendingSwitch(null)}
          onDismissError={() => setActionError('')}
        />
      ) : (
        <div className="flex h-full min-h-0 flex-col items-center justify-center gap-2 text-muted">
          <Boxes className="h-8 w-8 text-faint" />
          <p className="text-sm">选择左侧部署查看详情，或点「+」新建</p>
        </div>
      )}
    </div>
  )
}
