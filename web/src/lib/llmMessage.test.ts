import { describe, expect, it } from 'vitest'
import { contentToText, extractReasoning, msgColor, toMsgView } from './llmMessage'

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

describe('msgColor', () => {
  it('已知角色返回固定色', () => {
    expect(msgColor('user')).toBe('#38bdf8')
    expect(msgColor('assistant')).toBe('#34d399')
  })

  it('未知角色兜底灰', () => {
    expect(msgColor('weird')).toBe('#94a3b8')
  })
})
