import type { ReactNode } from 'react'

// 页面统一头部：固定 64px（h-16）+ 底部分隔线，与侧栏 logo 区（h-16 border-b）
// 连成一条贯穿壳体的横线。标题 + 副标题纵向堆叠（合计约 38px 垂直居中），
// 右侧动作区（新建按钮 / 开关 / 搜索框）经 children 注入。
export function PageHeader({
  title,
  subtitle,
  children,
}: {
  title: ReactNode
  subtitle?: ReactNode
  children?: ReactNode
}) {
  return (
    <header className="flex h-16 flex-shrink-0 items-center justify-between gap-4 border-b border-border bg-surface px-6">
      <div className="min-w-0">
        <h1 className="text-base font-semibold leading-tight text-text">{title}</h1>
        {subtitle != null && (
          <p className="mt-0.5 truncate text-[13px] leading-tight text-text-secondary">{subtitle}</p>
        )}
      </div>
      {children != null && <div className="flex flex-shrink-0 items-center gap-3">{children}</div>}
    </header>
  )
}
