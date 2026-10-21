import { afterEach, describe, expect, it, vi } from 'vitest'
import { onMarkdownClick } from './useCodeCopy'

function mkEvent(target: HTMLElement): React.MouseEvent {
  return {
    target,
    preventDefault: vi.fn(),
    stopPropagation: vi.fn(),
  } as unknown as React.MouseEvent
}

describe('onMarkdownClick', () => {
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('非 .code-copy 目标不处理', () => {
    const div = document.createElement('div')
    const ev = mkEvent(div)
    onMarkdownClick(ev)
    expect(ev.preventDefault).not.toHaveBeenCalled()
  })

  it('点击 .code-copy 复制成功后切 .copied + aria-label「已复制」，1400ms 后恢复', async () => {
    vi.useFakeTimers()
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
      configurable: true,
    })

    const btn = document.createElement('button')
    btn.className = 'code-copy'
    btn.setAttribute('data-code', 'echo hello')
    btn.setAttribute('aria-label', '复制代码')
    document.body.appendChild(btn)

    const ev = mkEvent(btn)
    onMarkdownClick(ev)
    expect(ev.preventDefault).toHaveBeenCalled()
    expect(ev.stopPropagation).toHaveBeenCalled()

    // 等待 copyText 的 promise resolve：图标按钮无文字，成功态看 class + aria-label
    await vi.waitFor(() => expect(btn.classList.contains('copied')).toBe(true))
    expect(btn.getAttribute('aria-label')).toBe('已复制')
    expect(btn.title).toBe('已复制')

    vi.advanceTimersByTime(1400)
    expect(btn.classList.contains('copied')).toBe(false)
    expect(btn.getAttribute('aria-label')).toBe('复制代码')
  })

  it('复制失败时不加 .copied，aria-label 提示「复制失败」', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockRejectedValue(new Error('denied')) },
      configurable: true,
    })
    document.execCommand = vi.fn().mockReturnValue(false)

    const btn = document.createElement('button')
    btn.className = 'code-copy'
    btn.setAttribute('data-code', 'x')
    document.body.appendChild(btn)

    onMarkdownClick(mkEvent(btn))
    await vi.waitFor(() => expect(btn.getAttribute('aria-label')).toBe('复制失败'))
    expect(btn.classList.contains('copied')).toBe(false)
  })

  it('closest 命中子元素也能找到 .code-copy 容器', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
      configurable: true,
    })

    const btn = document.createElement('button')
    btn.className = 'code-copy'
    btn.setAttribute('data-code', 'y')
    const icon = document.createElement('span')
    btn.appendChild(icon)
    document.body.appendChild(btn)

    onMarkdownClick(mkEvent(icon))
    await vi.waitFor(() => expect(btn.classList.contains('copied')).toBe(true))
  })

  it('连点时重置回退计时，成功态不被上一轮 timer 提前清掉', async () => {
    vi.useFakeTimers()
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
      configurable: true,
    })

    const btn = document.createElement('button')
    btn.className = 'code-copy'
    btn.setAttribute('data-code', 'z')
    document.body.appendChild(btn)

    // 用 advanceTimersByTimeAsync(0) 做确定性的微任务刷新，假时间轴完全可控。
    onMarkdownClick(mkEvent(btn)) // click1：timer1 @ t=1400
    await vi.advanceTimersByTimeAsync(0)
    expect(btn.classList.contains('copied')).toBe(true)

    await vi.advanceTimersByTimeAsync(1000) // t=1000，timer1 未到
    expect(btn.classList.contains('copied')).toBe(true)

    onMarkdownClick(mkEvent(btn)) // click2：清掉 timer1，timer2 @ t=2400
    await vi.advanceTimersByTimeAsync(0)
    vi.advanceTimersByTime(1000) // t=2000：若无重置，timer1 早在 1400 清掉 copied
    expect(btn.classList.contains('copied')).toBe(true)

    vi.advanceTimersByTime(399) // t=2399，还差 1ms
    expect(btn.classList.contains('copied')).toBe(true)
    vi.advanceTimersByTime(1) // t=2400 到点，回退
    expect(btn.classList.contains('copied')).toBe(false)
  })
})
