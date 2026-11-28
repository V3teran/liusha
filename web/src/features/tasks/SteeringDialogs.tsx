import { useId, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { adjustTaskGoal, assignTaskAction } from '@/api/tasks'
import { errorText } from '@/api/client'
import type { TaskSummary } from '@/api/types'
import { FormDialog } from '@/components/ui/FormDialog'
import { FIELD_LABEL_CLASS, TEXTAREA_CLASS } from '@/components/ui/control'
import { cn } from '@/lib/utils'

// 运行中引导的两个任务级入口（调整目标 / 指派动作）。弹层只在打开时挂载（每次打开都是
// 新表单，未提交的草稿不跨开关保留）。成功后失效任务与探索图查询：详情板、列表与图随之
// 刷新；时间线回执经 SSE 到达。

/** 引导写入影响的查询：任务详情与列表、探索图。 */
const STEERING_AFFECTED = [['tasks'], ['exploration']] as const

function useSteeringMutation<T>(fn: () => Promise<T>, onDone: () => void) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: fn,
    onSuccess: () => {
      for (const key of STEERING_AFFECTED) void qc.invalidateQueries({ queryKey: key })
      onDone()
    },
  })
}

interface SteeringDialogProps {
  task: TaskSummary
  open: boolean
  onOpenChange: (open: boolean) => void
}

/** 调整目标：预填当前目标；未开始的规划动作撤回并按新目标重新规划。 */
export function AdjustGoalDialog({ task, open, onOpenChange }: SteeringDialogProps) {
  const fieldId = useId()
  const [goal, setGoal] = useState(task.goal)
  const mutation = useSteeringMutation(() => adjustTaskGoal(task.id, goal.trim()), () => onOpenChange(false))
  const trimmed = goal.trim()
  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title="调整目标"
      description="未开始的规划动作将撤回并按新目标重新规划；执行中的动作由监察者立即复核，越界的会被否决，你也可以在探索图里直接否决。"
      submitLabel="保存目标"
      canSubmit={trimmed !== '' && trimmed !== task.goal}
      pending={mutation.isPending}
      error={mutation.isError ? errorText(mutation.error, '调整目标失败') : undefined}
      onSubmit={() => mutation.mutate()}
    >
      <div>
        <label htmlFor={fieldId} className={cn(FIELD_LABEL_CLASS, 'mb-1.5 block')}>
          任务目标
        </label>
        <textarea
          id={fieldId}
          value={goal}
          onChange={(e) => setGoal(e.target.value)}
          rows={8}
          className={cn(TEXTAREA_CLASS, 'min-h-[160px] resize-y')}
        />
      </div>
    </FormDialog>
  )
}

/** 指派动作：用户直接要求执行的一步，先于规划者排出的动作被执行。 */
export function AssignActionDialog({ task, open, onOpenChange }: SteeringDialogProps) {
  const instructionId = useId()
  const [instruction, setInstruction] = useState('')
  const mutation = useSteeringMutation(
    () => assignTaskAction(task.id, instruction.trim()),
    () => onOpenChange(false),
  )
  return (
    <FormDialog
      open={open}
      onOpenChange={onOpenChange}
      title="指派动作"
      description="只按指令执行这一步、不扩大范围，监察者不会否决它；先于规划者排出的动作被执行。"
      submitLabel="指派"
      canSubmit={instruction.trim() !== ''}
      pending={mutation.isPending}
      error={mutation.isError ? errorText(mutation.error, '指派动作失败') : undefined}
      onSubmit={() => mutation.mutate()}
    >
      <div>
        <label htmlFor={instructionId} className={cn(FIELD_LABEL_CLASS, 'mb-1.5 block')}>
          指令
        </label>
        <textarea
          id={instructionId}
          value={instruction}
          onChange={(e) => setInstruction(e.target.value)}
          rows={4}
          placeholder="如：用 admin/password 登录后，复测 /vulnerabilities/sqli/ 的 id 参数"
          className={cn(TEXTAREA_CLASS, 'resize-y')}
        />
      </div>
    </FormDialog>
  )
}
