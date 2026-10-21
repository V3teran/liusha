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
      out.push({ kind: 'tools', key: 'tools-' + bucket[0].Seq, tools: bucket })
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
    if (m.Role === 'user') step = 0 // 用户指令边界：追加 = 新指令 = 新计数周期
    const day = deps.dayKey(m.CreatedAt)
    if (day && day !== lastDay) {
      out.push({ kind: 'divider', key: 'day-' + day, label: deps.dayLabel(m.CreatedAt) })
      lastDay = day
    }
    let stepNo: number | undefined
    if (tag === 'reasoning') {
      step += 1
      stepNo = step
    }
    out.push({ kind: 'msg', key: m.Seq, msg: m, step: stepNo })
  }
  flush()
  return out
}

// inFlightToolCalls 计算执行中的工具调用：同 (agent, tool) 的 tool_call 先进先出
// 配对 tool_result，队列残留（已见 call 未见 result）即执行中。executor 并发执行
// 同名工具时交错到达（call call result result），FIFO 配对保持一对一消耗。
// 任务终态后不再有新 result——呈现层须以任务活跃门控，避免死任务的悬挂 call 永转。
export function inFlightToolCalls(messages: Message[]): Set<number> {
  const queues = new Map<string, number[]>()
  const running = new Set<number>()
  for (const m of messages) {
    const ev = m.Metadata
    if (!ev?.tool_name) continue
    const k = `${ev.agent_name}\u0000${ev.tool_name}`
    if (ev.kind === 'tool_call') {
      const q = queues.get(k) ?? []
      q.push(m.Seq)
      queues.set(k, q)
      running.add(m.Seq)
    } else if (ev.kind === 'tool_result') {
      const seq = queues.get(k)?.shift()
      if (seq !== undefined) running.delete(seq)
    }
  }
  return running
}
