/**
 * 节点详情浮层：主文本突出 + 元信息徽章 + 领域载荷 k/v + 验证审计链
 * （坐实/证伪的机器证据——审计链的可读出口）。
 */

import { X } from 'lucide-react'
import { cn } from '@/lib/utils'
import type { GraphNode, Verification } from '@/api/explorationgraph'
import { kindProfile, nodeTitle, stateMeta, confidenceMeta, PRIORITY_META, COMPLEXITY_META } from './kindTheme'

const SOURCE_LABELS: Record<string, string> = {
  user: '用户',
  planner: '规划者',
  executor: '执行者',
  evaluator: '裁决者',
  monitor: '监察者',
}

function MetaChip({ label, value, valueClass }: { label: string; value: string; valueClass?: string }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-lg bg-surface-2 px-2 py-1 text-xs text-text-secondary">
      {label}
      <span className={cn('font-medium text-text', valueClass)}>{value}</span>
    </span>
  )
}

export function NodeDetailPanel({
  node,
  verification,
  onClose,
}: {
  node: GraphNode
  verification?: Verification
  onClose: () => void
}) {
  const profile = kindProfile(node.kind)
  const Icon = profile.icon
  const title = nodeTitle(node)
  const state = stateMeta(node.state)
  const confidence = confidenceMeta(node.confidence)
  const content = (node.content ?? {}) as Record<string, unknown>
  // 主文本已置顶展示，k/v 里跳过同值字段避免重复
  const contentEntries = Object.entries(content).filter(
    ([, v]) => v != null && v !== '' && String(v) !== title,
  )
  const outcomeLabel = verification?.outcome === 'confirmed' ? '已坐实' : '已证伪'
  const dependsCount = node.depends_on?.length ?? 0

  return (
    <div className="absolute right-4 top-4 z-10 flex max-h-[calc(100%-2rem)] w-96 flex-col overflow-hidden rounded-xl border border-border bg-surface shadow-2xl">
      <div className="flex items-center gap-2 border-b border-border px-4 py-3">
        <Icon className="h-4 w-4 flex-shrink-0" style={{ color: profile.accent }} />
        <span className="text-sm font-semibold text-text">{profile.label}</span>
        {state && (
          <span className="inline-flex items-center gap-1 rounded bg-surface-2 px-1.5 py-0.5 text-xs text-text-secondary">
            <span className={cn('h-1.5 w-1.5 rounded-full', state.dotClass)} />
            {state.label}
          </span>
        )}
        <button
          onClick={onClose}
          className="ml-auto rounded p-1 text-text-tertiary transition-colors hover:bg-surface-2 hover:text-text"
          title="关闭"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      <div className="flex-1 space-y-4 overflow-y-auto px-4 py-3">
        {title && <p className="whitespace-pre-wrap break-words text-sm leading-relaxed text-text">{title}</p>}

        <div className="flex flex-wrap gap-1.5">
          {node.priority && PRIORITY_META[node.priority] && (
            <MetaChip
              label="优先级"
              value={`${PRIORITY_META[node.priority].code} ${PRIORITY_META[node.priority].label}`}
              valueClass={node.priority === 'critical' ? 'text-sev-critical' : node.priority === 'high' ? 'text-sev-high' : undefined}
            />
          )}
          {node.complexity && COMPLEXITY_META[node.complexity] && (
            <MetaChip label="复杂度" value={COMPLEXITY_META[node.complexity]} />
          )}
          {confidence && <MetaChip label="置信度" value={confidence} />}
          <MetaChip label="来源" value={SOURCE_LABELS[node.source_type] ?? node.source_type} />
          <MetaChip label="创建" value={new Date(node.created_at).toLocaleString()} />
        </div>

        {node.kind === 'action' && (dependsCount > 0 || node.blocked_reason) && (
          <section className="rounded-lg border border-border bg-background/50 px-3 py-2 text-xs text-text-secondary">
            {dependsCount > 0 && <div>依赖 {dependsCount} 个前置动作</div>}
            {node.blocked_reason && <div className="text-status-warning">阻塞：{node.blocked_reason}</div>}
          </section>
        )}

        <section>
          <h4 className="mb-2 text-xs font-semibold uppercase tracking-wide text-text-tertiary">内容</h4>
          {contentEntries.length === 0 ? (
            <p className="text-xs text-text-tertiary">（无其他字段）</p>
          ) : (
            <dl className="space-y-2">
              {contentEntries.map(([k, v]) => (
                <div key={k}>
                  <dt className="text-xs text-text-tertiary">{k}</dt>
                  <dd className="text-xs text-text">
                    {typeof v === 'object' ? (
                      <pre className="mt-0.5 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-background p-2 font-mono text-[10px] leading-relaxed">
                        {JSON.stringify(v, null, 2)}
                      </pre>
                    ) : (
                      <span className="whitespace-pre-wrap break-words">{String(v)}</span>
                    )}
                  </dd>
                </div>
              ))}
            </dl>
          )}
        </section>

        {verification && (
          <section>
            <h4 className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-wide text-text-tertiary">
              验证审计链
              <span
                className={cn(
                  'rounded px-1.5 py-0.5 text-[10px] font-semibold',
                  verification.outcome === 'confirmed'
                    ? 'bg-status-success/15 text-status-success'
                    : 'bg-status-error/15 text-status-error',
                )}
              >
                {outcomeLabel}
              </span>
            </h4>
            <div className="space-y-1 text-xs text-text-secondary">
              <div>
                裁决时刻：<span className="font-mono">{new Date(verification.created_at).toLocaleString()}</span>
              </div>
              {verification.duration_ms > 0 && <div>复现耗时：{verification.duration_ms} ms</div>}
            </div>
            {verification.evaluation != null && (
              <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-background p-2 font-mono text-[10px] leading-relaxed text-text">
                {typeof verification.evaluation === 'string'
                  ? verification.evaluation
                  : JSON.stringify(verification.evaluation, null, 2)}
              </pre>
            )}
          </section>
        )}
      </div>
    </div>
  )
}
