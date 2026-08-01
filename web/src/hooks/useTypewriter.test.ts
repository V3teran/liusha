import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useTypewriter } from './useTypewriter'

// rAF 用假计时器驱动：每次 flush 前进一帧（16ms），显式推进以断言逐字揭示。
describe('useTypewriter', () => {
  let rafCbs: FrameRequestCallback[] = []
  let now = 0

  beforeEach(() => {
    rafCbs = []
    now = 0
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => {
      rafCbs.push(cb)
      return rafCbs.length
    })
    vi.stubGlobal('cancelAnimationFrame', () => {})
    // 默认非 reduced-motion
    vi.stubGlobal('matchMedia', () => ({ matches: false }))
  })
  afterEach(() => vi.unstubAllGlobals())

  // 推进 n 帧（每帧 +stepMs）
  function tick(frames: number, stepMs = 16) {
    for (let i = 0; i < frames; i++) {
      now += stepMs
      const cbs = rafCbs
      rafCbs = []
      act(() => {
        cbs.forEach((cb) => cb(now))
      })
    }
  }

  it('逐字揭示：不一次性吐全文', () => {
    const { result, rerender } = renderHook(({ source }) => useTypewriter(source), {
      initialProps: { source: '' },
    })
    const target = '这是一段很长的推理文字用来测试逐字揭示效果是否平滑'
    rerender({ source: target })
    tick(1)
    // 一帧后只揭示了一部分，不是全文
    expect(result.current.length).toBeGreaterThan(0)
    expect(result.current.length).toBeLessThan(target.length)
    // 是目标的前缀
    expect(target.startsWith(result.current)).toBe(true)
  })

  it('最终追平目标全文', () => {
    const { result, rerender } = renderHook(({ source }) => useTypewriter(source), {
      initialProps: { source: '' },
    })
    rerender({ source: '短文本' })
    tick(60)
    expect(result.current).toBe('短文本')
  })

  it('source 清空时显示同步清空', () => {
    const { result, rerender } = renderHook(({ source }) => useTypewriter(source), {
      initialProps: { source: '一些内容' },
    })
    tick(60)
    rerender({ source: '' })
    tick(1)
    expect(result.current).toBe('')
  })

  it('reduced-motion 直接吐全文，不动画', () => {
    vi.stubGlobal('matchMedia', () => ({ matches: true }))
    const { result, rerender } = renderHook(({ source }) => useTypewriter(source), {
      initialProps: { source: '' },
    })
    rerender({ source: '无障碍模式全文' })
    expect(result.current).toBe('无障碍模式全文') // 无需 tick
  })

  it('大 backlog 自适应加速：比基础速率快，且最终追平', () => {
    const big = renderHook(({ source }) => useTypewriter(source), {
      initialProps: { source: '' },
    })
    big.rerender({ source: 'x'.repeat(500) })
    tick(10)
    const bigProgress = big.result.current.length

    // 对照：小 backlog 同样 10 帧的进度应远小于大 backlog（证明自适应确实按 backlog 加速）
    const small = renderHook(({ source }) => useTypewriter(source), {
      initialProps: { source: '' },
    })
    small.rerender({ source: 'y'.repeat(20) })
    tick(10)
    expect(bigProgress).toBeGreaterThan(small.result.current.length)

    // 最终必追平（source 停止增长后总会赶上）
    tick(300)
    expect(big.result.current.length).toBe(500)
  })
})
