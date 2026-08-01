import type { ReactNode } from 'react'
import { CopyButton } from './CopyButton'

interface CopySectionProps {
  title: string
  hint?: ReactNode // 标题旁的补充说明（如「增量（上下文共 N 条）」）
  copyLabel?: string // 复制按钮文案，默认「复制」
  copyText?: string // 有值才渲染复制按钮；空/undefined 则不显示（该节没有可复制的正文）
  copied: boolean
  onCopy: () => void
  children: ReactNode
}

// 详情抽屉里重复了 5 次的「标题 + 右对齐复制按钮 + 内容」结构，抽成一个组件。
// 复制态（图标+文案切换）由外部 useCopyToClipboard 驱动，本组件只负责渲染。
export function CopySection({ title, hint, copyLabel = '复制', copyText, copied, onCopy, children }: CopySectionProps) {
  return (
    <section>
      <h3 className="mb-2.5 flex items-baseline gap-2 text-[13px] font-semibold text-text">
        {title}
        {hint && <span className="text-[11px] font-normal text-muted">{hint}</span>}
        {copyText && <CopyButton copied={copied} onClick={onCopy} label={copyLabel} className="ml-auto" />}
      </h3>
      {children}
    </section>
  )
}
