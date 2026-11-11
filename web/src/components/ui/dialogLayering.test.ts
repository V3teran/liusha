import { describe, expect, it } from 'vitest'

// 弹层层级只有一个事实源（index.css）：
//   - 模态弹层：.app-overlay（含定位）与 .tac-modal / .app-drawer 同处 --z-modal，按 DOM 先后定序；
//   - 锚定弹层：.app-popover / .app-menu 处 --z-popover。
// 使用点各自补定位与 z-index 曾导致漂移：确认框遮罩 z-50 压在无层级的内容之上，按钮点不到
// （点到遮罩即被当作点外部关闭）；另有对话框遮罩缺定位，背景不压暗。这里按源码守住约定。

const sources = import.meta.glob('/src/**/*.tsx', { query: '?raw', import: 'default', eager: true }) as Record<
  string,
  string
>

function tagClassNames(src: string, tag: string): string[] {
  const out: string[] = []
  const re = new RegExp(`<${tag.replace('.', '\\.')}\\b[\\s\\S]*?>`, 'g')
  for (const m of src.matchAll(re)) {
    const cls = /className=(?:"([^"]*)"|\{[\s\S]*?\})/.exec(m[0])
    out.push(cls ? (cls[1] ?? cls[0]) : '')
  }
  return out
}

const files = Object.entries(sources).filter(([path]) => !path.endsWith('.test.tsx'))

describe('弹层层级约定', () => {
  it('遮罩只用 app-overlay，不在使用点补定位或层级', () => {
    const bad = files.flatMap(([path, src]) =>
      tagClassNames(src, 'Dialog.Overlay')
        .filter((c) => c.trim() !== 'app-overlay')
        .map((c) => `${path}: ${c}`),
    )
    expect(bad).toEqual([])
  })

  it('弹层内容走 tac-modal / app-drawer 取层级，不写 z-*', () => {
    const bad = files.flatMap(([path, src]) =>
      tagClassNames(src, 'Dialog.Content')
        .filter((c) => !/\b(tac-modal|app-drawer)\b/.test(c) || /(^|[\s'"])z-/.test(c))
        .map((c) => `${path}: ${c.slice(0, 120)}`),
    )
    expect(bad).toEqual([])
  })

  it('锚定弹层（Popover / DropdownMenu）走 app-popover / app-menu 取层级，不写 z-*', () => {
    const bad = files.flatMap(([path, src]) =>
      [...tagClassNames(src, 'Popover.Content'), ...tagClassNames(src, 'DropdownMenu.Content')]
        .filter((c) => !/\b(app-popover|app-menu)\b/.test(c) || /(^|[\s'"])z-/.test(c))
        .map((c) => `${path}: ${c.slice(0, 120)}`),
    )
    expect(bad).toEqual([])
  })
})
