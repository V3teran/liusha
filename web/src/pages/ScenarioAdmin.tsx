import { useCallback, useEffect, useState } from 'react'
import {
  listScenarioConfigs,
  saveScenario,
  deleteScenario,
  listPlaybookConfigs,
} from '@/api/config'
import type { ScenarioConfig, PlaybookConfig, ScenarioEngine } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { ConfigListShell, ConfigRow } from '@/features/config/ConfigListShell'
import { ConfigDrawer, Field, INPUT_CLASS } from '@/features/config/ConfigDrawer'

// 新建场景的空白初值。engine 默认 swarm，enabled 默认 true。
function blankScenario(): ScenarioConfig {
  return {
    id: '',
    code: '',
    name: '',
    description: '',
    instruction: '',
    domain: '',
    engine: 'swarm',
    playbook_id: '',
    enabled: true,
  }
}

// 场景配置管理页：全字段编辑（含 disabled）。场景 = 引用一个剧本 + 选 engine + 交战域。
export function ScenarioAdmin() {
  const [rows, setRows] = useState<ScenarioConfig[]>([])
  const [playbooks, setPlaybooks] = useState<PlaybookConfig[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [draft, setDraft] = useState<ScenarioConfig | null>(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [scs, pbs] = await Promise.all([listScenarioConfigs(), listPlaybookConfigs()])
      setRows(scs)
      setPlaybooks(pbs)
    } catch (e) {
      setError(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const patch = (p: Partial<ScenarioConfig>) => setDraft((d) => (d ? { ...d, ...p } : d))

  const onSave = async () => {
    if (!draft) return
    setSaving(true)
    try {
      await saveScenario(draft)
      setDraft(null)
      await load()
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const onDelete = async () => {
    if (!draft?.id || !window.confirm(`确认删除场景「${draft.name}」？`)) return
    setSaving(true)
    try {
      await deleteScenario(draft.id)
      setDraft(null)
      await load()
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '删除失败')
    } finally {
      setSaving(false)
    }
  }

  const playbookName = (id: string) => playbooks.find((p) => p.id === id)?.name ?? id
  const saveDisabled = !draft?.code || !draft?.name || !draft?.playbook_id

  return (
    <>
      <ConfigListShell
        title="场景"
        subtitle="引用一个剧本 + 选引擎（solo/swarm）+ 交战域，是运行期派发的入口配置"
        loading={loading}
        error={error}
        empty={rows.length === 0}
        emptyHint="暂无场景——点右上「新建」创建第一个"
        onNew={() => setDraft(blankScenario())}
      >
        {rows.map((sc) => (
          <ConfigRow
            key={sc.id}
            code={sc.code}
            name={sc.name}
            dimmed={!sc.enabled}
            onClick={() => setDraft(sc)}
            right={
              <div className="flex flex-shrink-0 items-center gap-2">
                <Badge variant="outline">{sc.engine}</Badge>
                {!sc.enabled && <Badge variant="outline">已停用</Badge>}
              </div>
            }
          />
        ))}
      </ConfigListShell>

      <ConfigDrawer
        open={!!draft}
        title={draft?.id ? '编辑场景' : '新建场景'}
        onOpenChange={(o) => !o && setDraft(null)}
        onSave={() => void onSave()}
        onDelete={draft?.id ? () => void onDelete() : undefined}
        saving={saving}
        saveDisabled={saveDisabled}
      >
        {draft && (
          <>
            <Field label="Code" hint="业务主键，创建后作 task.scenario_id 存值">
              <input
                className={INPUT_CLASS}
                value={draft.code}
                spellCheck={false}
                onChange={(e) => patch({ code: e.target.value })}
              />
            </Field>
            <Field label="名称">
              <input className={INPUT_CLASS} value={draft.name} onChange={(e) => patch({ name: e.target.value })} />
            </Field>
            <Field label="简介">
              <input
                className={INPUT_CLASS}
                value={draft.description}
                onChange={(e) => patch({ description: e.target.value })}
              />
            </Field>
            <Field label="剧本" hint="该场景使用的猎手组合">
              <select
                className={INPUT_CLASS}
                value={draft.playbook_id}
                onChange={(e) => patch({ playbook_id: e.target.value })}
              >
                <option value="" disabled>
                  选择剧本
                </option>
                {playbooks.map((p) => (
                  <option key={p.id} value={p.id}>
                    {playbookName(p.id)}
                  </option>
                ))}
              </select>
            </Field>
            <div className="flex gap-3">
              <Field label="引擎">
                <select
                  className={INPUT_CLASS}
                  value={draft.engine}
                  onChange={(e) => patch({ engine: e.target.value as ScenarioEngine })}
                >
                  <option value="solo">solo</option>
                  <option value="swarm">swarm</option>
                </select>
              </Field>
              <Field label="交战域" hint="web/ctf/cloud…">
                <input
                  className={INPUT_CLASS}
                  value={draft.domain}
                  spellCheck={false}
                  onChange={(e) => patch({ domain: e.target.value })}
                />
              </Field>
            </div>
            <Field label="领域指令" hint="注入 AI 的场景侧重（system 指令）">
              <textarea
                className={INPUT_CLASS + ' min-h-32 resize-y font-mono'}
                value={draft.instruction}
                onChange={(e) => patch({ instruction: e.target.value })}
              />
            </Field>
            <label className="flex items-center gap-2 text-[13px] text-text">
              <input
                type="checkbox"
                checked={draft.enabled}
                onChange={(e) => patch({ enabled: e.target.checked })}
              />
              启用（停用后 picker 与运行期都拿不到此场景）
            </label>
          </>
        )}
      </ConfigDrawer>
    </>
  )
}
