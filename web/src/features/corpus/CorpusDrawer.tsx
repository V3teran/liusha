import { useEffect, useId, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { Pencil, Trash2, X } from 'lucide-react'
import { errorText } from '@/api/client'
import type { CorpusDraft, CorpusEntry } from '@/api/corpus'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { JumpLinks } from '@/features/relations/TaskJumpLinks'
import { taskJumps } from '@/features/relations/taskJumps'
import { corpusScopeLabel, corpusSourceColor, corpusSourceLabel } from '@/lib/corpus'
import { fullTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import { CorpusScopeSelect } from './CorpusScopeSelect'
import { CorpusTextFields, draftValid } from './CorpusTextFields'
import { useCorpusDetail, useDeleteCorpusEntry, useUpdateCorpusEntry } from './useCorpusQueries'

interface CorpusDrawerProps {
  /** 打开的条目 ID；空 = 关闭 */
  id: string
  /** 列表里已有的行，详情取回前先撑住 */
  placeholder: CorpusEntry | null
  /** 语义检索启用时才标注「待建索引」（字面检索不需要向量） */
  semantic: boolean
  onClose: () => void
}

/** 编辑态的草稿与原值比较出的改动（只提交改了的字段）。 */
function changesOf(entry: CorpusEntry, d: CorpusDraft): Partial<CorpusDraft> {
  const out: Partial<CorpusDraft> = {}
  if (d.title.trim() !== entry.title) out.title = d.title
  if (d.content.trim() !== entry.content) out.content = d.content
  if (d.scenario !== (entry.scenario?.code ?? null)) out.scenario = d.scenario
  return out
}

function draftOf(e: CorpusEntry): CorpusDraft {
  return { title: e.title, content: e.content, scenario: e.scenario?.code ?? null }
}

export function CorpusDrawer({ id, placeholder, semantic, onClose }: CorpusDrawerProps) {
  const detail = useCorpusDetail(id)
  const entry = detail.data?.entry ?? placeholder
  const confirmations = detail.data?.confirmations ?? []
  const update = useUpdateCorpusEntry()
  const remove = useDeleteCorpusEntry()
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState<CorpusDraft>({ title: '', content: '', scenario: null })
  const [confirmDelete, setConfirmDelete] = useState(false)
  const scopeId = useId()

  // 换了条目就退出编辑（草稿属于上一条）
  useEffect(() => {
    setEditing(false)
    update.reset()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 只随条目切换
  }, [id])

  const startEdit = () => {
    if (!entry) return
    setDraft(draftOf(entry))
    update.reset()
    setEditing(true)
  }

  const changes = entry ? changesOf(entry, draft) : {}
  const textChanged = changes.title !== undefined || changes.content !== undefined
  const canSave = Object.keys(changes).length > 0 && draftValid(draft)

  const save = () => {
    if (!entry || !canSave) return
    update.mutate({ id: entry.id, changes }, { onSuccess: () => setEditing(false) })
  }

  return (
    <Dialog.Root open={id !== ''} onOpenChange={(open) => !open && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="app-overlay" />
        <Dialog.Content
          className={cn(
            'tac-modal fixed inset-y-0 right-0 flex h-full w-full flex-col sm:max-w-[640px]',
            'border-border bg-surface shadow-2xl sm:rounded-l-2xl sm:border-l',
          )}
        >
          <div className="flex items-start justify-between gap-4 border-b border-border px-6 py-5">
            <div className="min-w-0 flex-1">
              {entry && (
                <div className="mb-2 flex flex-wrap items-center gap-2 text-xs">
                  <Badge dot={false} size="md" color={corpusSourceColor(entry.source)}>
                    {corpusSourceLabel(entry.source)}
                  </Badge>
                  <span className="text-text-secondary">{corpusScopeLabel(entry.scenario)}</span>
                  {semantic && !entry.indexed && <span className="text-text-tertiary">· 待建索引</span>}
                </div>
              )}
              <Dialog.Title className="text-lg font-semibold text-text">{entry?.title ?? '知识'}</Dialog.Title>
              <Dialog.Description className="sr-only">知识条目详情</Dialog.Description>
            </div>
            <Dialog.Close
              aria-label="关闭"
              className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg text-text-secondary transition-colors hover:bg-surface-2 hover:text-text"
            >
              <X className="h-5 w-5" />
            </Dialog.Close>
          </div>

          <div className="flex-1 overflow-y-auto px-6 py-5">
            {detail.isError && !entry ? (
              <p role="alert" className="text-sm text-sev-critical">
                ⚠ {errorText(detail.error, '加载失败')}
              </p>
            ) : !entry ? (
              <p className="text-sm text-text-tertiary">加载中…</p>
            ) : editing ? (
              <div className="flex flex-col gap-4">
                <div className="flex flex-col gap-1.5">
                  <label htmlFor={scopeId} className="text-xs font-medium text-text-secondary">
                    适用范围
                  </label>
                  <CorpusScopeSelect
                    id={scopeId}
                    value={draft.scenario}
                    onChange={(scenario) => setDraft({ ...draft, scenario })}
                  />
                </div>
                <CorpusTextFields value={draft} onChange={setDraft} contentRows={14} />
              </div>
            ) : (
              <div className="flex flex-col gap-8">
                <p className="whitespace-pre-wrap break-words text-sm leading-relaxed text-text">{entry.content}</p>

                <section aria-labelledby={`${scopeId}-confirmations`}>
                  <h3 id={`${scopeId}-confirmations`} className="mb-3 text-sm font-semibold text-text">
                    印证 <span className="font-mono text-xs font-normal text-text-tertiary">{confirmations.length}</span>
                  </h3>
                  {confirmations.length === 0 ? (
                    <p className="text-xs text-text-tertiary">
                      {entry.source === 'expert' ? '尚无任务印证这条人工内容。' : '沉淀它的任务已被删除。'}
                    </p>
                  ) : (
                    <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border">
                      {confirmations.map((c) => (
                        <li key={c.task_id} className="flex items-center gap-3 px-3 py-2.5">
                          <div className="min-w-0 flex-1">
                            <p className="truncate text-sm text-text" title={c.task_goal}>
                              {c.task_goal.replace(/\s+/g, ' ')}
                            </p>
                            <p className="font-mono text-[11px] text-text-tertiary">{fullTime(c.confirmed_at)}</p>
                          </div>
                          <JumpLinks
                            size="sm"
                            onNavigate={onClose}
                            jumps={taskJumps({ taskId: c.task_id, conversationId: '', source: '', from: 'task' }, ['task'])}
                          />
                        </li>
                      ))}
                    </ul>
                  )}
                </section>

                <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-xs">
                  <dt className="text-text-tertiary">创建</dt>
                  <dd className="font-mono text-text-secondary">{fullTime(entry.created_at)}</dd>
                  <dt className="text-text-tertiary">更新</dt>
                  <dd className="font-mono text-text-secondary">{fullTime(entry.updated_at)}</dd>
                </dl>
              </div>
            )}
          </div>

          {entry && (
            <div className="flex flex-wrap items-center gap-2.5 border-t border-border bg-surface-2/50 px-6 py-3.5">
              {editing ? (
                <>
                  {update.isError ? (
                    <p role="alert" className="mr-auto text-xs text-sev-critical">
                      ⚠ {errorText(update.error, '保存失败')}
                    </p>
                  ) : entry.source === 'agent' && textChanged ? (
                    <p className="mr-auto text-xs text-text-secondary">保存后成为人工内容，任务收尾不再改写它</p>
                  ) : (
                    <span className="mr-auto" />
                  )}
                  <Button variant="secondary" disabled={update.isPending} onClick={() => setEditing(false)}>
                    取消
                  </Button>
                  <Button disabled={!canSave || update.isPending} onClick={save}>
                    {update.isPending ? '保存中…' : '保存'}
                  </Button>
                </>
              ) : (
                <>
                  {remove.isError && (
                    <p role="alert" className="text-xs text-sev-critical">
                      ⚠ {errorText(remove.error, '删除失败')}
                    </p>
                  )}
                  <Button variant="danger" className="mr-auto" onClick={() => setConfirmDelete(true)}>
                    <Trash2 className="h-4 w-4" />
                    删除
                  </Button>
                  <Button variant="secondary" onClick={startEdit}>
                    <Pencil className="h-4 w-4" />
                    编辑
                  </Button>
                </>
              )}
            </div>
          )}

          <ConfirmDialog
            open={confirmDelete}
            onOpenChange={setConfirmDelete}
            danger
            title="删除知识"
            message={`「${entry?.title ?? ''}」删除后不可恢复，印证记录一并删除。`}
            confirmLabel="确认删除"
            onConfirm={() => entry && remove.mutate(entry.id, { onSuccess: onClose })}
          />
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
