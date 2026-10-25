import { ProvidersView } from '@/features/model/ProvidersView'
import { AssignmentView } from '@/features/model/AssignmentView'

// LLM 配置页：单页设置流（settings page）。
// 两个功能是同一件事的两步——先接入 provider（实体 CRUD），再把各档位路由到 provider（表单配置）。
// 排版统一约束：两个分区共用同一内容列宽与分区头解剖（标题+说明居左、动作居右），
// 内容全部文档流排布、页面级滚动——不存在内部滚动容器/视口高度的工作台面板，
// 上下两段宽度、密度、留白完全一致。编辑类交互（provider 表单）走居中弹窗，对齐配置页族。
export function ModelPage() {
  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex-shrink-0 border-b border-border bg-surface px-6 py-4">
        <h1 className="text-base font-semibold text-text">LLM 配置</h1>
        <p className="mt-0.5 text-sm text-text-secondary">配置模型提供商和能力路由</p>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-4xl flex-col gap-8 px-6 py-6">
          <ProvidersView />
          <AssignmentView />
        </div>
      </div>
    </div>
  )
}
