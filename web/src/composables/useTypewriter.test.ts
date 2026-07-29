import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { ref, nextTick } from 'vue'
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
  async function tick(frames: number, stepMs = 16) {
    for (let i = 0; i < frames; i++) {
      now += stepMs
      const cbs = rafCbs
      rafCbs = []
      cbs.forEach((cb) => cb(now))
      await nextTick()
    }
  }

  it('逐字揭示：不一次性吐全文', async () => {
    const src = ref('')
    const out = useTypewriter(src)
    src.value = '这是一段很长的推理文字用来测试逐字揭示效果是否平滑'
    await nextTick()
    await tick(1)
    // 一帧后只揭示了一部分，不是全文
    expect(out.value.length).toBeGreaterThan(0)
    expect(out.value.length).toBeLessThan(src.value.length)
    // 是目标的前缀
    expect(src.value.startsWith(out.value)).toBe(true)
  })

  it('最终追平目标全文', async () => {
    const src = ref('短文本')
    const out = useTypewriter(src)
    await nextTick()
    await tick(60)
    expect(out.value).toBe('短文本')
  })

  it('source 清空时显示同步清空', async () => {
    const src = ref('一些内容')
    const out = useTypewriter(src)
    await tick(60)
    src.value = ''
    await nextTick()
    await tick(1)
    expect(out.value).toBe('')
  })

  it('reduced-motion 直接吐全文，不动画', async () => {
    vi.stubGlobal('matchMedia', () => ({ matches: true }))
    const src = ref('')
    const out = useTypewriter(src)
    src.value = '无障碍模式全文'
    await nextTick()
    expect(out.value).toBe('无障碍模式全文') // 无需 tick
  })

  it('大 backlog 自适应加速：比基础速率快，且最终追平', async () => {
    const big = ref('')
    const bigOut = useTypewriter(big)
    big.value = 'x'.repeat(500)
    await nextTick()
    await tick(10)
    const bigProgress = bigOut.value.length

    // 对照：小 backlog 同样 10 帧的进度应远小于大 backlog（证明自适应确实按 backlog 加速）
    const small = ref('')
    const smallOut = useTypewriter(small)
    small.value = 'y'.repeat(20)
    await nextTick()
    await tick(10)
    expect(bigProgress).toBeGreaterThan(smallOut.value.length)

    // 最终必追平（source 停止增长后总会赶上）
    await tick(300)
    expect(bigOut.value.length).toBe(500)
  })
})
