import type { ComponentType } from 'react'
import type { TaskSummary } from '@/api/types'
import { NAV_GROUPS } from '@/layout/navigation'
import { conversationPath } from '@/lib/source'
import { scopeQuery, type Scope } from './scope'

/** 任务相关跳转的目标：任务名下的视图。 */
export type JumpTarget = 'task' | 'conversation' | 'findings' | 'exploration' | 'llmAudit'

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
  /** 发现数：「发现」只在有发现时出现；不要「发现」的调用方可不填 */
  findingCount?: number
  /** 从哪里出发：从对话出发时落点按对话限定，其余按任务限定（见 scope.ts） */
  from: 'task' | 'conversation'
  /** 探索图落点打开的节点：从发现出发时是坐实它的结果节点；缺省 = 看整张图 */
  explorationNode?: string
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
const ORDER: readonly JumpTarget[] = ['task', 'conversation', 'findings', 'exploration', 'llmAudit']

const NAV_PATH: Record<JumpTarget, string> = {
  task: '/tasks',
  conversation: '/conversations/direct',
  findings: '/findings',
  exploration: '/exploration-graph',
  llmAudit: '/llm-audit',
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

/**
 * 从一个对话出发的跳转：落点按这个对话限定（对话页顶栏、调用详情都用它）。纯聊天对话没有任务，
 * 只对对话本身成立的目标才可用。
 */
export function conversationJumps(source: string, conversationId: string, targets: readonly JumpTarget[]): Jump[] {
  return taskJumps({ taskId: '', conversationId, source, from: 'conversation' }, targets)
}

/**
 * 任务相关跳转（全站唯一规则）：按 targets 取需要的目标，固定顺序。落点只显示这个任务 / 这个
 * 对话的内容。
 *
 * 不做通向空页面的按钮——一个去处成立的前提在这里判定，调用方不必自己挡：
 *   - 没有任务（纯聊天对话）：任务、发现、探索图都没有；对话与模型日志照有（问答的调用记在对话名下）
 *   - 没有对话不放「对话」；没有发现不放「发现」
 */
export function taskJumps(ctx: TaskJumpContext, targets: readonly JumpTarget[]): Jump[] {
  const hasTask = ctx.taskId !== ''
  const scope: Scope =
    ctx.from === 'conversation' && ctx.conversationId
      ? { kind: 'conversation', id: ctx.conversationId }
      : { kind: 'task', id: ctx.taskId }
  const href: Record<JumpTarget, string | null> = {
    // 任务页：列表收窄到这一个任务并打开它的详情
    task: hasTask ? `/tasks?${scopeQuery(scope)}&selected=${encodeURIComponent(ctx.taskId)}` : null,
    conversation: ctx.conversationId ? conversationPath(ctx.source, ctx.conversationId) : null,
    findings: hasTask && (ctx.findingCount ?? 0) > 0 ? `/findings?${scopeQuery(scope)}` : null,
    exploration: hasTask
      ? `/exploration-graph?${scopeQuery(scope)}${ctx.explorationNode ? `&selected=${encodeURIComponent(ctx.explorationNode)}` : ''}`
      : null,
    // 模型日志：按任务筛是这个任务的智能体运行的调用，按对话筛还包括对话里直接发起的调用
    llmAudit: `/llm-audit?${scopeQuery(scope)}`,
  }
  return ORDER.filter((t) => targets.includes(t)).flatMap((target) => {
    const to = href[target]
    return to ? [jump(target, to)] : []
  })
}
