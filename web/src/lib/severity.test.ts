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
  it('已知等级返回三件套配色（字色为等级 token，底/边由同色 color-mix 派生）', () => {
    const tag = severityTagColor('critical')
    expect(tag.textColor).toBe('var(--sev-critical)')
    expect(tag.color).toBe('color-mix(in srgb, var(--sev-critical) 13%, var(--surface))')
    expect(tag.borderColor).toBe('color-mix(in srgb, var(--sev-critical) 33%, var(--surface))')
  })

  it('未知等级回退中性色 token', () => {
    const tag = severityTagColor('nope')
    expect(tag.textColor).toBe('var(--status-neutral)')
  })

  it('大小写不敏感', () => {
    expect(severityTagColor('HIGH').textColor).toBe('var(--sev-high)')
  })
})
