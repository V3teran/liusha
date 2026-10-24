import { ProvidersView } from '@/features/model/ProvidersView'

// LLM 配置页面 - 业界最佳实践设计
// 参考：OpenAI Platform、Anthropic Console、Vercel AI Settings
//
// 设计理念：
// 1. 单一职责 - 仅管理模型提供商（API Keys + 连接配置）
// 2. 扁平化 - 无多余的 tab 切换
// 3. 直观性 - 配置即所见，立即生效
//
// 能力路由（模型分配）应该在智能体配置中完成，而不是在这里

export function ModelPage() {
  return (
    <div className="flex h-full flex-col">
      {/* 统一头部 */}
      <header className="border-b border-border bg-surface px-6 py-4">
        <h1 className="text-base font-semibold text-text">模型提供商</h1>
        <p className="mt-0.5 text-sm text-text-secondary">
          配置 LLM API 密钥和连接参数
        </p>
      </header>

      {/* 内容区 - 直接展示提供商列表 */}
      <div className="min-h-0 flex-1 overflow-hidden">
        <ProvidersView />
      </div>
    </div>
  )
}
