import { Link } from 'react-router-dom'
import type { LucideIcon } from 'lucide-react'

/**
 * 跳转链接：详情面板/抽屉「跳转」区的统一视觉语法——边框盒 + 图标 + 文本，
 * hover 描边转 accent。图标取侧栏导航同款（任务 ListChecks / 对话 MessageSquare /
 * 漏洞 Bug / 探索图 GitBranch），跨面板跳转与换页导航是同一个目的地的两种入口。
 */
export function JumpLink({
  to,
  icon: Icon,
  title,
  onClick,
  children,
}: {
  to: string
  icon: LucideIcon
  title?: string
  onClick?: () => void
  children: React.ReactNode
}) {
  return (
    <Link
      to={to}
      onClick={onClick}
      title={title}
      className="inline-flex items-center gap-1.5 rounded-lg border border-border bg-surface-2 px-3 py-1.5 text-[12.5px] text-text-secondary transition-colors hover:border-accent hover:text-accent"
    >
      <Icon className="h-3.5 w-3.5" />
      {children}
    </Link>
  )
}
