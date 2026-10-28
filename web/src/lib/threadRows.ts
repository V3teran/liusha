import type { Message } from '../api/types'
import { classifyMessage, isCollapsibleTool } from './messageKind'

// threadRows：把有序消息流分组成可渲染的「行」——TimelineThread（活动时间轴）消费这份分组，
// 分组逻辑与渲染解耦，便于独立测试。
//
// 分组规则：
//   - reasoning(想)/user/assistant/task_status 各自独立成 msg 行；
//   - 紧随其后的普通工具调用(tool-call/tool-result)累积成一个 tools 折叠组（遇非工具消息 flush）；
//   - 按天插入 divider；
//   - reasoning 行带 step（本次用户指令内的全局推理步号，每条 user 消息重置——一次指令=一个计数周期）。

export type ThreadRow =
  | { kind: 'divider'; key: string; label: string }
  | { kind: 'msg'; key: number; msg: Message; step?: number }
  | { kind: 'tools'; key: string; tools: Message[] }

// dayKey/dayLabel 由调用方注入（避免 lib 循环依赖 format 的时间格式细节耦合到此纯逻辑）。
export interface ThreadRowDeps {
  dayKey: (iso: string) => string
  dayLabel: (iso: string) => string
}

export function buildThreadRows(messages: Message[], deps: ThreadRowDeps): ThreadRow[] {
  const out: ThreadRow[] = []
  let lastDay = ''
  let step = 0
  let bucket: Message[] = []
  const flush = () => {
    if (bucket.length) {
      out.push({ kind: 'tools', key: 'tools-' + bucket[0].seq, tools: bucket })
      bucket = []
    }
  }
  for (const m of messages) {
    const tag = classifyMessage(m)
    if (isCollapsibleTool(tag)) {
      bucket.push(m)
      continue
    }
    flush()
    if (m.role === 'user') step = 0 // 用户指令边界：追加 = 新指令 = 新计数周期
    const day = deps.dayKey(m.created_at)
    if (day && day !== lastDay) {
      out.push({ kind: 'divider', key: 'day-' + day, label: deps.dayLabel(m.created_at) })
      lastDay = day
    }
    let stepNo: number | undefined
    if (tag === 'reasoning') {
      step += 1
      stepNo = step
    }
    out.push({ kind: 'msg', key: m.seq, msg: m, step: stepNo })
  }
  flush()
  return out
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
// matchedResultSeqs 是已配对的 result Seq 集——各组按它把孤儿结果限定在
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
