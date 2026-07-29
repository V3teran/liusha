import { ref, watch, onUnmounted, type Ref } from 'vue'

// useTypewriter：把「目标文本」平滑地逐字揭示成「显示文本」，与目标文本的到达节奏解耦。
//
// 为什么需要：后端 reasoning 流经 eino ReAct 图的 stateModelWrapper 时被 ConcatMessageStream
// 同步拍平（逐 token 在 eino 内部就被抽干缓冲），到前端时整段 delta 在几毫秒内涌出——直接绑
// liveReasoning 会让气泡整块蹦出、没有逐字效果。业界通行做法（ChatGPT/Claude UI）是本地打字机
// 动画：网络整块到达没关系，前端按稳定节奏逐字揭示。rAF 驱动，backlog 越大揭示越快（永不长时间
// 落后），到达即追平后自动 idle。
//
// prefers-reduced-motion：直接吐全文，不做动画（无障碍）。

// charsPerSecondBase：基础揭示速率（字/秒）。约 55 字/秒 ≈ 自然快速打字，中文可读。
const CHARS_PER_SECOND_BASE = 55
// maxBacklogFrames：backlog 目标清空帧数上限。backlog 很大时按此加速，避免长时间落后于真实进度。
const MAX_BACKLOG_CLEAR_FRAMES = 90 // ~1.5s @60fps

/**
 * @param source 目标文本 ref（会随 delta 增长；重置为 '' 时显示同步清空）
 * @returns displayed 显示文本 ref（逐字追平 source）
 */
export function useTypewriter(source: Ref<string>): Ref<string> {
  const displayed = ref('')
  const reduce =
    typeof window !== 'undefined' &&
    window.matchMedia?.('(prefers-reduced-motion: reduce)').matches

  let raf: number | undefined
  let lastTs = 0

  const stop = () => {
    if (raf !== undefined) {
      cancelAnimationFrame(raf)
      raf = undefined
    }
  }

  const step = (ts: number) => {
    const target = source.value
    // 目标比已显示短，或已显示不再是目标前缀（source 被重置/换内容）→ 直接对齐，避免残留旧字。
    if (target.length < displayed.value.length || !target.startsWith(displayed.value)) {
      displayed.value = target
      lastTs = ts
      if (displayed.value.length >= target.length) {
        stop()
        return
      }
    }

    const backlog = target.length - displayed.value.length
    if (backlog <= 0) {
      stop() // 追平：停 rAF，省电；source 再增长时 watch 会重启。
      return
    }

    const dt = lastTs ? (ts - lastTs) / 1000 : 0
    lastTs = ts
    // 基础速率 + backlog 自适应加速：backlog 大时按「上限帧数内清空」提速，保证不长期滞后。
    const adaptive = Math.ceil(backlog / MAX_BACKLOG_CLEAR_FRAMES) * 60
    const rate = Math.max(CHARS_PER_SECOND_BASE, adaptive)
    const advance = Math.max(1, Math.round(rate * dt))
    displayed.value = target.slice(0, displayed.value.length + advance)

    raf = requestAnimationFrame(step)
  }

  const start = () => {
    if (raf === undefined) {
      lastTs = 0
      raf = requestAnimationFrame(step)
    }
  }

  watch(
    source,
    (val) => {
      if (reduce) {
        displayed.value = val // 无障碍：直接全文，不动画
        return
      }
      if (val.length > displayed.value.length && val.startsWith(displayed.value)) {
        start() // 目标增长 → 启动/继续揭示
      } else if (val.length <= displayed.value.length) {
        displayed.value = val // 收缩/清空 → 立即对齐
        stop()
      } else {
        start() // 内容变更（非前缀增长）→ step 内会对齐后继续
      }
    },
    { immediate: true },
  )

  onUnmounted(stop)

  return displayed
}
