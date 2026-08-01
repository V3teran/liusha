import { useEffect, useRef, useState } from 'react'

// useTypewriter：把「目标文本」平滑地逐字揭示成「显示文本」，与目标文本的到达节奏解耦。
//
// 为什么需要：后端 reasoning 流经 eino ReAct 图的 stateModelWrapper 时被 ConcatMessageStream
// 同步拍平（逐 token 在 eino 内部就被抽干缓冲），到前端时整段 delta 在几毫秒内涌出——直接绑
// liveReasoning 会让气泡整块蹦出、没有逐字效果。业界通行做法（ChatGPT/Claude UI）是本地打字机
// 动画：网络整块到达没关系，前端按稳定节奏逐字揭示。rAF 驱动，backlog 越大揭示越快（永不长时间
// 落后），到达即追平后自动 idle。
//
// prefers-reduced-motion：直接吐全文，不做动画（无障碍）。

// CHARS_PER_SECOND_BASE：基础揭示速率（字/秒）。约 55 字/秒 ≈ 自然快速打字，中文可读。
const CHARS_PER_SECOND_BASE = 55
// MAX_BACKLOG_CLEAR_FRAMES：backlog 目标清空帧数上限。backlog 很大时按此加速，避免长时间落后于真实进度。
const MAX_BACKLOG_CLEAR_FRAMES = 90 // ~1.5s @60fps

/**
 * @param source 目标文本（会随 delta 增长；重置为 '' 时显示同步清空）
 * @returns displayed 显示文本（逐字追平 source）
 */
export function useTypewriter(source: string): string {
  const [displayed, setDisplayed] = useState('')
  const displayedRef = useRef('')
  const rafRef = useRef<number | undefined>(undefined)
  const lastTsRef = useRef(0)

  useEffect(() => {
    displayedRef.current = displayed
  }, [displayed])

  useEffect(() => {
    const reduce =
      typeof window !== 'undefined' &&
      window.matchMedia?.('(prefers-reduced-motion: reduce)').matches

    const stop = () => {
      if (rafRef.current !== undefined) {
        cancelAnimationFrame(rafRef.current)
        rafRef.current = undefined
      }
    }

    const step = (ts: number) => {
      const target = source
      const current = displayedRef.current
      // 目标比已显示短，或已显示不再是目标前缀（source 被重置/换内容）→ 直接对齐，避免残留旧字。
      if (target.length < current.length || !target.startsWith(current)) {
        displayedRef.current = target
        setDisplayed(target)
        lastTsRef.current = ts
        if (target.length >= target.length) {
          stop()
          return
        }
      }

      const backlog = target.length - displayedRef.current.length
      if (backlog <= 0) {
        stop() // 追平：停 rAF，省电；source 再增长时 effect 会重启。
        return
      }

      const dt = lastTsRef.current ? (ts - lastTsRef.current) / 1000 : 0
      lastTsRef.current = ts
      // 基础速率 + backlog 自适应加速：backlog 大时按「上限帧数内清空」提速，保证不长期滞后。
      const adaptive = Math.ceil(backlog / MAX_BACKLOG_CLEAR_FRAMES) * 60
      const rate = Math.max(CHARS_PER_SECOND_BASE, adaptive)
      const advance = Math.max(1, Math.round(rate * dt))
      const next = target.slice(0, displayedRef.current.length + advance)
      displayedRef.current = next
      setDisplayed(next)

      rafRef.current = requestAnimationFrame(step)
    }

    const start = () => {
      if (rafRef.current === undefined) {
        lastTsRef.current = 0
        rafRef.current = requestAnimationFrame(step)
      }
    }

    if (reduce) {
      displayedRef.current = source // 无障碍：直接全文，不动画
      setDisplayed(source)
    } else if (source.length > displayedRef.current.length && source.startsWith(displayedRef.current)) {
      start() // 目标增长 → 启动/继续揭示
    } else if (source.length <= displayedRef.current.length) {
      displayedRef.current = source // 收缩/清空 → 立即对齐
      setDisplayed(source)
      stop()
    } else {
      start() // 内容变更（非前缀增长）→ step 内会对齐后继续
    }

    return stop
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [source])

  return displayed
}
