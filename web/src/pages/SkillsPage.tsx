import { useMemo, useState } from 'react'
import { listSkillsPaged } from '@/api/client'
import { createSkill, updateSkill, deleteSkill, type SkillChanges } from '@/api/config'
import type { Skill } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { ConfigListShell, ConfigRow } from '@/features/config/ConfigListShell'
import { usePagedList } from '@/features/config/usePagedList'
import { ConfigDrawer, Field, INPUT_CLASS } from '@/features/config/ConfigDrawer'

const fetchSkills = async (page: number, size: number, q: string) => {
  const res = await listSkillsPaged(page, size, q)
  return { items: res.skills, total: res.total }
}

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

// 取数全权交给 usePagedList（内部含防抖 + 竞态丢弃）；页面不得再自行 reload——
// 在 effect 里盯 query 逐键 reload 会绕过防抖、挂载时双请求（历史缺陷）。
export default function SkillsPage() {
  const list = usePagedList(fetchSkills)
  const rows = (list.rows as Skill[]) || []

  const [draft, setDraft] = useState<Skill | null>(null)
  // 新建模式（无 id 走 POST）；编辑模式按 code 走 PUT 部分更新。
  const [isNew, setIsNew] = useState(false)
  // 打开抽屉时的原始快照：dirty 判断基准（patch 全为不可变更新）。
  const [baseline, setBaseline] = useState<Skill | null>(null)
  const [saving, setSaving] = useState(false)

  const patch = (p: Partial<Skill>) => setDraft((d) => (d ? { ...d, ...p } : d))

  const openDraft = (sk: Skill) => {
    setDraft(sk)
    setBaseline(sk)
    setIsNew(false)
  }

  const openNew = () => {
    setDraft(EMPTY_DRAFT)
    setBaseline(EMPTY_DRAFT)
    setIsNew(true)
  }

  const closeDraft = () => {
    setDraft(null)
    setBaseline(null)
    setIsNew(false)
  }

  const requestClose = () => {
    if (dirty && !window.confirm('有未保存的更改，确认丢弃？')) return
    closeDraft()
  }

  // 编辑能力分界：内置技能是 skills/*.md 种子的镜像（reseed 覆盖 name/description/
  // body/category），编辑它们是无效功——只放开「启用」运维开关；自建技能全字段可编辑。
  const builtinLocked = !isNew && !!draft?.is_builtin

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
        onDelete={isNew || builtinLocked ? undefined : () => void onDelete()}
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
                          disabled={builtinLocked}
                          onChange={(e) => patch({ name: e.target.value })}
                        />
                      </Field>
                      <Field label="分类" hint="tooling / vuln 等目录归类">
                        <input
                          className={INPUT_CLASS}
                          value={draft.category}
                          disabled={builtinLocked}
                          onChange={(e) => patch({ category: e.target.value })}
                        />
                      </Field>
                      <Field label="描述" hint="多智能体按需读取时可见的技能摘要">
                        <textarea
                          className={INPUT_CLASS + ' min-h-[4rem] resize-y'}
                          value={draft.description}
                          disabled={builtinLocked}
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
                    </>
                  ),
                },
                {
                  value: 'content',
                  label: '知识内容',
                  content: (
                    <Field
                      label="技能文档（Markdown）"
                      hint={
                        builtinLocked
                          ? '内置技能由 skills/ 目录种子管理（reseed 覆盖），如需修改请改文件'
                          : '渐进式加载的领域知识：Tier 1 索引进 system prompt，运行时经 read_skill 全文读取'
                      }
                    >
                      <textarea
                        className={INPUT_CLASS + ' min-h-[26rem] resize-y font-mono text-sm'}
                        value={draft.body}
                        disabled={builtinLocked}
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
