import { useId, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { rejectTaskAction } from '@/api/tasks'
import { errorText } from '@/api/client'
import type { GraphNode } from '@/api/explorationgraph'
import { FormDialog } from '@/components/ui/FormDialog'
import { FIELD_LABEL_CLASS, TEXTAREA_CLASS } from '@/components/ui/control'
import { cn } from '@/lib/utils'

interface RejectActionDialogProps {
  action: GraphNode
  onOpenChange: (open: boolean) => void
}

/** 否决动作（危险操作，必须写明原因）：只在打开时挂载。成功后失效探索图与任务查询。 */
export function RejectActionDialog({ action, onOpenChange }: RejectActionDialogProps) {
  const fieldId = useId()
  const [reason, setReason] = useState('')
  const qc = useQueryClient()
  const mutation = useMutation({
    mutationFn: () => rejectTaskAction(action.task_id, action.id, reason.trim()),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['exploration'] })
      void qc.invalidateQueries({ queryKey: ['tasks'] })
      onOpenChange(false)
    },
  })
  return (
    <FormDialog
      open
      onOpenChange={onOpenChange}
      title="否决动作"
      danger
      description={
        action.state === 'running'
          ? '执行中的动作会立即中断，已产出的观察保留；规划者不会再提出等效的动作。'
          : '这一步不会再执行；规划者不会再提出等效的动作。'
      }
      submitLabel="确认否决"
      canSubmit={reason.trim() !== ''}
      pending={mutation.isPending}
      error={mutation.isError ? errorText(mutation.error, '否决失败') : undefined}
      onSubmit={() => mutation.mutate()}
    >
      <div>
        <label htmlFor={fieldId} className={cn(FIELD_LABEL_CLASS, 'mb-1.5 block')}>
          否决原因
        </label>
        <textarea
          id={fieldId}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          rows={3}
          placeholder="如：超出授权范围 / 与已完成的工作重复"
          className={cn(TEXTAREA_CLASS, 'resize-y')}
        />
      </div>
    </FormDialog>
  )
}
