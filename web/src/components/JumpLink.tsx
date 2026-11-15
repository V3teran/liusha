import { Link } from 'react-router-dom'
import type { ComponentType, ReactNode } from 'react'

/** md：详情面板 / 抽屉的「跳转」区；sm：与状态、筛选控件同行的紧凑尺寸（顶栏、工具栏）。 */
export type JumpLinkSize = 'md' | 'sm'

const SIZE_CLASS: Record<JumpLinkSize, string> = {
  md: 'gap-1.5 rounded-lg px-3 py-1.5 text-[12.5px]',
  sm: 'gap-1 rounded-md px-2 py-1 text-xs',
}

/**
 * 跳转链接：「跳转」的统一视觉语法——边框盒 + 图标 + 文本，hover 描边转 accent。
 * 图标取侧栏导航同款，跨面板跳转与换页导航是同一个目的地的两种入口。
 */
export function JumpLink({
  to,
  icon: Icon,
  title,
  size = 'md',
  onClick,
  children,
}: {
  to: string
  icon: ComponentType<{ className?: string }>
  title?: string
  size?: JumpLinkSize
  onClick?: () => void
  children: ReactNode
}) {
  return (
    <Link
      to={to}
      onClick={onClick}
      title={title}
      className={`inline-flex flex-shrink-0 items-center border border-border bg-surface-2 text-text-secondary transition-colors hover:border-accent hover:text-accent ${SIZE_CLASS[size]}`}
    >
      <Icon className="h-3.5 w-3.5" />
      {children}
    </Link>
  )
}
