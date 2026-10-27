import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { listAgentsPaged } from '@/api/client'
import { saveAgent, listToolCandidates, listSkillCandidates } from '@/api/config'
import { diffChanges, isWildcard, wildcardView } from '@/features/config/agentsEditor'
import { ToolPicker } from '@/features/config/ToolPicker'
import type { AgentConfig, Skill, Tool } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { ConfigListShell, ConfigRow } from '@/features/config/ConfigListShell'
import { usePagedList } from '@/features/config/usePagedList'
import { ConfigDrawer, Field, INPUT_CLASS } from '@/features/config/ConfigDrawer'
import { agentProfile } from '@/lib/agentColor'

// 智能体分页取数：适配 usePagedList 的 (page,size,q) → {items,total} 契约。
const fetchAgents = async (page: number, size: number, q: string) => {
  const res = await listAgentsPaged(page, size, q)
  return { items: res.agents, total: res.total }
}

// 智能体配置管理页（/config/agents）：四个固定种子角色，编辑系统提示词 + 调度摘要 + 工具集 + 技能白名单。
// 角色身份（标识符/名称/启用）由种子固定，页面只读、接口不收；调度摘要是 planner 规划时
// 读取的角色分工摘要（LLM 可读面），可编辑。
// 保存只提交脏字段（PUT 部分更新），与工具页的并发装配写互不覆盖快照。
// 工具/技能候选从目录（DB）读取，勾选而非填空——function_tools / cli_tools / skills 三区多选。
export function AgentsPage() {
  const list = usePagedList<AgentConfig>(fetchAgents)
  const { rows, loading, error, reload } = list
  // 工具候选：函数工具 / CLI 工具两套全量目录，一次性拉取供勾选。
  const [functionTools, setFunctionTools] = useState<Tool[]>([])
  const [cliTools, setCliTools] = useState<Tool[]>([])
  const [skills, setSkills] = useState<Skill[]>([])
  const [draft, setDraft] = useState<AgentConfig | null>(null)
  // 打开抽屉时的原始行快照：脏 diff 的基准（patch 全为不可变更新，行对象不会被原地改）。
  const [baseline, setBaseline] = useState<AgentConfig | null>(null)
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

  // 本轮编辑的脏字段（空对象 = 无变更）：驱动保存按钮禁用与关闭确认。
  const changes = draft && baseline ? diffChanges(baseline, draft) : {}
  const dirty = Object.keys(changes).length > 0

  const patch = (p: Partial<AgentConfig>) => setDraft((d) => (d ? { ...d, ...p } : d))

  // 切换 function_tools / cli_tools / skills 里某项的选中态（不可变：返回新数组）。
  // 全量哨兵 ["*"] 期间目录以全集勾选展示（与其他形式一致）；取消勾选即物化为
  // 「全集减一」的显式白名单——与后端 applyAssignment 的卸载语义对称。
  const toggleTool = (
    field: 'function_tools' | 'cli_tools' | 'skills',
    name: string,
    catalog: string[],
  ) =>
    setDraft((d) => {
      if (!d) return d
      const next = isWildcard(d[field])
        ? catalog.filter((n) => n !== name)
        : d[field].includes(name)
          ? d[field].filter((n) => n !== name)
          : [...d[field], name]
      return { ...d, [field]: next }
    })

  // 全选：收拢为哨兵 ["*"]（展示层永远是"目录全集勾选"，与逐个勾满语义等价，
  // 且未来新增的工具自动装配）。
  const selectAll = (field: 'function_tools' | 'cli_tools' | 'skills') =>
    setDraft((d) => (d ? { ...d, [field]: ['*'] } : d))

  const openDraft = (h: AgentConfig) => {
    setDraft(h)
    setBaseline(h)
  }

  const closeDraft = () => {
    setDraft(null)
    setBaseline(null)
  }

  // 用户交互关闭（Esc/遮罩/关闭钮）：有未保存更改时先确认，防误触丢大段提示词。
  const requestClose = () => {
    if (dirty && !window.confirm('有未保存的更改，确认丢弃？')) return
    closeDraft()
  }

  const onSave = async () => {
    if (!draft || !baseline || !dirty) return
    setSaving(true)
    try {
      await saveAgent(draft.id, changes)
      closeDraft()
      reload()
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

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
        {rows.map((h) => {
          const profile = agentProfile(h.code)
          const Icon = profile.icon
          return (
            <ConfigRow
              key={h.id}
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
                  {/* 英文标识跟在中文名后：mono 弱化展示，卡片即见寻址键 */}
                  <span className="ml-2 font-mono text-xs font-normal text-muted">{h.code}</span>
                </>
              }
              description={h.description}
              dimmed={!h.enabled}
              onClick={() => openDraft(h)}
              right={!h.enabled ? <Badge variant="outline">已停用</Badge> : undefined}
            />
          )
        })}
      </ConfigListShell>

      <ConfigDrawer
        open={!!draft}
        title={draft?.name || '智能体'}
        onOpenChange={(o) => !o && requestClose()}
        onSave={() => void onSave()}
        saving={saving}
        saveDisabled={!dirty}
        tabs={
          draft
            ? [
                {
                  value: 'basic',
                  label: '基本信息',
                  content: (
                    <>
                      <Field
                        label="调度摘要"
                        hint="面向编排者的角色分工摘要：planner 规划时读取（LLM 可读，非面向用户的描述）"
                      >
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
                          onChange={(e) => {
                            // 逐键钳到 ≥1 的整数：Number('')=0，负数/小数/非法输入都不落 draft。
                            const n = Math.floor(Number(e.target.value))
                            patch({ max_iterations: Number.isFinite(n) && n >= 1 ? n : 1 })
                          }}
                        />
                      </Field>
                      <Field
                        label="复杂度档"
                        hint={
                          <>
                            决定该智能体的 LLM 路由档位：轻量快答 / 标准推理 / 深度推理。在
                            <Link className="text-accent hover:underline" to="/config/models?tab=assignment">
                              「模型」页
                            </Link>
                            把每档指到某个部署。
                          </>
                        }
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
                      <Field label="启用" hint="由种子与运维控制，接口不可改">
                        <label className="flex items-center gap-2 text-sm text-text">
                          <input type="checkbox" checked={draft.enabled} disabled />
                          {draft.enabled ? '已启用' : '已停用'}
                        </label>
                      </Field>
                    </>
                  ),
                },
                {
                  value: 'prompt',
                  label: '系统提示词',
                  content: (
                    // tab 名即字段名，不再重复 Field 标题行；flex-1 铺满抽屉剩余高度。
                    <textarea
                      className={INPUT_CLASS + ' min-h-[22rem] flex-1 resize-y font-mono text-sm'}
                      value={draft.system_prompt}
                      placeholder="你是一个专业的安全扫描助手..."
                      onChange={(e) => patch({ system_prompt: e.target.value })}
                    />
                  ),
                },
                {
                  value: 'function_tools',
                  label: '内部工具',
                  content: (
                    <ToolPicker
                      catalog={functionTools}
                      selected={wildcardView(draft.function_tools, functionTools.map((t) => t.name))}
                      onToggle={(name) => toggleTool('function_tools', name, functionTools.map((t) => t.name))}
                      onSelectAll={() => selectAll('function_tools')}
                      emptyHint={isWildcard(draft.function_tools) ? '目录为空（全量模式下自动装配未来新增）' : '无可用内部工具'}
                    />
                  ),
                },
                {
                  value: 'cli_tools',
                  label: '外部工具',
                  content: (
                    <ToolPicker
                      catalog={cliTools}
                      selected={wildcardView(draft.cli_tools, cliTools.map((t) => t.name))}
                      onToggle={(name) => toggleTool('cli_tools', name, cliTools.map((t) => t.name))}
                      onSelectAll={() => selectAll('cli_tools')}
                      emptyHint={isWildcard(draft.cli_tools) ? '目录为空（全量模式下自动装配未来新增）' : '无可用外部工具'}
                    />
                  ),
                },
                {
                  value: 'skills',
                  label: '技能',
                  content: (
                    <ToolPicker
                      // 技能以 code 裸名寻址：多选器候选映射到最小结构，不再伪造 Tool 全字段。
                      catalog={skills.map((sk) => ({
                        name: sk.code,
                        category: sk.category,
                        description: sk.description,
                      }))}
                      selected={wildcardView(draft.skills, skills.map((sk) => sk.code))}
                      onToggle={(name) => toggleTool('skills', name, skills.map((sk) => sk.code))}
                      onSelectAll={() => selectAll('skills')}
                      emptyHint={isWildcard(draft.skills) ? '目录为空（全量模式下自动装配未来新增）' : '无可用技能知识库（可在「知识库」页创建）'}
                    />
                  ),
                },
              ]
            : []
        }
      />
    </>
  )
}
