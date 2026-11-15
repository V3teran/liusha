import { describe, expect, it } from 'vitest'

// 可滚动区域必须可键盘聚焦（WCAG 2.1.1，axe scrollable-region-focusable）：只能用鼠标滚的
// 长文本区块，键盘用户读不到被裁掉的部分。<pre> 是最常见的可滚动区块（工具输出、提示词、
// LLM 原文），曾在多个抽屉里漏加 tabIndex。这里按源码守住：带 overflow-auto / overflow-x-auto /
// overflow-y-auto 的 <pre> 必须写 tabIndex={0}。

const sources = import.meta.glob('/src/**/*.tsx', { query: '?raw', import: 'default', eager: true }) as Record<
  string,
  string
>

describe('可滚动区块可聚焦', () => {
  it('可滚动的 <pre> 都带 tabIndex={0}', () => {
    const bad = Object.entries(sources)
      .filter(([path]) => !path.endsWith('.test.tsx'))
      .flatMap(([path, src]) =>
        [...src.matchAll(/<pre\b[\s\S]*?>/g)]
          .map((m) => m[0])
          .filter((tag) => /\boverflow-(?:[xy]-)?auto\b/.test(tag) && !/tabIndex=\{0\}/.test(tag))
          .map((tag) => `${path}: ${tag.slice(0, 100)}`),
      )
    expect(bad).toEqual([])
  })
})
