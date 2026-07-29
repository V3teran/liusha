import type { Message } from '../api/types'
import { classifyMessage, isCollapsibleTool } from './messageKind'

// threadRows：把有序消息流分组成可渲染的「行」——ChatThread（气泡版）与 TimelineThread（轨迹版）
// 共用这份分组逻辑，避免两处渲染各写一遍分歧。
//
// 分组规则：
//   - reasoning(想)/spawn(派发)/spawn-done/finding(漏洞)/user/assistant 各自独立成 msg 行；
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
    if (tag === 'hidden') continue
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
