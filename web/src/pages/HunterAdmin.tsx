import { useCallback, useEffect, useState } from 'react'
import { listHunterConfigs, saveHunter, deleteHunter } from '@/api/config'
import type { HunterConfig, HunterKind } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { ConfigListShell, ConfigRow } from '@/features/config/ConfigListShell'
import { ConfigDrawer, Field, INPUT_CLASS } from '@/features/config/ConfigDrawer'

const DEFAULT_MAX_ITERATIONS = 20

// 新建猎手空白初值。kind 默认 domain（可被剧本组合的领域猎手）。
function blankHunter(): HunterConfig {
  return {
    id: '',
    code: '',
    kind: 'domain',
    name: '',
    description: '',
    body: '',
    tools: [],
    max_iterations: DEFAULT_MAX_ITERATIONS,
    enabled: true,
  }
}

// tools 数组 ↔ 每行一个的文本（编辑体验：一行一个工具 code，空行忽略）。
function toolsToText(tools: string[]): string {
  return tools.join('\n')
}
function textToTools(text: string): string[] {
  return text
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean)
}

// 智能体配置管理页（后端资源仍名 hunter）：领域智能体系统提示词 + 工具集 + 调度摘要，全字段编辑。
export function HunterAdmin() {
  const [rows, setRows] = useState<HunterConfig[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [draft, setDraft] = useState<HunterConfig | null>(null)
  const [toolsText, setToolsText] = useState('')
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      setRows(await listHunterConfigs())
    } catch (e) {
      setError(e instanceof Error ? e.message : '加载失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const openDraft = (h: HunterConfig) => {
    setDraft(h)
    setToolsText(toolsToText(h.tools))
  }

  const patch = (p: Partial<HunterConfig>) => setDraft((d) => (d ? { ...d, ...p } : d))

  const onSave = async () => {
    if (!draft) return
    setSaving(true)
    try {
      await saveHunter({ ...draft, tools: textToTools(toolsText) })
      setDraft(null)
      await load()
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const onDelete = async () => {
    if (!draft?.id || !window.confirm(`确认删除智能体「${draft.name}」？`)) return
    setSaving(true)
    try {
      await deleteHunter(draft.id)
      setDraft(null)
      await load()
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '删除失败')
    } finally {
      setSaving(false)
    }
  }

  const saveDisabled = !draft?.code || !draft?.name

  return (
    <>
      <ConfigListShell
        title="智能体"
        subtitle="领域智能体的系统提示词、工具集与调度摘要，剧本据此编排"
        loading={loading}
        error={error}
        empty={rows.length === 0}
        emptyHint="暂无智能体——点右上「新建」创建第一个"
        onNew={() => openDraft(blankHunter())}
      >
        {rows.map((h) => (
          <ConfigRow
            key={h.id}
            name={h.name}
            description={h.description}
            dimmed={!h.enabled}
            onClick={() => openDraft(h)}
            right={
              <div className="flex flex-shrink-0 items-center gap-2">
                <Badge variant="outline">{h.kind === 'orchestrator' ? '编排' : '领域'}</Badge>
                {!h.enabled && <Badge variant="outline">已停用</Badge>}
              </div>
            }
          />
        ))}
      </ConfigListShell>

      <ConfigDrawer
        open={!!draft}
        title={draft?.id ? '编辑智能体' : '新建智能体'}
        onOpenChange={(o) => !o && setDraft(null)}
        onSave={() => void onSave()}
        onDelete={draft?.id ? () => void onDelete() : undefined}
        saving={saving}
        saveDisabled={saveDisabled}
      >
        {draft && (
          <>
            <div className="flex gap-3">
              <Field label="标识符" hint="ID">
                <input
                  className={INPUT_CLASS}
                  value={draft.code}
                  spellCheck={false}
                  disabled={!!draft?.id}
                  onChange={(e) => patch({ code: e.target.value })}
                />
              </Field>
              <Field label="类型">
                <select
                  className={INPUT_CLASS}
                  value={draft.kind}
                  onChange={(e) => patch({ kind: e.target.value as HunterKind })}
                >
                  <option value="domain">领域智能体</option>
                  <option value="orchestrator">编排智能体</option>
                </select>
              </Field>
            </div>
            <Field label="名称">
              <input className={INPUT_CLASS} value={draft.name} onChange={(e) => patch({ name: e.target.value })} />
            </Field>
            <Field label="调度摘要" hint="多智能体协同时编排者据此选派（非面向用户的描述）">
              <input
                className={INPUT_CLASS}
                value={draft.description}
                onChange={(e) => patch({ description: e.target.value })}
              />
            </Field>
            <Field label="系统提示词" hint="该智能体运行时的 system prompt">
              <textarea
                className={INPUT_CLASS + ' min-h-40 resize-y font-mono'}
                value={draft.body}
                onChange={(e) => patch({ body: e.target.value })}
              />
            </Field>
            <Field label="工具集" hint="内部函数工具标识符，每行一个">
              <textarea
                className={INPUT_CLASS + ' min-h-24 resize-y font-mono'}
                value={toolsText}
                spellCheck={false}
                onChange={(e) => setToolsText(e.target.value)}
              />
            </Field>
            <Field label="最大迭代轮数" hint="ReAct 推理循环上限">
              <input
                type="number"
                min={1}
                className={INPUT_CLASS}
                value={draft.max_iterations}
                onChange={(e) => patch({ max_iterations: Number(e.target.value) })}
              />
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
