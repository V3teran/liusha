import type { ReactNode } from 'react'

// 页面统一头部：固定 56px（h-14）+ 底部分隔线，与侧栏 logo 区（h-14 border-b）连成一条贯穿
// 壳体的横线。只有标题，不设副标题：页面靠结构与控件本身说明用途，概念说明放在用到它的地方
// （字段、空态），不在页头常驻一行复述。右侧动作区经 children 注入。
export function PageHeader({ title, children }: { title: ReactNode; children?: ReactNode }) {
  return (
    <header className="flex h-14 flex-shrink-0 items-center justify-between gap-4 border-b border-border bg-surface px-6">
      <h1 className="min-w-0 truncate text-base font-semibold leading-tight text-text">{title}</h1>
      {children != null && <div className="flex flex-shrink-0 items-center gap-3">{children}</div>}
    </header>
  )
}
