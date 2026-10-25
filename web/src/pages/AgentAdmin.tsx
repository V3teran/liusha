import { useEffect, useMemo, useState } from 'react'
import { listAgentsPaged } from '@/api/client'
import { saveAgent, deleteAgent, listToolCandidates, listSkillCandidates } from '@/api/config'
import type { AgentConfig, Skill, Tool } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { ConfigListShell, ConfigRow } from '@/features/config/ConfigListShell'
import { usePagedList } from '@/features/config/usePagedList'
import { ConfigDrawer, Field, INPUT_CLASS } from '@/features/config/ConfigDrawer'

// 智能体分页取数：适配 usePagedList 的 (page,size,q) → {items,total} 契约。
const fetchAgents = async (page: number, size: number, q: string) => {
  const res = await listAgentsPaged(page, size, q)
  return { items: res.agents, total: res.total }
}

// 智能体配置管理页（后端资源仍名 agent）：领域智能体系统提示词 + 工具集 + 调度摘要，全字段编辑。
// 工具集从工具目录（DB）读取候选，勾选而非填空——function_tools（内部函数工具）与 cli_tools（外部 CLI 工具）分区多选。
export function AgentAdmin() {
  const list = usePagedList<AgentConfig>(fetchAgents)
  const { rows, loading, error, reload } = list
  // 工具候选：函数工具 / CLI 工具两套全量目录，一次性拉取供勾选。
  const [functionTools, setFunctionTools] = useState<Tool[]>([])
  const [cliTools, setCliTools] = useState<Tool[]>([])
  const [skills, setSkills] = useState<Skill[]>([])
  const [draft, setDraft] = useState<AgentConfig | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    void listToolCandidates('function')
      .then(setFunctionTools)
      .catch(() => setFunctionTools([]))
    void listToolCandidates('cli')
      .then(setCliTools)
      .catch(() => setCliTools([]))
    void listSkillCandidates()
      .then(setSkills)
      .catch(() => setSkills([]))
  }, [])

  const patch = (p: Partial<AgentConfig>) => setDraft((d) => (d ? { ...d, ...p } : d))

  // 切换 function_tools / cli_tools / skills 里某项的选中态（不可变：返回新数组）。
  const toggleTool = (field: 'function_tools' | 'cli_tools' | 'skills', name: string) =>
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
      await saveAgent(draft)
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
      await deleteAgent(draft.id)
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
        subtitle="领域智能体的系统提示词、工具集与调度摘要"
        loading={loading}
        error={error}
        empty={rows.length === 0}
        emptyHint={list.query ? '无匹配智能体' : '暂无智能体'}
        search={{ value: list.query, onChange: list.setQuery, placeholder: '搜索名称 / 标识 / 描述' }}
        server={{
          page: list.page,
          totalPages: list.totalPages,
          count: list.total,
          onPage: list.setPage,
          size: list.size,
          onSize: list.setSize,
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
              !h.enabled ? <Badge variant="outline">已停用</Badge> : undefined
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
                        <Field label="标识符" hint="唯一ID，创建后不可修改">
                          <input
                            className={INPUT_CLASS}
                            value={draft.code}
                            spellCheck={false}
                            disabled={!!draft.id}
                            onChange={(e) => patch({ code: e.target.value })}
                          />
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
                      <Field
                        label="复杂度档"
                        hint="决定该智能体的 LLM 路由档位：轻量快答 / 标准推理 / 深度推理。在「模型」页把每档指到某个部署。"
                      >
                        <select
                          className={INPUT_CLASS}
                          value={draft.complexity}
                          onChange={(e) => patch({ complexity: e.target.value })}
                        >
                          <option value="simple">轻量快答（simple）</option>
                          <option value="medium">标准推理（medium）</option>
                          <option value="complex">深度推理（complex）</option>
                        </select>
                      </Field>
                      <label className="flex items-center gap-2 text-sm text-text">
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
                        className={INPUT_CLASS + ' min-h-[22rem] resize-y font-mono text-sm'}
                        value={draft.system_prompt}
                        placeholder="你是一个专业的安全扫描助手..."
                        onChange={(e) => patch({ system_prompt: e.target.value })}
                      />
                    </Field>
                  ),
                },
                {
                  value: 'function_tools',
                  label: '内部工具',
                  content: (
                    <Field
                      label="内部函数工具"
                      hint="进程内工具，提供核心能力（如数据库查询、文件操作等）"
                    >
                      {draft.function_tools?.length === 1 && draft.function_tools[0] === '*' ? (
                        <div className="rounded-lg border border-accent/30 bg-accent/5 px-4 py-3">
                          <div className="flex items-center gap-2">
                            <span className="rounded bg-accent/20 px-2 py-1 text-xs font-semibold text-accent">
                              全量模式
                            </span>
                            <span className="text-sm text-muted-foreground">
                              自动装配所有内部工具
                            </span>
                          </div>
                        </div>
                      ) : (
                        <ToolPicker
                          catalog={functionTools}
                          selected={draft.function_tools}
                          onToggle={(name) => toggleTool('function_tools', name)}
                          emptyHint="无可用内部工具"
                        />
                      )}
                    </Field>
                  ),
                },
                {
                  value: 'cli_tools',
                  label: '外部工具',
                  content: (
                    <Field
                      label="外部 CLI 工具"
                      hint="严格白名单：仅勾选的外部工具可调用（如 nmap、sqlmap 等安全工具）"
                    >
                      {draft.cli_tools?.length === 1 && draft.cli_tools[0] === '*' ? (
                        <div className="rounded-lg border border-accent/30 bg-accent/5 px-4 py-3">
                          <div className="flex items-center gap-2">
                            <span className="rounded bg-accent/20 px-2 py-1 text-xs font-semibold text-accent">
                              全量模式
                            </span>
                            <span className="text-sm text-muted-foreground">
                              自动装配所有 {cliTools.length} 个外部工具（沙箱隔离保证安全）
                            </span>
                          </div>
                        </div>
                      ) : (
                        <ToolPicker
                          catalog={cliTools}
                          selected={draft.cli_tools}
                          onToggle={(name) => toggleTool('cli_tools', name)}
                          emptyHint="无可用外部工具"
                        />
                      )}
                    </Field>
                  ),
                },
                {
                  value: 'skills',
                  label: '技能集',
                  content: (
                    <Field
                      label="技能知识库"
                      hint="渐进式加载的领域知识，智能体可通过 read_skill 按需读取"
                    >
                      {draft.skills?.length === 1 && draft.skills[0] === '*' ? (
                        <div className="rounded-lg border border-accent/30 bg-accent/5 px-4 py-3">
                          <div className="flex items-center gap-2">
                            <span className="rounded bg-accent/20 px-2 py-1 text-xs font-semibold text-accent">
                              全量模式
                            </span>
                            <span className="text-sm text-muted-foreground">
                              自动装配所有技能知识库
                            </span>
                          </div>
                        </div>
                      ) : (
                        <ToolPicker
                          catalog={skills.map((sk) => ({
                            name: sk.code,
                            category: sk.category,
                            description: sk.description,
                            kind: 'function' as const,
                            sort_order: 0
                          }))}
                          selected={draft.skills}
                          onToggle={(name) => toggleTool('skills', name)}
                          emptyHint="无可用技能知识库（可在「知识库」页创建）"
                        />
                      )}
                    </Field>
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
    return <p className="text-sm text-muted">{emptyHint}</p>
  }

  return (
    <div className="max-h-56 space-y-3 overflow-y-auto rounded-md border border-border bg-background p-3">
      {byCategory.map(([cat, tools]) => (
        <div key={cat}>
          <p className="mb-1 font-mono text-xs uppercase tracking-wide text-muted">{cat}</p>
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
                    'rounded-md border px-2 py-1 font-mono text-xs transition-colors ' +
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
