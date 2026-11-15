import { useTaskDetail } from '@/features/tasks/useTaskQueries'
import { useConversation } from '@/features/conversation/useConversationQueries'
import type { Scope } from './scope'

/** 单行显示：目标、标题里的换行与连续空白折成一个空格。 */
function oneLine(s: string): string {
  return s.replace(/\s+/g, ' ').trim()
}

/**
 * 范围的显示名：「任务：目标」或「对话：标题」（对话没有标题时显示 ID）。名字还没取到时
 * 返回 undefined，调用方显示占位。
 */
export function useScopeLabel(scope: Scope | null): string | undefined {
  const task = useTaskDetail(scope?.kind === 'task' ? scope.id : null).data
  const conversation = useConversation(scope?.kind === 'conversation' ? scope.id : '').data
  if (scope?.kind === 'task' && task) return `任务：${oneLine(task.goal)}`
  if (scope?.kind === 'conversation' && conversation) return `对话：${oneLine(conversation.title) || conversation.id}`
  return undefined
}
