import type { Message } from '@/api/types'
import { KNOWN_AGENTS } from '@/lib/agentColor'
import { classifyMessage, isCollapsibleTool } from './messageKind'

// threadRows：把有序消息流整理成可渲染的「行」——TimelineThread（活动时间轴）消费这份整理结果，
// 整理逻辑与渲染解耦，便于独立测试。
//
// 行有三种：
//   - divider：按天插入的日期分隔；
//   - msg：user / assistant / reasoning / task_status 各自一行。reasoning 是一个 agent 一轮的终稿，
//     这一轮的工具过程事件（tool-call / tool-result）归在它名下（tools）：一张卡里先工具后结论；
//   - round：终稿还没到的轮，只有工具过程事件——在途，或被中断后再没有终稿。排在全部 msg 行之后。
//
// 工具事件怎样找到自己那一轮：
//   - 带轮次标识（round_id）：与终稿的标识相同，精确归组。同一 agent 会并行跑多轮（执行者的几个
//     动作、评估者的几条验证），只凭先后顺序分不清；
//   - 不带（标识上线之前落库的）：归同一 agent 的下一条终稿。
//
// reasoning 行带 step（本次用户指令内的全局推理步号，每条 user 消息重置——一次指令=一个计数周期）。

export type RoundRow = { kind: 'round'; key: string; agent: string; tools: Message[] }

export type ThreadRow =
  | { kind: 'divider'; key: string; label: string }
  | { kind: 'msg'; key: number; msg: Message; step?: number; tools?: Message[] }
  | RoundRow

// dayKey/dayLabel 由调用方注入（避免 lib 循环依赖 format 的时间格式细节耦合到此纯逻辑）。
export interface ThreadRowDeps {
  dayKey: (iso: string) => string
  dayLabel: (iso: string) => string
}

export function buildThreadRows(messages: Message[], deps: ThreadRowDeps): ThreadRow[] {
  const out: ThreadRow[] = []
  let lastDay = ''
  let step = 0
  // 等终稿来领的工具事件
  const waiting = new Map<string, Message[]>()
  const collect = (key: string, m: Message) => waiting.set(key, [...(waiting.get(key) ?? []), m])
  const take = (key: string): Message[] => {
    const got = waiting.get(key) ?? []
    waiting.delete(key)
    return got
  }

  for (const m of messages) {
    const tag = classifyMessage(m)
    if (isCollapsibleTool(tag)) {
      collect(m.metadata?.round_id || 'agent:' + (m.metadata?.agent_name ?? ''), m)
      continue
    }
    if (m.role === 'user') step = 0 // 用户指令边界：追加 = 新指令 = 新计数周期
    const day = deps.dayKey(m.created_at)
    if (day && day !== lastDay) {
      out.push({ kind: 'divider', key: 'day-' + day, label: deps.dayLabel(m.created_at) })
      lastDay = day
    }
    let stepNo: number | undefined
    let tools: Message[] | undefined
    if (tag === 'reasoning') {
      step += 1
      stepNo = step
      // 终稿先领走不带标识、一直等它的那些，再领走与自己同一轮的
      const own = [...take('agent:' + (m.metadata?.agent_name ?? '')), ...take(m.metadata?.round_id ?? '')]
      if (own.length) tools = own.sort((a, b) => a.seq - b.seq)
    }
    out.push(tools ? { kind: 'msg', key: m.seq, msg: m, step: stepNo, tools } : { kind: 'msg', key: m.seq, msg: m, step: stepNo })
  }

  // 没有终稿来领的轮：按首个事件的出现顺序排在最后
  const open = [...waiting.values()].sort((a, b) => a[0].seq - b[0].seq)
  for (const tools of open) {
    out.push({ kind: 'round', key: roundKey(tools), agent: tools[0].metadata?.agent_name ?? '', tools })
  }
  return out
}

/** 一轮的折叠组的标识：取自这一轮的工具事件本身，这一轮从「在途」变成「已出终稿」时不变——展开状态跟着它走。 */
export function roundKey(tools: Message[]): string {
  return 'tools-' + (tools[0]?.metadata?.round_id || tools[0]?.seq)
}

/**
 * 行的作者：reasoning 归产出它的智能体，聊天回答归 assistant，坐实的发现归坐实它的评估者；
 * 其余（人的消息、任务状态、引导回执）没有作者。
 *
 * 只认这三种而不看一切过程事件：工具事件的 agent_name 是「谁调的」，它们的归属在整理成行时
 * 已经归进各自的那一轮（见 buildThreadRows），不单独成行。
 */
export function authorOf(msg: Message): string {
  const tag = classifyMessage(msg)
  if (tag === 'reasoning' || tag === 'finding') return msg.metadata?.agent_name || ''
  if (tag === 'assistant') return 'assistant'
  return ''
}

/**
 * 时间线的骨架：人的指令、任务的生命周期节点、运行中引导的回执。它们不属于任何一个智能体，
 * 按智能体筛选时留着——读某一个智能体的一段工作时，靠它们知道这些工作是在哪条指令之后、
 * 任务走到了哪一步。
 */
export function isSkeleton(row: ThreadRow): boolean {
  if (row.kind !== 'msg') return false
  const tag = classifyMessage(row.msg)
  return tag === 'user' || tag === 'task-status' || tag === 'steering'
}

