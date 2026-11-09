import { describe, expect, it } from 'vitest'
import { hunks, lineDiff } from './lineDiff'

describe('lineDiff', () => {
  it('标出删除与新增行，保留公共行', () => {
    expect(lineDiff('a\nb\nc', 'a\nx\nc')).toEqual([
      { kind: 'same', text: 'a' },
      { kind: 'del', text: 'b' },
      { kind: 'add', text: 'x' },
      { kind: 'same', text: 'c' },
    ])
  })
  it('相同文本全为公共行；末尾追加为新增', () => {
    expect(lineDiff('a', 'a').every((l) => l.kind === 'same')).toBe(true)
    expect(lineDiff('a', 'a\nb')).toEqual([
      { kind: 'same', text: 'a' },
      { kind: 'add', text: 'b' },
    ])
  })
})

describe('hunks', () => {
  it('只保留改动及上下文，远处省略', () => {
    const before = Array.from({ length: 20 }, (_, i) => `l${i}`).join('\n')
    const after = before.replace('l10', 'L10')
    const h = hunks(lineDiff(before, after), 1)
    expect(h.filter((l) => l === null)).toHaveLength(0)
    expect(h.map((l) => l?.text)).toEqual(['l9', 'l10', 'L10', 'l11'])
    expect(hunks(lineDiff('a', 'a'))).toEqual([])
  })
})
