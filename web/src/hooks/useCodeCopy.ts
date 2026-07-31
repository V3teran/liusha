// 代码块复制：dangerouslySetInnerHTML 注入的 .code-copy 按钮无法绑 React handler，用事件委托。
// markdown 容器上挂一个 onClick，命中 .code-copy 就复制其 data-code、按钮短暂变「已复制」。
import { copyText } from '@/lib/clipboard'

// onMarkdownClick 可直接绑到 markdown 容器 onClick 的处理器。
export function onMarkdownClick(ev: React.MouseEvent) {
  const target = (ev.target as HTMLElement)?.closest('.code-copy') as HTMLButtonElement | null
  if (!target) return
  ev.preventDefault()
  ev.stopPropagation()
  const code = target.getAttribute('data-code') ?? ''
  void copyText(code).then((ok) => {
    const prev = target.textContent
    target.textContent = ok ? '已复制' : '复制失败'
    target.classList.toggle('copied', ok)
    window.setTimeout(() => {
      target.textContent = prev
      target.classList.remove('copied')
    }, 1400)
  })
}