/** 只留某个智能体的行（骨架照旧）；后面再没有可见内容的日期分隔一并去掉，不留空的一天。 */
export function filterByAgent(rows: ThreadRow[], agent: string | null): ThreadRow[] {
  if (!agent) return rows
  const visible = rows.map((r) => r.kind === 'divider' || isSkeleton(r) || (r.kind === 'round' ? r.agent : authorOf(r.msg)) === agent)
  let contentFollows = false
  for (let i = rows.length - 1; i >= 0; i--) {
    if (rows[i].kind === 'divider') {
      visible[i] = contentFollows
      contentFollows = false
    } else if (visible[i]) {
      contentFollows = true
    }
  }
  return rows.filter((_, i) => visible[i])
}

export interface AgentActivity {
  name: string
  /** 这个智能体在时间线上的条目数：出过终稿的轮各一条，没有终稿的轮也算，坐实的发现归坐实它的评估者 */
  count: number
}

/**
 * 时间线上出现过的智能体及其条目数（authorOf 的口径）：登记过的身份（与身份配色的登记顺序一致）
 * 在前，其余按首现排在后面。
 */
export function agentActivity(rows: ThreadRow[]): AgentActivity[] {
  const counts = new Map<string, number>()
  for (const r of rows) {
    const name = r.kind === 'round' ? r.agent : r.kind === 'msg' ? authorOf(r.msg) : ''
    if (name) counts.set(name, (counts.get(name) ?? 0) + 1)
  }
  // 不在登记表里的同为一个名次，稳定排序保持它们的首现顺序
  return [...counts].sort(([a], [b]) => knownRank(a) - knownRank(b)).map(([name, count]) => ({ name, count }))
}

function knownRank(name: string): number {
  const i = KNOWN_AGENTS.indexOf(name)
  return i === -1 ? KNOWN_AGENTS.length : i
}

// 配对核心（单一事实源，inFlightToolCalls 与 pairToolResults 共用——两套
// 配对实现会漂移，后端的双路径分裂就是这么来的）：
//   - call_id 配对：同一 (call_id) 的 call/result 精确匹配——并发同名工具
//     乱序完成（call1 call2 result2 result1）时 FIFO 会把 result2 配给 call1；
//   - FIFO 兜底：无 call_id 的历史事件按同 (agent, tool) 队列消耗；
//   - 带 call_id 但对不上号的 result 视为孤儿（其 call 事件已丢，FIFO 会
//     错拿别人的 call）。
interface PairingState {
  matched: Map<number, Message> // call Seq → result 消息
  matchedResultSeqs: Set<number> // 已配对的 result Seq
  orphanResults: Message[] // 配不上 call 的孤立结果
  running: Set<number> // 已见 call 未见 result（执行中/悬挂）
}

function pairCallsAndResults(messages: Message[]): PairingState {
  const fifo = new Map<string, number[]>()
  const byCallID = new Map<string, number>()
  const state: PairingState = {
    matched: new Map(),
    matchedResultSeqs: new Set(),
    orphanResults: [],
    running: new Set(),
  }
  for (const m of messages) {
    const ev = m.metadata
    if (!ev?.tool_name) continue
    const k = `${ev.agent_name}\u0000${ev.tool_name}`
    if (ev.kind === 'tool_call') {
      if (ev.call_id) byCallID.set(ev.call_id, m.seq)
      else {
        const q = fifo.get(k) ?? []
        q.push(m.seq)
        fifo.set(k, q)
      }
      state.running.add(m.seq)
    } else if (ev.kind === 'tool_result') {
      let callSeq: number | undefined
      if (ev.call_id) {
        callSeq = byCallID.get(ev.call_id)
        if (callSeq !== undefined) byCallID.delete(ev.call_id)
      } else {
        const q = fifo.get(k)
        if (q && q.length > 0) {
          callSeq = q.shift()
          fifo.set(k, q)
        }
      }
      if (callSeq !== undefined) {
        state.matched.set(callSeq, m)
        state.matchedResultSeqs.add(m.seq)
        state.running.delete(callSeq)
      } else {
        state.orphanResults.push(m)
      }
    }
  }
  return state
}

// inFlightToolCalls 计算执行中的工具调用：队列残留（已见 call 未见 result）
// 即执行中。任务终态后不再有新 result——呈现层须以任务活跃门控，避免死任务
// 的悬挂 call 永转。
export function inFlightToolCalls(messages: Message[]): Set<number> {
  return pairCallsAndResults(messages).running
}

// ToolPairs 是调用→结果的配对结果：matched 为 call Seq → result 消息；
// orphanResults 是配不上 call 的孤立结果（call 丢失时的展示兜底）；
// matchedResultSeqs 是已配对的 result Seq 集——各折叠组按它把孤儿结果限定在
// 自己的消息区间内渲染（全局列表会让每个折叠组重复渲染同一批孤儿）。
export interface ToolPairs {
  matched: Map<number, Message>
  orphanResults: Message[]
  matchedResultSeqs: Set<number>
}

// pairToolResults 把 tool_call 与 tool_result 配成「一动作一卡」（配对口径
// 见 pairCallsAndResults）。
export function pairToolResults(messages: Message[]): ToolPairs {
  const { matched, matchedResultSeqs, orphanResults } = pairCallsAndResults(messages)
  return { matched, matchedResultSeqs, orphanResults }
}
