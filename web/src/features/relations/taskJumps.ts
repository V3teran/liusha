import type { ComponentType } from 'react'
import type { TaskSummary } from '@/api/types'
import { NAV_GROUPS } from '@/layout/navigation'
import { conversationPath } from '@/lib/source'
import { scopeQuery, type Scope } from './scope'

/** 任务相关跳转的目标：任务名下的视图。 */
export type JumpTarget = 'task' | 'conversation' | 'findings' | 'exploration'

/** 一个跳转按钮：名字与图标取侧栏导航（同一个目的地的两种入口），to 已按范围限定。 */
export interface Jump {
  target: JumpTarget
  label: string
  icon: ComponentType<{ className?: string }>
  to: string
}

/** 生成跳转所需的任务信息。 */
export interface TaskJumpContext {
  taskId: string
  /** 任务的对话；空 = 没有对话（对话已删除，或代理捕获建对话失败） */
  conversationId: string
  /** 下发来源：决定对话页的标签页 */
  source: string
  /** 漏洞数：「漏洞」只在有漏洞时出现；不要「漏洞」的调用方可不填 */
  findingCount?: number
  /** 从哪里出发：从对话出发时落点按对话限定，其余按任务限定（见 scope.ts） */
  from: 'task' | 'conversation'
}

/** 从任务摘要取跳转所需的信息。 */
export function jumpContext(task: TaskSummary, from: TaskJumpContext['from']): TaskJumpContext {
  return {
    taskId: task.id,
    conversationId: task.conversation_id,
    source: task.source,
    findingCount: task.finding_count,
    from,
  }
}

/** 目标的固定顺序与侧栏导航一致。 */
const ORDER: readonly JumpTarget[] = ['task', 'conversation', 'findings', 'exploration']

const NAV_PATH: Record<JumpTarget, string> = {
  task: '/tasks',
  conversation: '/conversations/direct',
  findings: '/findings',
  exploration: '/exploration-graph',
}

function navItem(target: JumpTarget) {
  const item = NAV_GROUPS.flatMap((g) => g.items).find((i) => i.to === NAV_PATH[target])
  if (!item) throw new Error(`侧栏导航缺少 ${NAV_PATH[target]}`)
  return item
}

function jump(target: JumpTarget, to: string): Jump {
  const { label, icon } = navItem(target)
  return { target, label, icon, to }
}

/** 跳到一个对话（没有任务的对话内记录，如问答类 LLM 调用，只有这一个去处）。 */
export function conversationJump(source: string, conversationId: string): Jump {
  return jump('conversation', conversationPath(source, conversationId))
}

/**
 * 任务相关跳转（全站唯一规则）：按 targets 取需要的目标，固定顺序；没有对话不放「对话」，
 * 没有漏洞不放「漏洞」（不做通向空页面的按钮）。落点只显示这个任务 / 这个对话的内容。
 */
export function taskJumps(ctx: TaskJumpContext, targets: readonly JumpTarget[]): Jump[] {
  const scope: Scope =
    ctx.from === 'conversation' && ctx.conversationId
      ? { kind: 'conversation', id: ctx.conversationId }
      : { kind: 'task', id: ctx.taskId }
  const href: Record<JumpTarget, string | null> = {
    // 任务页：列表收窄到这一个任务并打开它的详情
    task: `/tasks?${scopeQuery(scope)}&selected=${encodeURIComponent(ctx.taskId)}`,
    conversation: ctx.conversationId ? conversationPath(ctx.source, ctx.conversationId) : null,
    findings: (ctx.findingCount ?? 0) > 0 ? `/findings?${scopeQuery(scope)}` : null,
    exploration: `/exploration-graph?${scopeQuery(scope)}`,
  }
  return ORDER.filter((t) => targets.includes(t)).flatMap((target) => {
    const to = href[target]
    return to ? [jump(target, to)] : []
  })
}
