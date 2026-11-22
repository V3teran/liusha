import { useId, useState } from 'react'
import { ApiError, errorText } from '@/api/client'
import type { CorpusDraft } from '@/api/corpus'
import { ConfirmDialog } from '@/components/ui/ConfirmDialog'
import { FormDialog } from '@/components/ui/FormDialog'
import { CorpusScopeSelect } from './CorpusScopeSelect'
import { CorpusTextFields, draftValid } from './CorpusTextFields'
import { useCreateCorpusEntry } from './useCorpusQueries'

interface CorpusCreateDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** 录入成功或要查看与之重复的已有条目时打开它 */
  onOpenEntry: (id: string) => void
}

const EMPTY: CorpusDraft = { title: '', content: '', scenario: null }

/** 正文与已有条目重复时，409 错误体带出已有条目的 ID。 */
function duplicateOf(e: unknown): string {
  return e instanceof ApiError && e.status === 409 && typeof e.body.existing_id === 'string' ? e.body.existing_id : ''
}

export function CorpusCreateDialog({ open, onOpenChange, onOpenEntry }: CorpusCreateDialogProps) {
  const [draft, setDraft] = useState<CorpusDraft>(EMPTY)
  const [confirmDiscard, setConfirmDiscard] = useState(false)
  const create = useCreateCorpusEntry()
  const scopeId = useId()
  const existing = duplicateOf(create.error)
  const typed = draft.title.trim() !== '' || draft.content.trim() !== ''

  const close = () => {
    setDraft(EMPTY)
    create.reset()
    onOpenChange(false)
  }

  return (
    <>
      <FormDialog
        open={open}
        // 已经写了内容时关闭要先确认（Esc、点遮罩都会触发关闭）
        onOpenChange={(next) => (next ? onOpenChange(true) : typed ? setConfirmDiscard(true) : close())}
        title="录入知识"
        submitLabel="录入"
        canSubmit={draftValid(draft)}
        pending={create.isPending}
        error={create.isError ? errorText(create.error, '录入失败') : undefined}
        onSubmit={() =>
          create.mutate(draft, {
            onSuccess: (entry) => {
              close()
              onOpenEntry(entry.id)
            },
          })
        }
      >
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
        <CorpusTextFields value={draft} onChange={setDraft} autoFocus />
        {existing && (
          <button
            type="button"
            className="self-start text-xs text-accent underline-offset-2 hover:underline"
            onClick={() => {
              close()
              onOpenEntry(existing)
            }}
          >
            查看已有条目
          </button>
        )}
      </FormDialog>
      <ConfirmDialog
        open={confirmDiscard}
        onOpenChange={setConfirmDiscard}
        danger
        title="放弃录入"
        message="已填写的内容放弃后不可恢复。"
        confirmLabel="确认放弃"
        onConfirm={close}
      />
    </>
  )
}
