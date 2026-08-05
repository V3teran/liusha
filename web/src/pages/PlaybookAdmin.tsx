import { useCallback, useEffect, useState } from 'react'
import { ArrowDown, ArrowUp, X } from 'lucide-react'
import {
  listPlaybookConfigs,
  getPlaybookConfig,
  savePlaybook,
  deletePlaybook,
  listHunterConfigs,
} from '@/api/config'
import type { PlaybookConfig, HunterConfig } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { ConfigListShell, ConfigRow } from '@/features/config/ConfigListShell'
import { ConfigDrawer, Field, INPUT_CLASS } from '@/features/config/ConfigDrawer'

function blankPlaybook(): PlaybookConfig {
  return { id: '', code: '', name: '', description: '', enabled: true, hunters: [] }
}

// 剧本配置管理页：一个剧本 = 一组有序的领域智能体。编辑时拉全量智能体作候选，
// 已选按序展示、可上下移/移除；保存时按数组下标定 position。
export function PlaybookAdmin() {
  const [rows, setRows] = useState<PlaybookConfig[]>([])
  const [hunters, setHunters] = useState<HunterConfig[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [draft, setDraft] = useState<PlaybookConfig | null>(null)
  const [selected, setSelected] = useState<string[]>([]) // 有序 hunter id 列表
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [pbs, hs] = await Promise.all([listPlaybookConfigs(), listHunterConfigs()])
      setRows(pbs)
      setHunters(hs)
    } catch (e) {
      setError(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  // 只有 domain 猎手可进剧本组合池（orchestrator 全局唯一、swarm 自动注入，不组合）。
  const domainHunters = hunters.filter((h) => h.kind === 'domain')
  const hunterName = (id: string) => hunters.find((h) => h.id === id)?.name ?? id

  const openNew = () => {
    setDraft(blankPlaybook())
    setSelected([])
  }

  // 编辑：拉全量单条（带有序 hunters），铺进选择态。
  const openEdit = async (pb: PlaybookConfig) => {
    try {
      const full = await getPlaybookConfig(pb.id)
      setDraft(full)
      setSelected([...(full.hunters ?? [])].sort((a, b) => a.position - b.position).map((h) => h.hunter_id))
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '加载剧本失败')
    }
  }

  const patch = (p: Partial<PlaybookConfig>) => setDraft((d) => (d ? { ...d, ...p } : d))

  const addHunter = (id: string) => {
    if (id && !selected.includes(id)) setSelected((s) => [...s, id])
  }
  const removeHunter = (id: string) => setSelected((s) => s.filter((x) => x !== id))
  const move = (idx: number, dir: -1 | 1) => {
    setSelected((s) => {
      const next = [...s]
      const j = idx + dir
      if (j < 0 || j >= next.length) return s
      ;[next[idx], next[j]] = [next[j], next[idx]]
      return next
    })
  }

  const onSave = async () => {
    if (!draft) return
    setSaving(true)
    try {
      await savePlaybook(draft, selected)
      setDraft(null)
      await load()
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const onDelete = async () => {
    if (!draft?.id || !window.confirm(`确认删除剧本「${draft.name}」？`)) return
    setSaving(true)
    try {
      await deletePlaybook(draft.id)
      setDraft(null)
      await load()
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '删除失败')
    } finally {
      setSaving(false)
    }
  }

  const saveDisabled = !draft?.code || !draft?.name
  const available = domainHunters.filter((h) => !selected.includes(h.id))

  return (
    <>
      <ConfigListShell
        title="剧本"
        subtitle="一组有序的领域智能体，场景绑定剧本决定派发哪些智能体"
        loading={loading}
        error={error}
        empty={rows.length === 0}
        emptyHint="暂无剧本——点右上「新建」创建第一个"
        onNew={openNew}
      >
        {rows.map((pb) => (
          <ConfigRow
            key={pb.id}
            name={pb.name}
            description={pb.description}
            dimmed={!pb.enabled}
            onClick={() => void openEdit(pb)}
            right={!pb.enabled ? <Badge variant="outline">已停用</Badge> : undefined}
          />
        ))}
      </ConfigListShell>

      <ConfigDrawer
        open={!!draft}
        title={draft?.id ? '编辑剧本' : '新建剧本'}
        onOpenChange={(o) => !o && setDraft(null)}
        onSave={() => void onSave()}
        onDelete={draft?.id ? () => void onDelete() : undefined}
        saving={saving}
        saveDisabled={saveDisabled}
      >
        {draft && (
          <>
            <Field label="标识符" hint="ID">
              <input
                className={INPUT_CLASS}
                value={draft.code}
                spellCheck={false}
                disabled={!!draft?.id}
                onChange={(e) => patch({ code: e.target.value })}
              />
            </Field>
            <Field label="名称">
              <input className={INPUT_CLASS} value={draft.name} onChange={(e) => patch({ name: e.target.value })} />
            </Field>
            <Field label="描述">
              <input
                className={INPUT_CLASS}
                value={draft.description}
                onChange={(e) => patch({ description: e.target.value })}
              />
            </Field>

            <Field label="智能体序列" hint="按顺序执行，可上下移">
              <div className="flex flex-col gap-1.5">
                {selected.length === 0 && (
                  <p className="rounded-md border border-dashed border-border px-3 py-2 text-[12.5px] text-muted">
                    尚未添加智能体，从下方选择
                  </p>
                )}
                {selected.map((id, idx) => (
                  <div
                    key={id}
                    className="flex items-center gap-2 rounded-md border border-border bg-surface-2 px-2.5 py-1.5"
                  >
                    <span className="w-5 flex-shrink-0 text-center font-mono text-[11px] text-faint">{idx + 1}</span>
                    <span className="min-w-0 flex-1 truncate text-[13px] text-text">{hunterName(id)}</span>
                    <button
                      type="button"
                      onClick={() => move(idx, -1)}
                      disabled={idx === 0}
                      aria-label="上移"
                      className="text-muted hover:text-text disabled:opacity-30"
                    >
                      <ArrowUp className="h-3.5 w-3.5" />
                    </button>
                    <button
                      type="button"
                      onClick={() => move(idx, 1)}
                      disabled={idx === selected.length - 1}
                      aria-label="下移"
                      className="text-muted hover:text-text disabled:opacity-30"
                    >
                      <ArrowDown className="h-3.5 w-3.5" />
                    </button>
                    <button
                      type="button"
                      onClick={() => removeHunter(id)}
                      aria-label="移除"
                      className="text-muted hover:text-sev-critical"
                    >
                      <X className="h-3.5 w-3.5" />
                    </button>
                  </div>
                ))}
                <select
                  className={INPUT_CLASS}
                  value=""
                  onChange={(e) => addHunter(e.target.value)}
                  disabled={available.length === 0}
                >
                  <option value="" disabled>
                    {available.length === 0 ? '无可添加的领域智能体' : '添加领域智能体…'}
                  </option>
                  {available.map((h) => (
                    <option key={h.id} value={h.id}>
                      {h.name}
                    </option>
                  ))}
                </select>
              </div>
            </Field>

            <label className="flex items-center gap-2 text-[13px] text-text">
              <input
                type="checkbox"
                checked={draft.enabled}
                onChange={(e) => patch({ enabled: e.target.checked })}
              />
              启用
            </label>
          </>
        )}
      </ConfigDrawer>
    </>
  )
}
