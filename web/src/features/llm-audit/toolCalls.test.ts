import { describe, expect, test } from 'vitest'
import { prettyArgs, toToolCalls } from './toolCalls'

describe('prettyArgs', () => {
  test('把 JSON 字符串参数美化成缩进格式', () => {
    expect(prettyArgs('{"cmd":"ls","timeout":30}')).toBe('{\n  "cmd": "ls",\n  "timeout": 30\n}')
  })

  test('空对象与空串归一成空串（不展示"无意义的 {}"）', () => {
    expect(prettyArgs('{}')).toBe('')
    expect(prettyArgs('')).toBe('')
    expect(prettyArgs('  ')).toBe('')
  })

  test('非法 JSON 原样保留，不吞内容', () => {
    expect(prettyArgs('{不是合法json')).toBe('{不是合法json')
  })

  test('已是对象（非字符串）直接序列化', () => {
    expect(prettyArgs({ a: 1 })).toBe('{\n  "a": 1\n}')
  })

  test('null/undefined 返回空串', () => {
    expect(prettyArgs(null)).toBe('')
    expect(prettyArgs(undefined)).toBe('')
  })
})

describe('toToolCalls', () => {
  test('解析审计里的 tool_calls 数组（llm.ToolCall：arguments 为 JSON 值）', () => {
    const calls = toToolCalls([{ id: 'call_1', name: 'run_command', arguments: { cmd: 'id' } }])
    expect(calls).toHaveLength(1)
    expect(calls[0].id).toBe('call_1')
    expect(calls[0].name).toBe('run_command')
    expect(calls[0].args).toBe('{\n  "cmd": "id"\n}')
  })

  test('缺字段兜底占位', () => {
    const calls = toToolCalls([{}])
    expect(calls[0]).toEqual({ id: 'call-0', name: '(未命名工具)', args: '' })
  })

  test('null 返回空数组', () => {
    expect(toToolCalls(null)).toEqual([])
    expect(toToolCalls(undefined)).toEqual([])
  })

  test('缺 id/name 时兜底占位，不抛错', () => {
    const calls = toToolCalls([{ arguments: {} }])
    expect(calls[0].id).toBe('call-0')
    expect(calls[0].name).toBe('(未命名工具)')
  })

  test('多个工具调用保序', () => {
    const calls = toToolCalls([
      { id: 'a', name: 'run_command', arguments: {} },
      { id: 'b', name: 'run_command', arguments: {} },
      { id: 'c', name: 'write_credential', arguments: {} },
    ])
    expect(calls.map((c) => c.name)).toEqual(['run_command', 'run_command', 'write_credential'])
  })
})
