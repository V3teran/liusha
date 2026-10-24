import { useSearchParams } from 'react-router-dom'
import { ProvidersView } from '@/features/model/ProvidersView'
import { AssignmentView } from '@/features/model/AssignmentView'
import { cn } from '@/lib/utils'

// LLM 配置页面 - 采用业界最佳实践设计
// 参考：Vercel AI Settings、OpenAI Platform、Anthropic Console
//
// 设计原则：
// 1. 扁平化导航 - 使用简洁的 tab 切换，避免深层嵌套
// 2. 信息层次清晰 - 标题、描述、内容区分明确
// 3. 操作就近原则 - 配置项旁直接操作，减少跳转
// 4. 视觉克制 - 无多余装饰，专注内容

type Tab = 'providers' | 'assignment'

const TABS: { key: Tab; label: string; description: string }[] = [
  {
    key: 'providers',
    label: '模型提供商',
    description: '配置 API 密钥和连接参数'
  },
  {
    key: 'assignment',
    label: '能力路由',
    description: '为不同复杂度分配模型'
  },
]

export function ModelPage() {
  const [params, setParams] = useSearchParams()
  const active: Tab = params.get('tab') === 'assignment' ? 'assignment' : 'providers'

  const select = (tab: Tab) => {
    const next = new URLSearchParams(params)
    if (tab === 'providers') next.delete('tab')
    else next.set('tab', tab)
    setParams(next, { replace: true })
  }

  const activeTab = TABS.find(t => t.key === active) || TABS[0]

  return (
    <div className="flex h-full flex-col">
      {/* 统一头部 */}
      <header className="border-b border-border bg-surface px-6 py-4">
        <h1 className="text-base font-semibold text-text">LLM 配置</h1>
        <p className="mt-0.5 text-sm text-text-secondary">{activeTab.description}</p>
      </header>

      {/* 简洁的 tab 导航 */}
      <div className="border-b border-border bg-surface">
        <nav className="flex gap-1 px-6" role="tablist" aria-label="LLM 配置视图">
          {TABS.map((t) => {
            const isActive = active === t.key
            return (
              <button
                key={t.key}
                type="button"
                role="tab"
                aria-selected={isActive}
                onClick={() => select(t.key)}
                className={cn(
                  'relative px-4 py-3 text-sm font-medium transition-colors',
                  'border-b-2 -mb-px',
                  isActive
                    ? 'border-accent text-accent'
                    : 'border-transparent text-text-secondary hover:text-text hover:border-border'
                )}
              >
                {t.label}
              </button>
            )
          })}
        </nav>
      </div>

      {/* 内容区 */}
      <div className="min-h-0 flex-1 overflow-hidden">
        {active === 'providers' ? <ProvidersView /> : <AssignmentView />}
      </div>
    </div>
  )
}
