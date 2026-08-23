import { useEffect, useMemo, useState } from 'react'
import { listHuntersPaged } from '@/api/client'
import { saveHunter, deleteHunter, listToolCandidates } from '@/api/config'
import type { HunterConfig, HunterKind, Tool } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { ConfigListShell, ConfigRow } from '@/features/config/ConfigListShell'
import { usePagedList } from '@/features/config/usePagedList'
import { ConfigDrawer, Field, INPUT_CLASS } from '@/features/config/ConfigDrawer'

const DEFAULT_MAX_ITERATIONS = 20

// 智能体分页取数：适配 usePagedList 的 (page,size,q) → {items,total} 契约。
const fetchHunters = async (page: number, size: number, q: string) => {
  const res = await listHuntersPaged(page, size, q)
  return { items: res.hunters, total: res.total }
}

// 新建猎手空白初值。kind 默认 domain（领域智能体）。
function blankHunter(): HunterConfig {
  return {
    id: '',
    code: '',
    kind: 'domain',
    name: '',
    description: '',
    body: '',
    function_tools: [],
    cli_tools: [],
    max_iterations: DEFAULT_MAX_ITERATIONS,
    enabled: true,
  }
}

// 智能体配置管理页（后端资源仍名 hunter）：领域智能体系统提示词 + 工具集 + 调度摘要，全字段编辑。
// 工具集从工具目录（DB）读取候选，勾选而非填空——function_tools（内部函数工具）与 cli_tools（外部 CLI 工具）分区多选。
export function HunterAdmin() {
  const list = usePagedList<HunterConfig>(fetchHunters)
  const { rows, loading, error, reload } = list
  // 工具候选：函数工具 / CLI 工具两套全量目录，一次性拉取供勾选。
  const [functionTools, setFunctionTools] = useState<Tool[]>([])
  const [cliTools, setCliTools] = useState<Tool[]>([])
  const [draft, setDraft] = useState<HunterConfig | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    void listToolCandidates('function')
      .then(setFunctionTools)
      .catch(() => setFunctionTools([]))
    void listToolCandidates('cli')
      .then(setCliTools)
      .catch(() => setCliTools([]))
  }, [])

  const patch = (p: Partial<HunterConfig>) => setDraft((d) => (d ? { ...d, ...p } : d))

  // 切换 function_tools / cli_tools 里某工具的选中态（不可变：返回新数组）。
  const toggleTool = (field: 'function_tools' | 'cli_tools', name: string) =>
    setDraft((d) =>
      d
        ? {
            ...d,
            [field]: d[field].includes(name)
              ? d[field].filter((n) => n !== name)
              : [...d[field], name],
          }
        : d,
    )

  const onSave = async () => {
    if (!draft) return
    setSaving(true)
    try {
      await saveHunter(draft)
      setDraft(null)
      reload()
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
      reload()
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
        subtitle="领域智能体的系统提示词、工具集与调度摘要，编排者据此派活"
        loading={loading}
        error={error}
        empty={rows.length === 0}
        emptyHint={list.query ? '无匹配智能体' : '暂无智能体——点右上「新建」创建第一个'}
        onNew={() => setDraft(blankHunter())}
        search={{ value: list.query, onChange: list.setQuery, placeholder: '搜索名称 / 标识 / 描述' }}
        server={{
          page: list.page,
          totalPages: list.totalPages,
          count: list.total,
          onPage: list.setPage,
        }}
      >
        {rows.map((h) => (
          <ConfigRow
            key={h.id}
            name={h.name}
            description={h.description}
            dimmed={!h.enabled}
            onClick={() => setDraft(h)}
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
        tabs={
          draft
            ? [
                {
                  value: 'basic',
                  label: '基本信息',
                  content: (
                    <>
                      <div className="flex gap-3">
                        <Field label="标识符" hint="ID">
                          <input
                            className={INPUT_CLASS}
                            value={draft.code}
                            spellCheck={false}
                            disabled={!!draft.id}
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
                        <input
                          className={INPUT_CLASS}
                          value={draft.name}
                          onChange={(e) => patch({ name: e.target.value })}
                        />
                      </Field>
                      <Field label="调度摘要" hint="多智能体协同时编排者据此选派（非面向用户的描述）">
                        <input
                          className={INPUT_CLASS}
                          value={draft.description}
                          onChange={(e) => patch({ description: e.target.value })}
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
                  ),
                },
                {
                  value: 'prompt',
                  label: '系统提示词',
                  content: (
                    <Field label="系统提示词" hint="该智能体运行时的 system prompt">
                      <textarea
                        className={INPUT_CLASS + ' min-h-[22rem] resize-y font-mono'}
                        value={draft.body}
                        onChange={(e) => patch({ body: e.target.value })}
                      />
                    </Field>
                  ),
                },
                {
                  value: 'tools',
                  label: '工具集',
                  // 内部/外部工具是同一装配决策的两半，窄屏堆叠、≥640px 并排一屏对照。
                  content: (
                    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                      <Field label="内部工具集" hint="进程内函数工具；勾选后运行期可调用">
                        <ToolPicker
                          catalog={functionTools}
                          selected={draft.function_tools}
                          onToggle={(name) => toggleTool('function_tools', name)}
                          emptyHint="无可用函数工具目录"
                        />
                      </Field>
                      <Field label="外部工具集" hint="严格白名单：仅勾选的外部 CLI 工具装配，未勾选=不装配">
                        <ToolPicker
                          catalog={cliTools}
                          selected={draft.cli_tools}
                          onToggle={(name) => toggleTool('cli_tools', name)}
                          emptyHint="无可用外部工具目录"
                        />
                      </Field>
                    </div>
                  ),
                },
              ]
            : []
        }
      />
    </>
  )
}

// 工具多选器：按 category 分组（桶内按 name 字典序）渲染可勾选的工具标签。
// function / cli 两类共用同一交互，仅候选源不同。
function ToolPicker({
  catalog,
  selected,
  onToggle,
  emptyHint,
}: {
  catalog: Tool[]
  selected: string[]
  onToggle: (name: string) => void
  emptyHint: string
}) {
  const byCategory = useMemo(() => {
    const byCat = new Map<string, Tool[]>()
    for (const t of catalog) {
      const cat = t.category || '未分类'
      const bucket = byCat.get(cat) ?? []
      bucket.push(t)
      byCat.set(cat, bucket)
    }
    return [...byCat.entries()]
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([cat, tools]) => [cat, [...tools].sort((a, b) => a.name.localeCompare(b.name))] as const)
  }, [catalog])

  if (byCategory.length === 0) {
    return <p className="text-[13px] text-muted">{emptyHint}</p>
  }

  return (
    <div className="max-h-56 space-y-3 overflow-y-auto rounded-md border border-border bg-background p-3">
      {byCategory.map(([cat, tools]) => (
        <div key={cat}>
          <p className="mb-1 font-mono text-[11px] uppercase tracking-wide text-muted">{cat}</p>
          <div className="flex flex-wrap gap-2">
            {tools.map((t) => {
              const active = selected.includes(t.name)
              return (
                <button
                  key={t.name}
                  type="button"
                  title={t.description}
                  aria-pressed={active}
                  onClick={() => onToggle(t.name)}
                  className={
                    'rounded-md border px-2 py-1 font-mono text-[12px] transition-colors ' +
                    (active
                      ? 'border-accent bg-accent/15 text-accent'
                      : 'border-border text-text hover:border-accent/60')
                  }
                >
                  {t.name}
                </button>
              )
            })}
          </div>
        </div>
      ))}
    </div>
  )
}
