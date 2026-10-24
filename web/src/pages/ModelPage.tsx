import { useSearchParams } from 'react-router-dom'
import { Boxes, Route } from 'lucide-react'
import { ProvidersView } from '@/features/model/ProvidersView'
import { AssignmentView } from '@/features/model/AssignmentView'
import { cn } from '@/lib/utils'

// 模型模块：单模块内 tab 切换两视图，共用一套 provider 事实源。
//   · 部署（providers）：接入哪些 provider，连接参数与能力标志。
//   · 能力分档（assignment）：每档绑定一个部署，并勾选归入本档的智能体（读写 agent.complexity，
//     与「智能体」页复杂度下拉同源，只是展示维度不同）。
// 「先接部署，再给各档选模型并归档 agent」是同一件事的两步，同模块内切换比跨侧栏跳转更贴心智。
// 活动视图持久化进 URL search（?tab=），刷新/分享/前进后退都能还原。
type Tab = 'providers' | 'assignment'

const TABS: { key: Tab; label: string; icon: typeof Boxes }[] = [
  { key: 'providers', label: '部署', icon: Boxes },
  { key: 'assignment', label: '能力分档', icon: Route },
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

  return (
    <div className="flex h-full flex-col">
      {/* 统一头部 */}
      <header className="flex items-center justify-between border-b border-border bg-surface px-6 py-4">
        <div>
          <h1 className="text-base font-semibold text-text">LLM 配置</h1>
          <p className="mt-0.5 text-sm text-text-secondary">模型部署与能力分档配置</p>
        </div>
      </header>

      {/* Tab 切换 */}
      <div className="flex h-full min-h-0 flex-col">
      <div
        role="tablist"
        aria-label="模型模块视图"
        className="flex flex-shrink-0 items-center gap-1 border-b border-border px-4 pt-2"
      >
        {TABS.map((t) => {
          const on = active === t.key
          const Icon = t.icon
          return (
            <button
              key={t.key}
              type="button"
              role="tab"
              aria-selected={on}
              onClick={() => select(t.key)}
              className={cn(
                'relative -mb-px flex items-center gap-2 border-b-2 px-4 py-2.5 text-sm font-medium transition-all',
                on
                  ? 'border-accent text-accent'
                  : 'border-transparent text-text-secondary hover:text-text',
              )}
            >
              <Icon className="h-4 w-4" />
              {t.label}
            </button>
          )
        })}
      </div>

      <div className="min-h-0 flex-1">
        {active === 'providers' ? <ProvidersView /> : <AssignmentView />}
      </div>
      </div>
    </div>
  )
}
