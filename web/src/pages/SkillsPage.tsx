import { useMemo, useRef, useState } from 'react'
import {
  assignSkill,
  createSkill,
  deleteSkill,
  getSkill,
  patchSkill,
  resetSkill,
  type SkillPatch,
} from '@/api/config'
import type { SkillAgent, SkillDetail } from '@/api/types'
import { AgentAssignSection } from '@/components/AgentAssignSection'
import { Badge } from '@/components/ui/Badge'
import { INPUT_CLASS, TEXTAREA_CLASS } from '@/components/ui/control'
import { ConfigDrawer, Field } from '@/features/config/ConfigDrawer'
import { ConfigListShell, ConfigRow } from '@/features/config/ConfigListShell'
import { OverrideStatus } from '@/features/config/OverrideStatus'
import { useSkillsPagedList } from '@/features/config/useConfigQueries'
import { useDirtyDraft } from '@/hooks/useDirtyDraft'

// 技能页（/config/skills）。内置技能的出厂内容随程序发布：可自定义名称、描述、正文、启用，
// 可对比出厂与恢复默认，不可删除；自建技能可增删改。同标识时自建技能优先（内置版本被遮蔽）。

interface SkillDraft {
  code: string
  category: string
  name: string
  description: string
  body: string
  enabled: boolean
}

type ContentField = 'name' | 'description' | 'body' | 'enabled'

const EMPTY_DRAFT: SkillDraft = { code: '', category: '', name: '', description: '', body: '', enabled: true }

const draftOf = (d: SkillDetail): SkillDraft => ({
  code: d.skill.code,
  category: d.skill.category,
  name: d.skill.name,
  description: d.skill.description,
  body: d.skill.body,
  enabled: d.skill.enabled,
})

