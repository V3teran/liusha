import { useMemo, useState } from 'react'
import { useDirtyDraft } from '@/hooks/useDirtyDraft'
import { createSkill, updateSkill, deleteSkill, getSkillDetail, assignSkill, type SkillChanges, type SkillAgent } from '@/api/config'
import type { Skill } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { ConfigListShell, ConfigRow } from '@/features/config/ConfigListShell'
import { useSkillsPagedList } from '@/features/config/useConfigQueries'
import { ConfigDrawer, Field, INPUT_CLASS } from '@/features/config/ConfigDrawer'
import { AgentAssignSection } from '@/components/AgentAssignSection'

// 空白新建草稿（code 由用户填，建后固定；is_builtin 由后端定 = false）。
const EMPTY_DRAFT: Skill = {
  id: '',
  code: '',
  name: '',
  description: '',
  category: '',
  body: '',
  is_builtin: false,
  enabled: true,
}

// 取数全权交给 useSkillsPagedList（React Query：queryKey 含 page/size/q，竞态丢弃
// 由 RQ 接管）；页面不得再自行 reload——在 effect 里盯 query 逐键 reload 会绕过
// 防抖、挂载时双请求（历史缺陷）。保存/删除后 list.reload() 失效该资源缓存。
export default function SkillsPage() {
  const list = useSkillsPagedList()
  const rows = list.rows

  const { draft, patch, open: openDraftState, close: closeDraftState, baseline } = useDirtyDraft<Skill>()
  // 新建模式（无 id 走 POST）；编辑模式按 code 走 PUT 部分更新。
  const [isNew, setIsNew] = useState(false)
  const [saving, setSaving] = useState(false)
  // 装配 tab 数据：抽屉打开时拉详情（全量智能体及各自装配态，后端权威）。
  const [assignAgents, setAssignAgents] = useState<SkillAgent[] | null>(null)
  const [assignSaving, setAssignSaving] = useState<string | null>(null)

  const openDraft = (sk: Skill) => {
    openDraftState(sk)
    setIsNew(false)
    setAssignAgents(null)
    // 装配态随详情拉取（后端权威）；自建/内置都可装配。
    getSkillDetail(sk.code)
      .then((d) => setAssignAgents(d.agents))
      .catch(() => setAssignAgents([]))
  }

  // 装配胶囊点击：乐观翻转 → assignSkill 回存（失败回滚并提示）。后端单一权威。
  const toggleAssign = (agentCode: string, next: boolean) => {
    if (!draft) return
    setAssignSaving(agentCode)
    setAssignAgents((prev) =>
      prev ? prev.map((a) => (a.code === agentCode ? { ...a, involved: next } : a)) : prev,
    )
    assignSkill(draft.code, agentCode, next)
      .catch(() => {
        setAssignAgents((prev) =>
          prev ? prev.map((a) => (a.code === agentCode ? { ...a, involved: !next } : a)) : prev,
        )
        window.alert('装配状态保存失败')
      })
      .finally(() => setAssignSaving(null))
  }

  const openNew = () => {
    openDraftState(EMPTY_DRAFT)
    setIsNew(true)
  }

  const closeDraft = () => {
    closeDraftState()
    setIsNew(false)
  }

  const requestClose = () => {
    if (dirty && !window.confirm('有未保存的更改，确认丢弃？')) return
    closeDraft()
  }

  const changes = useMemo<SkillChanges | null>(() => {
    if (!draft || !baseline) return null
    const c: SkillChanges = {}
    if (draft.name !== baseline.name) c.name = draft.name
    if (draft.description !== baseline.description) c.description = draft.description
    if (draft.category !== baseline.category) c.category = draft.category
    if (draft.body !== baseline.body) c.body = draft.body
    if (draft.enabled !== baseline.enabled) c.enabled = draft.enabled
    return c
  }, [draft, baseline])
  const dirty = !!changes && Object.keys(changes).length > 0

  // 新建必填校验；编辑态无变更禁用保存。
  const createInvalid =
    isNew &&
    (!draft?.code.trim() || !draft?.name.trim() || !draft?.category.trim() || !draft?.body.trim())

  const onSave = async () => {
    if (!draft || !dirty) return
    setSaving(true)
    try {
      if (isNew) {
        await createSkill({
          code: draft.code.trim(),
          name: draft.name.trim(),
          description: draft.description,
          category: draft.category.trim(),
          body: draft.body,
          enabled: draft.enabled,
        })
      } else if (changes) {
        await updateSkill(draft.code, changes)
      }
      closeDraft()
      list.reload()
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const onDelete = async () => {
    if (!draft) return
    if (!window.confirm(`删除技能「${draft.name}」？不可恢复。`)) return
    setSaving(true)
    try {
      await deleteSkill(draft.code)
      closeDraft()
      list.reload()
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '删除失败')
    } finally {
      setSaving(false)
    }
  }

  const saveDisabled = saving || createInvalid || (!isNew && !dirty)

  return (
    <>
      <ConfigListShell
        title="技能知识库"
        subtitle="领域知识与方法论"
        loading={list.loading}
        error={list.error}
        empty={rows.length === 0}
        emptyHint={list.query ? '无匹配技能' : '暂无技能'}
        search={{ value: list.query, onChange: list.setQuery, placeholder: '搜索名称 / 描述' }}
        server={{
          page: list.page,
          totalPages: list.totalPages,
          count: list.total,
          onPage: list.setPage,
          size: list.size,
          onSize: list.setSize,
        }}
        onNew={openNew}
      >
        {rows.map((sk) => (
          <ConfigRow
            key={sk.id}
            name={sk.name}
            description={sk.description}
            dimmed={!sk.enabled}
            onClick={() => openDraft(sk)}
            right={
              <div className="flex items-center gap-2">
                <Badge variant="outline">{sk.category}</Badge>
                {sk.is_builtin && <Badge variant="outline">内置</Badge>}
                {!sk.enabled && <Badge variant="outline">已停用</Badge>}
              </div>
            }
          />
        ))}
      </ConfigListShell>

      <ConfigDrawer
        open={!!draft}
        title={isNew ? '新建技能' : draft?.name || '技能详情'}
        onOpenChange={(o) => !o && requestClose()}
        onSave={() => void onSave()}
        onDelete={isNew || draft?.is_builtin ? undefined : () => void onDelete()}
        saving={saving}
        saveDisabled={saveDisabled}
        tabs={
          draft
            ? [
                {
                  value: 'info',
                  label: '基本信息',
                  content: (
                    <>
                      <div className="flex gap-3">
                        <Field
                          label="标识符"
                          hint={isNew ? '唯一 code（寻址键），创建后不可修改' : '创建后固定'}
                        >
                          <input
                            className={INPUT_CLASS}
                            value={draft.code}
                            spellCheck={false}
                            disabled={!isNew}
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
                      <Field label="分类" hint="tooling / vuln 等目录归类">
                        <input
                          className={INPUT_CLASS}
                          value={draft.category}
                          onChange={(e) => patch({ category: e.target.value })}
                        />
                      </Field>
                      <Field
                        label="描述"
                        hint="进 agent 技能索引（Tier 1）的摘要——直接决定 LLM 何时选用本技能"
                      >
                        <textarea
                          className={INPUT_CLASS + ' min-h-[4rem] resize-y'}
                          value={draft.description}
                          onChange={(e) => patch({ description: e.target.value })}
                        />
                      </Field>
                      <div className="flex items-center gap-4 text-sm text-muted-foreground">
                        {draft.is_builtin && <Badge variant="outline">内置</Badge>}
                        <label className="flex items-center gap-2">
                          <input
                            type="checkbox"
                            checked={draft.enabled}
                            onChange={(e) => patch({ enabled: e.target.checked })}
                          />
                          启用
                        </label>
                      </div>
                      {!isNew &&
                        (assignAgents === null ? (
                          <p className="text-sm text-muted">装配状态加载中…</p>
                        ) : (
                          <AgentAssignSection
                            agents={assignAgents.map((a) => ({
                              code: a.code,
                              name: a.name,
                              involved: a.involved,
                            }))}
                            savingCode={assignSaving}
                            onToggle={toggleAssign}
                          />
                        ))}
                    </>
                  ),
                },
                {
                  value: 'content',
                  label: '知识内容',
                  content: (
                    <Field
                      label="技能文档（Markdown）"
                      hint="read_skill 全文读取的方法论正文（Tier 2）；修改以数据库为准"
                    >
                      <textarea
                        className={INPUT_CLASS + ' min-h-[26rem] resize-y font-mono text-sm'}
                        value={draft.body}
                        onChange={(e) => patch({ body: e.target.value })}
                      />
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
