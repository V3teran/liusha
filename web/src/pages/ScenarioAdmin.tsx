import { useCallback, useEffect, useState } from 'react'
import { listScenarios } from '@/api/client'
import { saveScenario, deleteScenario, listHunterConfigs } from '@/api/config'
import type { ScenarioConfig, HunterConfig, ScenarioEngine } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { ConfigListShell, ConfigRow } from '@/features/config/ConfigListShell'
import { ConfigDrawer, Field, INPUT_CLASS } from '@/features/config/ConfigDrawer'

// 执行模式展示标签（后端枚举值不变，仅前端呈现）。
const ENGINE_LABEL: Record<ScenarioEngine, string> = {
  solo: '单智能体',
  swarm: '多智能体协同',
}

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
    solo_hunter_id: '',
    enabled: true,
  }
}

// 场景配置管理页：全字段编辑（含 disabled）。
// solo 场景单点指定一个领域智能体执行；swarm 场景无需指定——运行期自动纳入全部启用领域智能体。
export function ScenarioAdmin() {
  const [rows, setRows] = useState<ScenarioConfig[]>([])
  const [hunters, setHunters] = useState<HunterConfig[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [draft, setDraft] = useState<ScenarioConfig | null>(null)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const [scs, hs] = await Promise.all([listScenarios(), listHunterConfigs()])
      setRows(scs)
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

  const patch = (p: Partial<ScenarioConfig>) => setDraft((d) => (d ? { ...d, ...p } : d))

  // 切引擎时清理互斥字段：切到 swarm 清掉 solo_hunter_id（后端会拒带值的 swarm）。
  const onEngineChange = (engine: ScenarioEngine) =>
    patch(engine === 'swarm' ? { engine, solo_hunter_id: '' } : { engine })

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

  // solo 引擎的候选仅领域智能体（orchestrator 不可单点执行）。
  const domainHunters = hunters.filter((h) => h.kind === 'domain')
  // solo 必须选中 solo_hunter_id；swarm 不校验（后端拒带值）。
  const saveDisabled =
    !draft?.code || !draft?.name || (draft?.engine === 'solo' && !draft?.solo_hunter_id)

  return (
    <>
      <ConfigListShell
        title="场景"
        subtitle="选执行模式与领域，是运行期派发的入口配置"
        loading={loading}
        error={error}
        empty={rows.length === 0}
        emptyHint="暂无场景——点右上「新建」创建第一个"
        onNew={() => setDraft(blankScenario())}
      >
        {rows.map((sc) => (
          <ConfigRow
            key={sc.id}
            name={sc.name}
            description={sc.description}
            dimmed={!sc.enabled}
            onClick={() => setDraft(sc)}
            right={
              <div className="flex flex-shrink-0 items-center gap-2">
                <Badge variant="outline">{ENGINE_LABEL[sc.engine]}</Badge>
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
            <Field label="标识符" hint={draft?.id ? 'ID' : 'ID，创建后作 task.scenario_id 存值'}>
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
            <div className="flex gap-3">
              <Field label="执行模式" hint="单体或多智能体协同">
                <select
                  className={INPUT_CLASS}
                  value={draft.engine}
                  onChange={(e) => onEngineChange(e.target.value as ScenarioEngine)}
                >
                  <option value="solo">单智能体</option>
                  <option value="swarm">多智能体协同</option>
                </select>
              </Field>
              <Field label="领域" hint="web / ctf / cloud…">
                <input
                  className={INPUT_CLASS}
                  value={draft.domain}
                  spellCheck={false}
                  onChange={(e) => patch({ domain: e.target.value })}
                />
              </Field>
            </div>
            {draft.engine === 'solo' ? (
              <Field label="执行智能体" hint="单智能体模式下唯一执行的领域智能体">
                <select
                  className={INPUT_CLASS}
                  value={draft.solo_hunter_id}
                  onChange={(e) => patch({ solo_hunter_id: e.target.value })}
                >
                  <option value="" disabled>
                    选择智能体
                  </option>
                  {domainHunters.map((h) => (
                    <option key={h.id} value={h.id}>
                      {h.name}
                    </option>
                  ))}
                </select>
              </Field>
            ) : (
              <Field label="执行智能体" hint="多智能体协同模式无需指定">
                <p className="rounded-md border border-border bg-background px-3 py-2 text-[13px] text-muted">
                  运行期自动纳入全部启用的领域智能体，由编排者动态派活。
                </p>
              </Field>
            )}
            <Field label="场景系统提示" hint="注入模型的场景侧重（system prompt）">
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
              启用（停用后选择器与运行期都拿不到此场景）
            </label>
          </>
        )}
      </ConfigDrawer>
    </>
  )
}
