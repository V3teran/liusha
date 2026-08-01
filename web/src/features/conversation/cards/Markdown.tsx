import { renderMarkdown } from '@/lib/markdown'
import { onMarkdownClick } from '@/hooks/useCodeCopy'
import { cn } from '@/lib/utils'

interface MarkdownProps {
  content: string
  className?: string
}

// 共享的 markdown 富文本渲染：DOMPurify 消毒后的 HTML + 代码块复制事件委托。
// 供答复文字 / ReasoningCard 复用（两者渲染逻辑一致，仅外层容器样式不同）。
export function Markdown({ content, className }: MarkdownProps) {
  return (
    <div
      className={cn('markdown-body break-words text-sm leading-relaxed', className)}
      onClick={onMarkdownClick}
      dangerouslySetInnerHTML={{ __html: renderMarkdown(content) }}
    />
  )
}
