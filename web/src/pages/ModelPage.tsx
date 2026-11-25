import { PageHeader } from '@/components/PageHeader'
import { ProvidersView } from '@/features/model/ProvidersView'
import { AssignmentView } from '@/features/model/AssignmentView'
import { RetrievalView } from '@/features/model/RetrievalView'

// 「模型」页（侧边栏「配置」组）：模型接入的单页设置流（settings page）。
// 三个分区按「先接入、再收口」排布——对话模型（provider 实体 CRUD）→ 检索模型（知识库
// 语义检索的地址、模型与密钥，未配置自动字面检索不阻塞）→ 能力路由（把各档位路由到
// provider；它消费前两区的数据，放在下游）。
// 排版统一约束：三个分区共用同一内容列宽与分区头解剖（标题+说明居左、动作居右），
// 内容全部文档流排布、页面级滚动——不存在内部滚动容器/视口高度的工作台面板，
// 上下三段宽度、密度、留白完全一致。provider 编辑在列表行下方手风琴展开（同刻至多一行）。
export function ModelPage() {
  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title="模型" />

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-4xl flex-col gap-6 px-6 py-6">
          <ProvidersView />
          <RetrievalView />
          <AssignmentView />
        </div>
      </div>
    </div>
  )
}
