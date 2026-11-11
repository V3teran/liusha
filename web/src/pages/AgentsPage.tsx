import { useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  deletePromptArchive,
  getAgent,
  listSkillCandidates,
  listToolCandidates,
  patchAgent,
  resetAgent,
} from '@/api/config'
import type { AgentDetail, AgentListItem, SkillListItem, Tool } from '@/api/types'
import { Badge } from '@/components/ui/Badge'
import { INPUT_CLASS, TEXTAREA_CLASS, NUMBER_INPUT_CLASS } from '@/components/ui/control'
import { Select } from '@/components/ui/Select'
import { ConfigDrawer, Field } from '@/features/config/ConfigDrawer'
import { ConfigListShell, ConfigRow } from '@/features/config/ConfigListShell'
import { OverrideStatus } from '@/features/config/OverrideStatus'
import { ToolPicker, type PickerCandidate } from '@/features/config/ToolPicker'
import {
  draftOf,
  patchOf,
  rebaseDraft,
  type AgentDraft,
  type AgentSetField,
  type Catalogs,
} from '@/features/config/agentsEditor'
import { useAgentsPagedList } from '@/features/config/useConfigQueries'
import { useDirtyDraft } from '@/hooks/useDirtyDraft'
import { agentProfile } from '@/lib/agentColor'
import { COMPLEXITY_GROUPS, groupMeta } from '@/features/model/roles'
import { toggleAll, toggleChecked } from '@/lib/override'

