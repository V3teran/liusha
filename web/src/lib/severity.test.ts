import { describe, expect, it } from 'vitest'
import { severityRank, severityTagColor } from './severity'

describe('severityRank', () => {
  it('按威胁等级排序：critical 最前，未知最后', () => {
    expect(severityRank('critical')).toBe(0)
    expect(severityRank('high')).toBe(1)
    expect(severityRank('medium')).toBe(2)
    expect(severityRank('low')).toBe(3)
    expect(severityRank('info')).toBe(4)
    expect(severityRank('unknown')).toBe(5)
  })

  it('大小写不敏感', () => {
    expect(severityRank('CRITICAL')).toBe(0)
    expect(severityRank('High')).toBe(1)
  })
})

describe('severityTagColor', () => {
  it('已知等级返回三件套配色', () => {
    const tag = severityTagColor('critical')
    expect(tag.textColor).toBe('#ef4444')
    expect(tag.color).toBe('#ef444422')
    expect(tag.borderColor).toBe('#ef444455')
  })

  it('未知等级回退默认色', () => {
    const tag = severityTagColor('nope')
    expect(tag.textColor).toBe('#6e7681')
  })

  it('大小写不敏感', () => {
    expect(severityTagColor('HIGH').textColor).toBe('#f97316')
  })
})
