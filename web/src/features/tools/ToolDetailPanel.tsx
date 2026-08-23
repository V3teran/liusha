import type { ToolAgent, ToolDetail } from '@/api/types'
import { categoryLabel } from './toolMeta'
import { KindBadge } from './KindBadge'

// 工具详情面板（主从视图右栏常驻，非 modal）。三态：未选 / 加载中 / 已选。
// 已选态展示：种类徽章 + 分类 + 完整描述 + 全量智能体（已装配者高亮，点击装/卸）。
// 装配态 involved 由后端权威计算并随 detail 下发，本组件只渲染与回调，不自算成员关系。
export function ToolDetailPanel({
  detail,
  loading,
  savingCode,
  onToggle,
}: {
  detail: ToolDetail | null
  loading: boolean
  savingCode: string | null
  onToggle: (code: string, next: boolean) => void
}) {
  const tool = detail?.tool

  if (loading && !detail) {
    return (
      <PanelFrame>
        <div className="tac-cursor py-16 text-center font-mono text-[13px] text-muted">加载中</div>
      </PanelFrame>
    )
  }

  if (!tool) {
    return (
      <PanelFrame>
        <div className="flex h-full flex-col items-center justify-center gap-2 py-16 text-center">
          <p className="font-mono text-[13px] text-muted">从左侧选择一个工具</p>
          <p className="text-[12px] text-faint">查看完整描述并装配到智能体</p>
        </div>
      </PanelFrame>
    )
  }

  return (
    <PanelFrame>
      <div className="tac-dots flex items-center gap-2.5 border-b border-border px-5 py-4">
        <h2 className="truncate font-mono text-base font-semibold text-text">{tool.name}</h2>
        <KindBadge kind={tool.kind} />
      </div>
      <div className="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto px-5 py-4.5">
        <div className="flex items-center gap-3 text-[13px]">
          <span className="text-muted">分类</span>
          <span className="text-text">{categoryLabel(tool.category)}</span>
        </div>
        <section>
          <p className="mb-1.5 text-xs font-medium text-muted">描述</p>
          <p className="whitespace-pre-wrap text-[13px] leading-relaxed text-text">
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

// 面板外框：与卡片同质感的圆角描边容器，撑满右栏高度。
function PanelFrame({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden rounded-xl border border-border bg-surface">
      {children}
    </div>
  )
}

// 「装配到智能体」区块：列出全量智能体，已装配（involved）者高亮，点击即装/卸该工具。
function AgentAssignSection({
  agents,
  savingCode,
  onToggle,
}: {
  agents: ToolAgent[]
  savingCode: string | null
  onToggle: (code: string, next: boolean) => void
}) {
  const involvedCount = agents.filter((a) => a.involved).length

  return (
    <section>
      <p className="mb-2 flex items-baseline gap-1.5 text-xs font-medium text-muted">
        装配到智能体
        <span className="font-normal text-faint">
          已装配 {involvedCount}/{agents.length}
        </span>
        <span className="ml-auto font-normal text-faint">点击装/卸</span>
      </p>
      {agents.length === 0 ? (
        <p className="text-[13px] text-muted">暂无智能体</p>
      ) : (
        <div className="flex flex-wrap gap-2">
          {agents.map((a) => {
            const saving = savingCode === a.code
            return (
              <button
                key={a.code}
                type="button"
                disabled={saving}
                aria-pressed={a.involved}
                onClick={() => onToggle(a.code, !a.involved)}
                className={
                  'rounded-md border px-2.5 py-1 text-[12.5px] transition-colors disabled:opacity-50 ' +
                  (a.involved
                    ? 'border-accent bg-accent/15 text-accent'
                    : 'border-border bg-background text-muted hover:border-accent/50 hover:text-text')
                }
              >
                {a.name}
              </button>
            )
          })}
        </div>
      )}
    </section>
  )
}
