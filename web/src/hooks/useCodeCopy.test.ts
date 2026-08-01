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

  it('点击 .code-copy 按钮复制成功后显示"已复制"，1400ms 后恢复原文', async () => {
    vi.useFakeTimers()
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
      configurable: true,
    })

    const btn = document.createElement('button')
    btn.className = 'code-copy'
    btn.setAttribute('data-code', 'echo hello')
    btn.textContent = '复制'
    document.body.appendChild(btn)

    const ev = mkEvent(btn)
    onMarkdownClick(ev)
    expect(ev.preventDefault).toHaveBeenCalled()
    expect(ev.stopPropagation).toHaveBeenCalled()

    // 等待 copyText 的 promise resolve
    await vi.waitFor(() => expect(btn.textContent).toBe('已复制'))
    expect(btn.classList.contains('copied')).toBe(true)

    vi.advanceTimersByTime(1400)
    expect(btn.textContent).toBe('复制')
    expect(btn.classList.contains('copied')).toBe(false)
  })

  it('复制失败时显示"复制失败"', async () => {
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
    await vi.waitFor(() => expect(btn.textContent).toBe('复制失败'))
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
    await vi.waitFor(() => expect(btn.textContent).toBe('已复制'))
  })
})
