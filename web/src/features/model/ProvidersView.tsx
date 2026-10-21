import { useCallback, useEffect, useState } from 'react'
import { Boxes } from 'lucide-react'
import { listProviders, saveProvider, deleteProvider } from '@/api/models'
import type { ProviderConfig } from '@/api/types'
import { ProviderListPanel } from './ProviderListPanel'
import { ProviderDetailPanel } from './ProviderDetailPanel'
import { blankProvider, providerInvalid } from './providerForm'

// 部署视图：左列表 + 右常驻详情面板的主从分栏（非卡片网格+模态抽屉）。
// provider 部署量小、改动频繁——同屏对照列表与表单，选中/新建即在右侧原地展开，
// 不需要「点开覆盖列表→关闭」的抽屉往返（对齐本项目「对话」页 ConversationList+Detail 分栏）。
// 事实源在 DB，前端写即改运行期装配（写经 llmstore 失效广播，runner 下次 For(role) 读到最新）。
// 安全：API Key 仅在保存时明文提交一次，后端加密落库，GET 响应永不回传——
// 「密钥未注入」徽章据 key_present 提示。
// 视图切换 tab 已上移至 ModelPage 顶部，本视图只管部署列表 + 详情。
export function ProvidersView() {
  const [providers, setProviders] = useState<ProviderConfig[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [draft, setDraft] = useState<ProviderConfig | null>(null)
  const [isNew, setIsNew] = useState(false)
  const [saving, setSaving] = useState(false)

  const reload = useCallback(() => {
    setLoading(true)
    setError('')
    listProviders()
      .then((rows) => setProviders(rows))
      .catch((e) => setError(e instanceof Error ? e.message : '加载失败'))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => reload(), [reload])

  const patch = (p: Partial<ProviderConfig>) => setDraft((d) => (d ? { ...d, ...p } : d))
  const openNew = () => {
    setDraft(blankProvider())
    setIsNew(true)
  }
  const openEdit = (p: ProviderConfig) => {
    setDraft(p)
    setIsNew(false)
  }

  const onSave = async () => {
    if (!draft) return
    setSaving(true)
    try {
      const saved = await saveProvider(draft, isNew)
      setDraft(saved)
      setIsNew(false)
      reload()
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const onDelete = async () => {
    if (!draft || isNew || !window.confirm(`确认删除 provider「${draft.key}」？`)) return
    setSaving(true)
    try {
      await deleteProvider(draft.key)
      setDraft(null)
      reload()
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '删除失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="grid h-full min-h-0 grid-cols-[280px_1fr]">
      <ProviderListPanel
        providers={providers}
        loading={loading}
        error={error}
        selectedKey={isNew ? '__new__' : draft?.key ?? ''}
        onSelect={openEdit}
        onNew={openNew}
      />
      {draft ? (
        <ProviderDetailPanel
          key={isNew ? '__new__' : draft.key}
          draft={draft}
          isNew={isNew}
          saving={saving}
          saveDisabled={providerInvalid(draft, isNew)}
          onPatch={patch}
          onSave={() => void onSave()}
          onDelete={isNew ? undefined : () => void onDelete()}
        />
      ) : (
        <div className="flex h-full min-h-0 flex-col items-center justify-center gap-2 text-muted">
          <Boxes className="h-8 w-8 text-faint" />
          <p className="text-[13px]">选择左侧部署查看详情，或点「+」新建</p>
        </div>
      )}
    </div>
  )
}
