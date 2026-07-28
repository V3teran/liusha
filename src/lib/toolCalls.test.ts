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
  test('解析 OpenAI 风格 tool_calls 数组', () => {
    const calls = toToolCalls([
      { id: 'call_1', type: 'function', function: { name: 'run_command', arguments: '{"cmd":"id"}' } },
    ])
    expect(calls).toHaveLength(1)
    expect(calls[0].id).toBe('call_1')
    expect(calls[0].name).toBe('run_command')
    expect(calls[0].args).toBe('{\n  "cmd": "id"\n}')
  })

  test('单个 function_call（非数组）也归一成数组', () => {
    const calls = toToolCalls({ name: 'read_findings', arguments: '{}' })
    expect(calls).toHaveLength(1)
    expect(calls[0].name).toBe('read_findings')
    expect(calls[0].args).toBe('')
  })

  test('null 返回空数组', () => {
    expect(toToolCalls(null)).toEqual([])
    expect(toToolCalls(undefined)).toEqual([])
  })

  test('缺 id/name 时兜底占位，不抛错', () => {
    const calls = toToolCalls([{ function: { arguments: '{}' } }])
    expect(calls[0].id).toBe('call-0')
    expect(calls[0].name).toBe('(未命名工具)')
  })

  test('多个工具调用保序', () => {
    const calls = toToolCalls([
      { id: 'a', function: { name: 'run_command', arguments: '{}' } },
      { id: 'b', function: { name: 'run_command', arguments: '{}' } },
      { id: 'c', function: { name: 'write_finding', arguments: '{}' } },
    ])
    expect(calls.map((c) => c.name)).toEqual(['run_command', 'run_command', 'write_finding'])
  })
})
