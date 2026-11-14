import type { ToolDetail } from '@/api/types'
import { categoryLabel } from './toolMeta'
import { KindBadge } from './KindBadge'
import { AgentAssignSection } from '@/components/AgentAssignSection'
import { Button } from '@/components/ui/Button'

// 工具详情面板（主从视图右栏常驻，非 modal）。四态：未选 / 加载中 / 加载失败 / 已选。
// 已选态展示：种类徽章 + 分类 + 沙盒安装状态 + 完整描述 + 全量智能体（已装配者高亮，点击装/卸）。
// 装配态 involved 由后端权威计算并随 detail 下发，本组件只渲染与回调，不自算成员关系。
export function ToolDetailPanel({
  detail,
  loading,
  error,
  onRetry,
  savingCode,
  onToggle,
}: {
  detail: ToolDetail | null
  loading: boolean
  error: string
  onRetry: () => void
  savingCode: string | null
  onToggle: (code: string, next: boolean) => void
}) {
  const tool = detail?.tool
  const allowedCount = detail?.agents.filter((a) => a.involved).length ?? 0

  if (loading && !detail) {
    return (
      <PanelFrame>
        <div className="terminal-cursor py-16 text-center font-mono text-sm text-text-secondary">加载中</div>
      </PanelFrame>
    )
  }

  if (error && !detail) {
    return (
      <PanelFrame>
        <div role="alert" className="flex h-full flex-col items-center justify-center gap-3 py-16 text-center">
          <p className="font-mono text-sm text-sev-critical">⚠ {error}</p>
          <Button variant="secondary" size="sm" onClick={onRetry}>
            重试
          </Button>
        </div>
      </PanelFrame>
    )
  }

  if (!tool) {
    return (
      <PanelFrame>
        <div className="flex h-full flex-col items-center justify-center gap-2 py-16 text-center">
          <p className="font-mono text-sm text-text-secondary">从左侧选择一个工具</p>
          <p className="text-xs text-text-tertiary">查看完整描述并装配到智能体</p>
        </div>
      </PanelFrame>
    )
  }

  return (
    <PanelFrame>
      <div className="flex items-center gap-2.5 border-b border-border px-5 py-4">
        <h2 className="truncate font-mono text-base font-semibold text-text">{tool.name}</h2>
        <KindBadge kind={tool.kind} />
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto px-5 py-4">
        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-2 text-sm">
          <dt className="text-text-secondary">分类</dt>
          <dd className="text-text">{categoryLabel(tool)}</dd>
          <dt className="text-text-secondary">沙盒</dt>
          <dd className={tool.installed === false ? 'text-status-warning' : 'text-text'}>
            {installLabel(tool.installed)}
          </dd>
        </dl>
        {tool.installed === false && allowedCount > 0 && (
          <p role="note" className="rounded-md border border-status-warning/40 px-3 py-2 text-xs text-status-warning">
            已允许 · 未安装：已装配它的 {allowedCount} 个智能体运行时无法调用，在沙盒装上后自动生效
          </p>
        )}
        <section>
          <p className="mb-1.5 text-xs font-medium text-text-secondary">描述</p>
          <p className="whitespace-pre-wrap text-sm leading-relaxed text-text">
            {tool.description || '暂无描述'}
          </p>
        </section>
        <AgentAssignSection
          agents={detail?.agents ?? []}
          savingCode={savingCode}
          onToggle={onToggle}
        />
      </div>
    </PanelFrame>
  )
}

function installLabel(installed: boolean | null): string {
  if (installed === null) return '状态未知（沙盒不可达）'
  return installed ? '已安装' : '未安装'
}

// 面板外框：与卡片同质感的圆角描边容器，撑满右栏高度。
function PanelFrame({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden rounded-xl border border-border bg-surface shadow-sm">
      {children}
    </div>
  )
}

