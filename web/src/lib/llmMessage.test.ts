import { describe, expect, it } from 'vitest'
import { contentToText, extractReasoning, msgColor, pickField, toMsgView } from './llmMessage'

describe('contentToText', () => {
  it('字符串原样返回', () => {
    expect(contentToText('hello')).toBe('hello')
  })

  it('多模态数组只取文本片段，非文本类型标注为 [type]', () => {
    const content = [{ type: 'text', text: '看图说话' }, { type: 'image_url', image_url: { url: 'x' } }]
    expect(contentToText(content)).toBe('看图说话\n[image_url]')
  })

  it('null/undefined 返回空串', () => {
    expect(contentToText(null)).toBe('')
    expect(contentToText(undefined)).toBe('')
  })

  it('其他对象兜底 JSON.stringify', () => {
    expect(contentToText({ a: 1 })).toBe(JSON.stringify({ a: 1 }, null, 2))
  })
})

describe('toMsgView', () => {
  it('提取 role/content/tool_calls', () => {
    const v = toMsgView({ role: 'user', content: '你好', tool_calls: [{ id: 'c1', function: { name: 'run', arguments: '{}' } }] })
    expect(v.role).toBe('user')
    expect(v.text).toBe('你好')
    expect(v.calls).toHaveLength(1)
    expect(v.calls[0].name).toBe('run')
  })

  it('缺失 role 时兜底 unknown', () => {
    expect(toMsgView({}).role).toBe('unknown')
  })
})

describe('extractReasoning', () => {
  it('result.thinking.text（审计契约新行，小写 json tag 键）', () => {
    expect(extractReasoning({ content: 'x', thinking: { text: '想一下', signature: 'sig' } })).toBe('想一下')
  })

  it('result.Thinking.Text（历史行，Go 结构体大写键）', () => {
    expect(extractReasoning({ Content: 'x', Thinking: { Text: '旧数据思考' } })).toBe('旧数据思考')
  })

  it('thinking 优先于 reasoning_content', () => {
    expect(extractReasoning({ thinking: { text: '新的' }, reasoning_content: '旧的' })).toBe('新的')
  })

  it('result.reasoning_content 字段', () => {
    expect(extractReasoning({ content: 'x', reasoning_content: '先想再说' })).toBe('先想再说')
  })

  it('result.extra["reasoning-content"] 备用字段', () => {
    expect(extractReasoning({ content: 'x', extra: { 'reasoning-content': '备用思考' } })).toBe('备用思考')
  })

  it('两者都无返回空串', () => {
    expect(extractReasoning({ content: 'x' })).toBe('')
  })

  it('result 非对象返回空串', () => {
    expect(extractReasoning('纯文本')).toBe('')
    expect(extractReasoning(null)).toBe('')
  })
})

describe('pickField', () => {
  it('按顺序取第一个存在且非 null 的键（新行小写优先，旧行大写兜底）', () => {
    expect(pickField({ content: '新', Content: '旧' }, ['content', 'Content'])).toBe('新')
    expect(pickField({ Content: '旧' }, ['content', 'Content'])).toBe('旧')
  })

  it('跳过 null/undefined 值', () => {
    expect(pickField({ ToolCalls: null, tool_calls: [] }, ['tool_calls', 'ToolCalls'])).toEqual([])
    expect(pickField({ ToolCalls: null }, ['tool_calls', 'ToolCalls'])).toBeUndefined()
  })

  it('非对象输入返回 undefined', () => {
    expect(pickField('文本', ['content'])).toBeUndefined()
    expect(pickField(null, ['content'])).toBeUndefined()
  })
})

describe('msgColor', () => {
  it('已知角色返回固定色', () => {
    expect(msgColor('user')).toBe('#38bdf8')
    expect(msgColor('assistant')).toBe('#34d399')
  })

  it('未知角色兜底灰', () => {
    expect(msgColor('weird')).toBe('#94a3b8')
  })
})
