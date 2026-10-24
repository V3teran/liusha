import { useEffect, useState } from 'react'
import { listSkillsPaged } from '@/api/client'
import type { Skill } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { ConfigListShell, ConfigRow } from '@/features/config/ConfigListShell'
import { usePagedList } from '@/features/config/usePagedList'
import { ConfigDrawer, Field, INPUT_CLASS } from '@/features/config/ConfigDrawer'

const fetchSkills = async (page: number, size: number, q: string) => {
  const res = await listSkillsPaged(page, size, q)
  return { items: res.skills, total: res.total }
}

export default function SkillsPage() {
  const list = usePagedList(fetchSkills)
  const [draft, setDraft] = useState<Skill | null>(null)

  useEffect(() => {
    void list.reload()
  }, [list.page, list.size, list.query])

  const { loading, error } = list
  const rows = (list.rows as Skill[]) || []

  return (
    <>
      <ConfigListShell
        title="技能知识库"
        subtitle="领域知识与方法论，智能体可通过 read_skill 按需读取"
        loading={loading}
        error={error}
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
      >
        {rows.map((sk) => (
          <ConfigRow
            key={sk.id}
            name={sk.name}
            description={sk.description}
            dimmed={!sk.enabled}
            onClick={() => setDraft(sk)}
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
        title={draft?.name || '技能详情'}
        onOpenChange={(o) => !o && setDraft(null)}
        onSave={() => {}}
        tabs={
          draft
            ? [
                {
                  value: 'info',
                  label: '基本信息',
                  content: (
                    <>
                      <Field label="标识符">
                        <input
                          className={INPUT_CLASS}
                          value={draft.code}
                          disabled
                        />
                      </Field>
                      <Field label="名称">
                        <input
                          className={INPUT_CLASS}
                          value={draft.name}
                          disabled
                        />
                      </Field>
                      <Field label="分类">
                        <input
                          className={INPUT_CLASS}
                          value={draft.category}
                          disabled
                        />
                      </Field>
                      <Field label="描述">
                        <textarea
                          className={INPUT_CLASS + ' min-h-[6rem] resize-y'}
                          value={draft.description}
                          disabled
                        />
                      </Field>
                      <div className="flex items-center gap-4 text-[13px] text-muted-foreground">
                        <label className="flex items-center gap-2">
                          <input
                            type="checkbox"
                            checked={draft.is_builtin}
                            disabled
                          />
                          内置技能
                        </label>
                        <label className="flex items-center gap-2">
                          <input
                            type="checkbox"
                            checked={draft.enabled}
                            disabled
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
                    <Field label="技能文档（Markdown）">
                      <textarea
                        className={INPUT_CLASS + ' min-h-[32rem] resize-y font-mono text-[13px]'}
                        value={draft.body}
                        disabled
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
