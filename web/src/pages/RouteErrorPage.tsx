import { useRouteError } from 'react-router-dom'
import { ErrorState } from '@/components/ui/ErrorState'

/**
 * 路由级错误页：页面渲染出错（含它的代码块取不到）时由路由的错误边界渲染，取代路由库自带的英文默认页。
 * 代码块取不到多发生在程序升级之后——开着旧版控制台的浏览器还在请求旧版本的代码块文件，重新加载
 * 即换成新版本；别的错误也照原样给出原因，重新加载后仍在，就拿着这条原因去排障。
 * 失败原因由路由库的错误边界打到控制台，这里不再重复。
 *
 * reload 在路由里取默认值；测试传入替身。
 */
export function RouteErrorPage({ reload = () => window.location.reload() }: { reload?: () => void } = {}) {
  const error = useRouteError()
  const reason = error instanceof Error ? error.message : error != null ? String(error) : ''
  return (
    <div className="flex h-full items-center justify-center">
      <ErrorState title="页面出错了" message={reason || undefined} onRetry={reload} retryLabel="重新加载" />
    </div>
  )
}
