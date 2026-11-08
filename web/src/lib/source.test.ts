import { describe, expect, it } from 'vitest'
import { SOURCE_OPTIONS, conversationPath, sourceColor, sourceLabel } from './source'

describe('source', () => {
  it('两种来源各有唯一的完整文案', () => {
    expect(sourceLabel('direct')).toBe('自主下发')
    expect(sourceLabel('proxy')).toBe('代理模式')
  })

  it('未知来源原样返回、色落中性灰', () => {
    expect(sourceLabel('weird')).toBe('weird')
    expect(sourceColor('weird')).toBe('var(--status-neutral)')
  })

  it('来源色引用来源 token', () => {
    expect(sourceColor('direct')).toBe('var(--source-direct)')
    expect(sourceColor('proxy')).toBe('var(--source-proxy)')
  })

  it('筛选选项与文案同源', () => {
    expect(SOURCE_OPTIONS.map((o) => o.label)).toEqual(['自主下发', '代理模式'])
  })
})

describe('conversationPath', () => {
  it('路径段等于来源值，带会话 id 时编码为 conv 参数', () => {
    expect(conversationPath('proxy', 'c 1')).toBe('/conversations/proxy?conv=c%201')
    expect(conversationPath('direct', 'c1')).toBe('/conversations/direct?conv=c1')
  })

  it('缺失或未知来源落自主下发 tab', () => {
    expect(conversationPath(undefined, 'c1')).toBe('/conversations/direct?conv=c1')
    expect(conversationPath('', 'c1')).toBe('/conversations/direct?conv=c1')
    expect(conversationPath('weird')).toBe('/conversations/direct')
  })
})
