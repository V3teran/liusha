import { JumpLink } from '@/components/JumpLink'
import { useTaskDetail } from '@/features/tasks/useTaskQueries'
import { jumpContext, taskJumps, type Jump, type JumpTarget, type TaskJumpContext } from './taskJumps'

/** 一组跳转按钮（详情面板、抽屉、顶栏的统一视觉）；没有可跳的目标时不渲染。 */
export function JumpLinks({ jumps, onNavigate }: { jumps: Jump[]; onNavigate?: () => void }) {
  if (jumps.length === 0) return null
  return (
    <div className="flex flex-wrap gap-2">
      {jumps.map((j) => (
        <JumpLink key={j.target} to={j.to} icon={j.icon} onClick={onNavigate}>
          {j.label}
        </JumpLink>
      ))}
    </div>
  )
}

interface TaskJumpLinksProps {
  taskId: string
  targets: readonly JumpTarget[]
  /** 从哪里出发：对话出发时落点按对话限定，缺省按任务限定（见 scope.ts） */
  from?: TaskJumpContext['from']
  /** 点击后的回调（抽屉里用来收起抽屉） */
  onNavigate?: () => void
}

/**
 * 只知道任务 ID 时的跳转按钮组（漏洞、流量、LLM 调用、探索图）：对话、漏洞数与来源取任务
 * 摘要（与任务详情共用缓存），摘要还没取到时不渲染。
 */
export function TaskJumpLinks({ taskId, targets, from = 'task', onNavigate }: TaskJumpLinksProps) {
  const task = useTaskDetail(taskId).data
  if (!task) return null
  return <JumpLinks jumps={taskJumps(jumpContext(task, from), targets)} onNavigate={onNavigate} />
}