// 智能体页（/config/agents）：四个固定角色。出厂配置随程序发布，这里编辑的是用户自定义：
// 章程、调度摘要、迭代上限、档位与三份白名单。保存只提交改动的字段；与出厂相同的值不算
// 自定义；每个已自定义字段可对比出厂、单独恢复默认。输出契约随程序固定，只读展示。
export function AgentsPage() {
  const list = useAgentsPagedList()
  const { rows, loading, error, reload } = list

  const [functionTools, setFunctionTools] = useState<Tool[]>([])
  const [cliTools, setCliTools] = useState<Tool[]>([])
  const [skills, setSkills] = useState<SkillListItem[]>([])
  const [catalogsReady, setCatalogsReady] = useState(false)

  const [detail, setDetail] = useState<AgentDetail | null>(null)
  const { draft, setDraft, patch, open, close, isDirty: dirty, baseline } = useDirtyDraft<AgentDraft>()
  const [saving, setSaving] = useState(false)
  const [resetting, setResetting] = useState<string | null>(null)
  // 抽屉当前展示的 agent：详情请求是异步的，迟到的响应不得覆盖后打开的另一个 agent。
  const openCodeRef = useRef<string | null>(null)

  useEffect(() => {
    void Promise.all([
      listToolCandidates('function').catch(() => []),
      listToolCandidates('cli').catch(() => []),
      listSkillCandidates().catch(() => []),
    ]).then(([fn, cli, sk]) => {
      setFunctionTools(fn)
      setCliTools(cli)
      setSkills(sk)
      setCatalogsReady(true)
    })
  }, [])

  const catalogs = useMemo<Catalogs>(
    () => ({
      function_tools: functionTools.map((t) => t.name),
      cli_tools: cliTools.map((t) => t.name),
      skills: skills.map((s) => s.code),
    }),
    [functionTools, cliTools, skills],
  )

  const changes = useMemo(
    () => (draft && baseline ? patchOf(baseline, draft, catalogs) : {}),
    [draft, baseline, catalogs],
  )

  const openAgent = (code: string) => {
    openCodeRef.current = code
    getAgent(code)
      .then((d) => {
        if (openCodeRef.current === code) setDetail(d)
      })
      .catch((e: unknown) => window.alert(e instanceof Error ? e.message : '加载失败'))
  }

  // 详情与候选目录都就绪后才生成草稿：「全部」模式要按目录全集展开勾选项。
  useEffect(() => {
    if (detail && catalogsReady && !draft) open(draftOf(detail, catalogs))
  }, [detail, catalogsReady, catalogs, draft, open])

  const closeDrawer = () => {
    openCodeRef.current = null
    setDetail(null)
    close()
  }

  const requestClose = () => {
    if (dirty && !window.confirm('有未保存的更改，确认丢弃？')) return
    closeDrawer()
  }

  const onSave = async () => {
    if (!detail || !dirty || Object.keys(changes).length === 0) return
    setSaving(true)
    try {
      await patchAgent(detail.agent.code, changes)
      closeDrawer()
      reload()
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  // 恢复默认是即时的服务端操作；其它字段上未保存的编辑保留。
  const onReset = async (field?: keyof AgentDraft) => {
    if (!detail || !draft || !baseline) return
    const what = field ? '该项' : '全部字段'
    if (!window.confirm(`把${what}恢复为出厂值？`)) return
    setResetting(field ?? 'all')
    try {
      const fresh = await resetAgent(detail.agent.code, field)
      const newBaseline = draftOf(fresh, catalogs)
      const merged = field ? rebaseDraft(baseline, draft, newBaseline, field) : newBaseline
      setDetail(fresh)
      open(newBaseline)
      setDraft(merged)
      reload()
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '恢复失败')
    } finally {
      setResetting(null)
    }
  }

  const onDismissArchive = async () => {
    if (!detail) return
    if (!window.confirm('确认已对照处理升级前的提示词？删除后无法找回。')) return
    try {
      await deletePromptArchive(detail.agent.code)
      setDetail({ ...detail, prompt_archive: null })
      reload()
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '删除失败')
    }
  }

  const status = (field: keyof AgentDraft, long = false) =>
    detail && (
      <OverrideStatus
        state={detail.overrides[field]}
        current={detail.agent[field]}
        long={long}
        busy={resetting === field}
        onReset={() => void onReset(field)}
      />
    )

  const picker = (field: AgentSetField, candidates: PickerCandidate[], emptyHint: string) =>
    draft && (
      <div className="flex min-h-0 flex-1 flex-col gap-2">
        {status(field)}
        <ToolPicker
          catalog={candidates}
          draft={draft[field]}
          onToggle={(name) => setDraft((d) => (d ? { ...d, [field]: toggleChecked(d[field], name) } : d))}
          onToggleAll={() => setDraft((d) => (d ? { ...d, [field]: toggleAll(d[field], catalogs[field]) } : d))}
          emptyHint={emptyHint}
        />
      </div>
    )

  const customizedCount = detail ? Object.keys(detail.overrides).length : 0

  return (
    <>
      <ConfigListShell
        title="智能体"
        subtitle="出厂配置随版本更新；在此自定义章程、工具与技能，改过的项可随时恢复默认"
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
        {rows.map((h) => {
          const profile = agentProfile(h.code)
          const Icon = profile.icon
          return (
            <ConfigRow
              key={h.code}
              leading={
                <span
                  className="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-full"
                  style={{ color: profile.accent, background: profile.soft }}
                >
                  <Icon className="h-4 w-4" strokeWidth={2.2} />
                </span>
              }
              name={
                <>
                  {h.name}
                  <span className="ml-2 font-mono text-xs font-normal text-text-secondary">{h.code}</span>
                </>
              }
              description={h.description}
              meta={<AgentMeta agent={h} />}
              onClick={() => openAgent(h.code)}
              right={
                <div className="flex items-center gap-2">
                  {h.has_prompt_archive && <Badge variant="outline">待处理的旧提示词</Badge>}
                  {h.customized.length > 0 && <Badge variant="outline">已自定义 {h.customized.length} 项</Badge>}
                </div>
              }
            />
          )
        })}
      </ConfigListShell>

      <ConfigDrawer
        open={!!detail}
        title={detail?.agent.name || '智能体'}
        onOpenChange={(o) => !o && requestClose()}
        onSave={() => void onSave()}
        saving={saving}
        saveDisabled={!dirty || Object.keys(changes).length === 0}
        tabs={
          detail && draft
            ? [
                {
                  value: 'basic',
                  label: '基本信息',
                  content: (
                    <>
                      <Field
                        label="调度摘要"
                        hint="面向编排者的角色分工摘要：planner 规划时读取"
                        aside={status('description')}
                      >
                        <input
                          className={INPUT_CLASS}
                          value={draft.description}
                          onChange={(e) => patch({ description: e.target.value })}
                        />
                      </Field>
                      <Field label="最大迭代轮数" hint="ReAct 推理循环上限（1–1000）" aside={status('max_iterations')}>
                        <input
                          type="number"
                          min={1}
                          max={1000}
                          className={NUMBER_INPUT_CLASS}
                          value={draft.max_iterations}
                          onChange={(e) => {
                            const n = Math.floor(Number(e.target.value))
                            patch({ max_iterations: Number.isFinite(n) && n >= 1 ? n : 1 })
                          }}
                        />
                      </Field>
                      <Field
                        label="复杂度档"
                        hint={
                          <>
                            决定该智能体的 LLM 路由档位。在
                            <Link className="text-accent hover:underline" to="/config/models?tab=assignment">
                              「模型」页
                            </Link>
                            把每档指到某个部署。
                          </>
                        }
                        aside={status('complexity')}
                      >
                        <Select
                          className="w-full"
                          value={draft.complexity}
                          onChange={(e) => patch({ complexity: e.target.value })}
                        >
                          {COMPLEXITY_GROUPS.map((g) => (
                            <option key={g.key} value={g.key}>
                              {g.label}（{g.key}）
                            </option>
                          ))}
                        </Select>
                      </Field>
                      {customizedCount > 0 && (
                        <div className="flex items-center justify-between rounded-md border border-border px-3 py-2 text-xs text-text-secondary">
                          <span>已自定义 {customizedCount} 项</span>
                          <button
                            type="button"
                            disabled={resetting !== null}
                            className="underline-offset-2 hover:text-accent hover:underline disabled:opacity-50"
                            onClick={() => void onReset()}
                          >
                            全部恢复出厂
                          </button>
                        </div>
                      )}
                    </>
                  ),
                },
                {
                  value: 'charter',
                  label: '章程',
                  content: (
                    <div className="flex min-h-0 flex-1 flex-col gap-3">
                      {detail.prompt_archive && (
                        <section
                          aria-label="升级前的自定义提示词"
                          className="rounded-md border border-status-warning/50 bg-status-warning/5 px-3 py-2 text-xs"
                        >
                          <p className="text-text">
                            升级前你自定义过这个智能体的提示词。新版把提示词拆成了可自定义的「章程」与随程序固定的
                            「输出契约」，旧的整份提示词无法自动拆分，原文已归档。请对照后决定是否把其中的修改重新写进章程。
                          </p>
                          <details className="mt-2">
                            <summary className="cursor-pointer text-text-secondary">查看归档原文</summary>
                            <pre className="mt-1 max-h-72 overflow-auto whitespace-pre-wrap rounded border border-border bg-background p-2 font-mono">
                              {detail.prompt_archive.prompt}
                            </pre>
                          </details>
                          <button
                            type="button"
                            className="mt-2 text-text-secondary underline-offset-2 hover:text-accent hover:underline"
                            onClick={() => void onDismissArchive()}
                          >
                            我已处理，删除归档
                          </button>
                        </section>
                      )}
                      {status('charter', true)}
                      <textarea
                        aria-label="章程"
                        className={TEXTAREA_CLASS + ' min-h-[20rem] flex-1 resize-y font-mono'}
                        value={draft.charter}
                        onChange={(e) => patch({ charter: e.target.value })}
                      />
                      {detail.contract && (
                        <details className="rounded-md border border-border px-3 py-2 text-xs">
                          <summary className="cursor-pointer text-text-secondary">
                            输出契约（随程序版本，不可修改；附在章程之后发给模型）
                          </summary>
                          <pre className="mt-2 max-h-80 overflow-auto whitespace-pre-wrap font-mono text-text-secondary">
                            {detail.contract}
                          </pre>
                        </details>
                      )}
                    </div>
                  ),
                },
                {
                  value: 'function_tools',
                  label: '内部工具',
                  content: picker('function_tools', functionTools, '无可用内部工具'),
                },
                {
                  value: 'cli_tools',
                  label: '外部工具',
                  content: picker('cli_tools', cliTools, '无可用外部工具'),
                },
                {
                  value: 'skills',
                  label: '技能',
                  content: picker(
                    'skills',
                    skills.map((sk) => ({ name: sk.code, category: sk.category, description: sk.description })),
                    '无可用技能（可在「技能」页创建）',
                  ),
                },
              ]
            : []
        }
      />
    </>
  )
}

// 名单字段的规模：含「*」即全部（含以后新增），否则为成员数。
function listSize(list: string[]): string {
  return list.includes('*') ? '全部' : String(list.length)
}

// AgentMeta 是智能体卡片底部的配置概要：推理档位、可用工具与技能规模、迭代上限——
// 不打开抽屉就能横向比较四个智能体的装配差异。
function AgentMeta({ agent }: { agent: AgentListItem }) {
  const items: [string, string][] = [
    ['档位', groupMeta(agent.complexity || 'medium').label],
    ['内部工具', listSize(agent.function_tools)],
    ['外部工具', listSize(agent.cli_tools)],
    ['技能', listSize(agent.skills)],
    ['迭代上限', String(agent.max_iterations)],
  ]
  return (
    <dl className="flex flex-wrap gap-x-4 gap-y-1 text-xs">
      {items.map(([k, v]) => (
        <div key={k} className="flex items-baseline gap-1.5">
          <dt className="text-text-tertiary">{k}</dt>
          <dd className="font-medium tabular-nums text-text">{v}</dd>
        </div>
      ))}
    </dl>
  )
}