export function SkillsPage() {
  const list = useSkillsPagedList()
  const rows = list.rows

  const { draft, patch, open, close, rebase, baseline } = useDirtyDraft<SkillDraft>()
  const [detail, setDetail] = useState<SkillDetail | null>(null)
  const [isNew, setIsNew] = useState(false)
  const [saving, setSaving] = useState(false)
  const [resetting, setResetting] = useState<string | null>(null)
  const [assignAgents, setAssignAgents] = useState<SkillAgent[] | null>(null)
  const [assignSaving, setAssignSaving] = useState<string | null>(null)
  // 抽屉当前展示的技能：详情与装配请求都是异步的，迟到的结果不得覆盖后打开的另一个技能。
  const openCodeRef = useRef<string | null>(null)

  const applyDetail = (d: SkillDetail) => {
    setDetail(d)
    setAssignAgents(d.agents)
  }

  const openSkill = (code: string) => {
    openCodeRef.current = code
    setIsNew(false)
    getSkill(code)
      .then((d) => {
        if (openCodeRef.current !== code) return
        applyDetail(d)
        open(draftOf(d))
      })
      .catch((e: unknown) => window.alert(e instanceof Error ? e.message : '加载失败'))
  }

  const openNew = () => {
    openCodeRef.current = null
    setDetail(null)
    setAssignAgents(null)
    setIsNew(true)
    open(EMPTY_DRAFT)
  }

  const closeDrawer = () => {
    openCodeRef.current = null
    setDetail(null)
    setIsNew(false)
    close()
  }

  const changes = useMemo<SkillPatch>(() => {
    if (!draft || !baseline) return {}
    const c: SkillPatch = {}
    if (draft.name !== baseline.name) c.name = draft.name
    if (draft.description !== baseline.description) c.description = draft.description
    if (draft.body !== baseline.body) c.body = draft.body
    if (draft.enabled !== baseline.enabled) c.enabled = draft.enabled
    return c
  }, [draft, baseline])
  const dirty = isNew || Object.keys(changes).length > 0

  const requestClose = () => {
    const edited = isNew ? JSON.stringify(draft) !== JSON.stringify(EMPTY_DRAFT) : dirty
    if (edited && !window.confirm('有未保存的更改，确认丢弃？')) return
    closeDrawer()
  }

  const createInvalid =
    isNew && (!draft?.code.trim() || !draft?.name.trim() || !draft?.category.trim() || !draft?.body.trim())

  const onSave = async () => {
    if (!draft) return
    setSaving(true)
    try {
      if (isNew) {
        await createSkill({
          code: draft.code.trim(),
          name: draft.name.trim(),
          description: draft.description,
          category: draft.category.trim(),
          body: draft.body,
        })
      } else {
        await patchSkill(draft.code, changes)
      }
      closeDrawer()
      list.reload()
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const onDelete = async () => {
    if (!draft) return
    if (!window.confirm(`删除技能「${draft.name}」？不可恢复，各智能体对它的装配也会一并移除。`)) return
    setSaving(true)
    try {
      await deleteSkill(draft.code)
      closeDrawer()
      list.reload()
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '删除失败')
    } finally {
      setSaving(false)
    }
  }

  // 恢复默认是即时的服务端操作；其它字段上未保存的编辑保留。
  const onReset = async (field?: ContentField) => {
    if (!draft || !baseline) return
    if (!window.confirm(`把${field ? '该项' : '全部字段'}恢复为出厂内容？`)) return
    const code = draft.code
    setResetting(field ?? 'all')
    try {
      const fresh = await resetSkill(code, field)
      list.reload()
      // 请求在途时抽屉已关闭或切到别的技能：不重开旧对象、不碰当前草稿。
      if (openCodeRef.current !== code) return
      const newBaseline = draftOf(fresh)
      applyDetail(fresh)
      rebase(newBaseline, (cur) => {
        if (!field) return newBaseline
        const merged = { ...newBaseline }
        for (const k of ['name', 'description', 'body', 'enabled'] as ContentField[]) {
          if (k !== field && cur[k] !== baseline[k]) Object.assign(merged, { [k]: cur[k] })
        }
        return merged
      })
    } catch (e) {
      window.alert(e instanceof Error ? e.message : '恢复失败')
    } finally {
      setResetting(null)
    }
  }

  // 装配胶囊点击：乐观翻转 → assignSkill 回存（失败回滚并提示）。
  const toggleAssign = (agentCode: string, next: boolean) => {
    if (!draft) return
    const skillCode = draft.code
    const flip = (v: boolean) =>
      setAssignAgents((prev) => prev?.map((a) => (a.code === agentCode ? { ...a, involved: v } : a)) ?? prev)
    setAssignSaving(agentCode)
    flip(next)
    assignSkill(skillCode, agentCode, next)
      .catch(() => {
        if (openCodeRef.current === skillCode) flip(!next)
        window.alert('装配状态保存失败')
      })
      .finally(() => {
        if (openCodeRef.current === skillCode) setAssignSaving(null)
      })
  }

  const isBuiltin = !!detail?.skill.is_builtin
  const status = (field: ContentField, long = false) =>
    detail && (
      <OverrideStatus
        state={detail.overrides[field]}
        current={detail.skill[field]}
        long={long}
        busy={resetting === field}
        format={field === 'enabled' ? (v) => (v ? '启用' : '停用') : undefined}
        onReset={() => void onReset(field)}
      />
    )
  const customizedCount = detail ? Object.keys(detail.overrides).length : 0

  return (
    <>
      <ConfigListShell
        title="技能"
        subtitle="供智能体检索的方法论文档：摘要进索引，全文按需读取。内置技能随版本更新，可自定义"
        loading={list.loading}
        error={list.error}
        empty={rows.length === 0}
        emptyHint={list.query ? '无匹配技能' : '暂无技能'}
        search={{ value: list.query, onChange: list.setQuery, placeholder: '搜索名称 / 标识 / 描述' }}
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
            key={sk.code}
            name={sk.name}
            description={sk.description}
            dimmed={!sk.enabled}
            onClick={() => openSkill(sk.code)}
            right={
              <div className="flex items-center gap-2">
                <Badge variant="outline">{sk.category}</Badge>
                {sk.is_builtin ? <Badge variant="outline">内置</Badge> : <Badge variant="outline">自建</Badge>}
                {sk.customized.length > 0 && <Badge variant="outline">已自定义</Badge>}
                {sk.shadows_builtin && <Badge variant="outline">遮蔽同名内置</Badge>}
                {!sk.enabled && <Badge variant="outline">已停用</Badge>}
              </div>
            }
          />
        ))}
      </ConfigListShell>

      <ConfigDrawer
        open={!!draft}
        title={isNew ? '新建技能' : draft?.name || '技能'}
        onOpenChange={(o) => !o && requestClose()}
        onSave={() => void onSave()}
        onDelete={isNew || isBuiltin ? undefined : () => void onDelete()}
        saving={saving}
        saveDisabled={saving || !!createInvalid || !dirty || resetting !== null}
        tabs={
          draft
            ? [
                {
                  value: 'info',
                  label: '基本信息',
                  content: (
                    <>
                      <Field label="标识符" hint={isNew ? '唯一寻址名：小写字母、数字与连字符，创建后不可修改' : '创建后固定'}>
                        <input
                          className={INPUT_CLASS}
                          value={draft.code}
                          spellCheck={false}
                          disabled={!isNew}
                          onChange={(e) => patch({ code: e.target.value })}
                        />
                      </Field>
                      {detail?.shadows_builtin && (
                        <p className="text-xs text-status-warning">
                          这个自建技能与同名内置技能冲突，内置版本被遮蔽、不生效。
                        </p>
                      )}
                      <Field label="名称" aside={status('name')}>
                        <input className={INPUT_CLASS} value={draft.name} onChange={(e) => patch({ name: e.target.value })} />
                      </Field>
                      <Field label="分类" hint={isBuiltin ? '内置技能的分类随出厂固定' : '知识域：vuln（漏洞知识）/ tool（工具使用知识）'}>
                        <input
                          className={INPUT_CLASS}
                          value={draft.category}
                          disabled={!isNew}
                          onChange={(e) => patch({ category: e.target.value })}
                        />
                      </Field>
                      <Field
                        label="描述"
                        hint="进智能体技能索引的摘要——直接决定 LLM 何时选用本技能"
                        aside={status('description', true)}
                      >
                        <textarea
                          className={TEXTAREA_CLASS + ' min-h-[4rem] resize-y'}
                          value={draft.description}
                          onChange={(e) => patch({ description: e.target.value })}
                        />
                      </Field>
                      {!isNew && (
                        <Field label="启用" hint="停用后不进任何智能体的技能索引" aside={status('enabled')}>
                          <span className="flex items-center gap-2 text-sm text-text">
                            <input
                              type="checkbox"
                              checked={draft.enabled}
                              onChange={(e) => patch({ enabled: e.target.checked })}
                            />
                            {draft.enabled ? '已启用' : '已停用'}
                          </span>
                        </Field>
                      )}
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
                      {!isNew &&
                        (assignAgents === null ? (
                          <p className="text-sm text-text-secondary">装配状态加载中…</p>
                        ) : (
                          <AgentAssignSection agents={assignAgents} savingCode={assignSaving} onToggle={toggleAssign} />
                        ))}
                    </>
                  ),
                },
                {
                  value: 'content',
                  label: '知识内容',
                  content: (
                    <div className="flex min-h-0 flex-1 flex-col gap-2">
                      {status('body', true)}
                      <Field label="技能文档（Markdown）" hint="read_skill 全文读取的方法论正文">
                        <textarea
                          className={TEXTAREA_CLASS + ' min-h-[24rem] resize-y font-mono'}
                          value={draft.body}
                          onChange={(e) => patch({ body: e.target.value })}
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
