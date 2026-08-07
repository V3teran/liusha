import { useCallback, useEffect, useState } from 'react'
import { listProviders, saveProvider, deleteProvider } from '@/api/models'
import type { ProviderConfig } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { ConfigListShell, ConfigRow } from '@/features/config/ConfigListShell'
import { ConfigDrawer } from '@/features/config/ConfigDrawer'
import { TYPE_LABEL, blankProvider, providerInvalid, providerTabs } from './providerForm'

// 部署视图：provider 连接参数 + 能力标志的全字段 CRUD（后端 llm_provider）。
// 事实源在 DB，前端写即改运行期装配（写经 llmstore 失效广播，runner 下次 For(role) 读到最新）。
// 安全：api_key_env 只填**环境变量名**，密钥值永不经前端——「密钥未注入」徽章据 key_present 提示。
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
      await saveProvider(draft, isNew)
      setDraft(null)
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
    <>
      <ConfigListShell
        title="模型部署"
        subtitle="provider 连接参数与能力标志；角色指派据此解析到具体部署"
        loading={loading}
        error={error}
        empty={providers.length === 0}
        emptyHint="暂无部署——点右上「新建」接入第一个 provider"
        onNew={openNew}
      >
        {providers.map((p) => (
          <ConfigRow
            key={p.key}
            name={p.key}
            description={`${p.default_model} · ${p.base_url}`}
            dimmed={!p.enabled}
            onClick={() => openEdit(p)}
            right={
              <div className="flex flex-shrink-0 items-center gap-2">
                <Badge variant="outline">{TYPE_LABEL[p.type]}</Badge>
                {!p.key_present && (
                  <Badge variant="outline" className="border-sev-high/50 text-sev-high">
                    密钥未注入
                  </Badge>
                )}
                {!p.enabled && <Badge variant="outline">已停用</Badge>}
              </div>
            }
          />
        ))}
      </ConfigListShell>

      <ConfigDrawer
        open={!!draft}
        title={isNew ? '接入 provider' : `编辑 ${draft?.key ?? ''}`}
        onOpenChange={(o) => !o && setDraft(null)}
        onSave={() => void onSave()}
        onDelete={isNew ? undefined : () => void onDelete()}
        saving={saving}
        saveDisabled={providerInvalid(draft)}
        tabs={draft ? providerTabs(draft, patch, isNew) : []}
      />
    </>
  )
}
