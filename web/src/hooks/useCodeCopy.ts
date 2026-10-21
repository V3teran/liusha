// 代码块复制：dangerouslySetInnerHTML 注入的 .code-copy 按钮无法绑 React handler，用事件委托。
// markdown 容器上挂一个 onClick，命中 .code-copy 就复制其 data-code；
// 成功态只切 .copied class（CSS 换 check 图标）+ 同步 aria-label/title，按钮本体始终无文字。
import { copyText } from '@/lib/clipboard'

// onMarkdownClick 可直接绑到 markdown 容器 onClick 的处理器。
export function onMarkdownClick(ev: React.MouseEvent) {
  const target = (ev.target as HTMLElement)?.closest('.code-copy') as HTMLButtonElement | null
  if (!target) return
  ev.preventDefault()
  ev.stopPropagation()
  // 连点时重置上一轮回退计时，避免成功态被早到的 timer 提前清掉。
  const prev = target.dataset.copyTimer
  if (prev) window.clearTimeout(Number(prev))
  const code = target.getAttribute('data-code') ?? ''
  void copyText(code).then((ok) => {
    target.classList.toggle('copied', ok)
    const label = ok ? '已复制' : '复制失败'
    target.setAttribute('aria-label', label)
    target.setAttribute('title', label)
    const timer = window.setTimeout(() => {
      target.classList.remove('copied')
      target.setAttribute('aria-label', '复制代码')
      target.setAttribute('title', '复制代码')
      delete target.dataset.copyTimer
    }, 1400)
    target.dataset.copyTimer = String(timer)
  })
}
