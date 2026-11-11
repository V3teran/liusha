import { Link, useLocation } from 'react-router-dom'
import { Compass } from 'lucide-react'
import { PageHeader } from '@/components/PageHeader'
import { buttonVariants } from '@/components/ui/Button'

/** 未匹配任何路由时的兜底页：保留应用外壳（侧栏可继续导航），给出明确的返回入口。 */
export function NotFoundPage() {
  const { pathname } = useLocation()
  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title="页面不存在" />
      <div className="flex flex-1 flex-col items-center justify-center gap-4 px-6 text-center">
        <span className="flex h-12 w-12 items-center justify-center rounded-full bg-surface-2 text-text-tertiary">
          <Compass aria-hidden className="h-6 w-6" />
        </span>
        <div>
          <p className="text-sm text-text">没有找到这个页面</p>
          <p className="mt-1 break-all font-mono text-xs text-text-tertiary">{pathname}</p>
        </div>
        <Link
          to="/tasks"
          className={buttonVariants()}
        >
          返回任务列表
        </Link>
      </div>
    </div>
  )
}
