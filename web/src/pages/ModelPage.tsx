import { useState } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { ProvidersView } from '@/features/model/ProvidersView'
import { AssignmentView } from '@/features/model/AssignmentView'

// LLM 配置页面 - 简洁的单页面垂直布局
//
// 设计原则：
// 1. 去除冗余：无重复标题、无多余图标
// 2. 清晰层次：折叠区块仅作分组，内容自带标题
// 3. 简单直接：最小化视觉噪音

export function ModelPage() {
  const [providersExpanded, setProvidersExpanded] = useState(true)
  const [routingExpanded, setRoutingExpanded] = useState(false)

  return (
    <div className="flex h-full flex-col">
      {/* 统一头部 */}
      <header className="border-b border-border bg-surface px-6 py-4">
        <h1 className="text-base font-semibold text-text">LLM 配置</h1>
        <p className="mt-0.5 text-sm text-text-secondary">
          配置模型提供商和能力路由
        </p>
      </header>

      {/* 内容区 */}
      <div className="flex-1 overflow-y-auto">
        {/* 模型提供商 */}
        <section className="border-b border-border">
          <button
            type="button"
            onClick={() => setProvidersExpanded(!providersExpanded)}
            className="flex w-full items-center justify-between px-6 py-3 text-left transition-colors hover:bg-surface-2"
          >
            <div className="flex flex-col gap-0.5">
              <span className="text-sm font-medium text-text">模型提供商</span>
              <span className="text-xs text-text-secondary">接入哪些 provider，连接参数与能力标志</span>
            </div>
            {providersExpanded ? (
              <ChevronDown className="h-4 w-4 flex-shrink-0 text-text-secondary" />
            ) : (
              <ChevronRight className="h-4 w-4 flex-shrink-0 text-text-secondary" />
            )}
          </button>
          {providersExpanded && (
            <div className="bg-background">
              <ProvidersView />
            </div>
          )}
        </section>

        {/* 能力路由 */}
        <section className="border-b border-border">
          <button
            type="button"
            onClick={() => setRoutingExpanded(!routingExpanded)}
            className="flex w-full items-center justify-between px-6 py-3 text-left transition-colors hover:bg-surface-2"
          >
            <div className="flex flex-col gap-0.5">
              <span className="text-sm font-medium text-text">能力路由</span>
              <span className="text-xs text-text-secondary">为每个复杂度档位分配模型，并归档智能体</span>
            </div>
            {routingExpanded ? (
              <ChevronDown className="h-4 w-4 flex-shrink-0 text-text-secondary" />
            ) : (
              <ChevronRight className="h-4 w-4 flex-shrink-0 text-text-secondary" />
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
