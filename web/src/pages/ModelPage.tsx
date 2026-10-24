import { useState } from 'react'
import { ChevronDown, ChevronRight, Settings } from 'lucide-react'
import { ProvidersView } from '@/features/model/ProvidersView'
import { AssignmentView } from '@/features/model/AssignmentView'
import { cn } from '@/lib/utils'

// LLM 配置页面 - 单页面垂直布局设计
// 参考：Vercel AI Settings 的单页面区块式布局
//
// 设计理念：
// 1. 单页面：无 tab 切换，所有配置在一个页面垂直排列
// 2. 可折叠区块：Providers 和 Model Routing 各自独立，可展开/收起
// 3. 清晰分隔：视觉上明确区分两个功能区域
// 4. 渐进呈现：高级配置默认收起，避免初学者困惑

export function ModelPage() {
  const [providersExpanded, setProvidersExpanded] = useState(true)
  const [routingExpanded, setRoutingExpanded] = useState(false)

  return (
    <div className="flex h-full flex-col">
      {/* 统一头部 */}
      <header className="border-b border-border bg-surface px-6 py-4">
        <h1 className="text-base font-semibold text-text">LLM 配置</h1>
        <p className="mt-0.5 text-sm text-text-secondary">
          管理模型提供商和智能体能力路由
        </p>
      </header>

      {/* 滚动内容区 */}
      <div className="flex-1 overflow-y-auto">
        {/* 模型提供商区块 */}
        <section className="border-b border-border">
          <button
            type="button"
            onClick={() => setProvidersExpanded(!providersExpanded)}
            className="flex w-full items-center justify-between bg-surface px-6 py-4 transition-colors hover:bg-surface-2"
          >
            <div className="flex items-center gap-3">
              <Settings className="h-5 w-5 text-text-secondary" />
              <div className="text-left">
                <h2 className="text-sm font-semibold text-text">模型提供商</h2>
                <p className="text-xs text-text-secondary">配置 API 密钥和连接参数</p>
              </div>
            </div>
            {providersExpanded ? (
              <ChevronDown className="h-5 w-5 text-text-secondary" />
            ) : (
              <ChevronRight className="h-5 w-5 text-text-secondary" />
            )}
          </button>
          {providersExpanded && (
            <div className="bg-background">
              <ProvidersView />
            </div>
          )}
        </section>

        {/* 能力路由区块 */}
        <section className="border-b border-border">
          <button
            type="button"
            onClick={() => setRoutingExpanded(!routingExpanded)}
            className="flex w-full items-center justify-between bg-surface px-6 py-4 transition-colors hover:bg-surface-2"
          >
            <div className="flex items-center gap-3">
              <Settings className="h-5 w-5 text-text-secondary" />
              <div className="text-left">
                <h2 className="text-sm font-semibold text-text">能力路由</h2>
                <p className="text-xs text-text-secondary">
                  为不同复杂度的智能体分配模型
                </p>
              </div>
            </div>
            {routingExpanded ? (
              <ChevronDown className="h-5 w-5 text-text-secondary" />
            ) : (
              <ChevronRight className="h-5 w-5 text-text-secondary" />
            )}
          </button>
          {routingExpanded && (
            <div className="bg-background">
              <AssignmentView />
            </div>
          )}
        </section>
      </div>
    </div>
  )
}
