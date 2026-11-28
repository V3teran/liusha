/**
 * 节点详情浮层：主文本突出 + 元信息徽章 + 领域载荷 k/v + 验证审计链
 * （坐实/证伪的机器证据——审计链的可读出口）。
 */

import { useState } from 'react'
import { Ban, X } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { cn } from '@/lib/utils'
import type { GraphNode, Verification } from '@/api/explorationgraph'
import { priorityMeta } from '@/lib/priority'
import {
  kindProfile,
  leadHasSpec,
  leadMeta,
  nodeTitle,
  rejectionScope,
  stateMeta,
  verificationMeta,
  COMPLEXITY_META,
  OBSERVATION_CATEGORY_META,
} from './kindTheme'
import { RejectActionDialog } from './RejectActionDialog'

const SOURCE_LABELS: Record<string, string> = {
  user: '用户',
  planner: '规划者',
  executor: '执行者',
  evaluator: '评估者',
  monitor: '监察者',
}

/** 内容字段的展示名：契约固定字段给中文名，LLM 自由扩展字段按原键展示（如实）。 */
const CONTENT_FIELD_LABELS: Record<string, string> = {
  statement: '陈述',
  instruction: '指令',
  description: '描述',
  reasoning: '推理依据',
  category: '分类',
  repro: '复现配方',
  impact: '影响',
  suggestion: '建议',
  host: '主机线索',
  target: '目标指向',
}

/** 标量值的展示形：枚举字段按既有词表翻译，其余按原值。 */
function displayValue(key: string, v: unknown): string {
  if (key === 'category' && typeof v === 'string') {
    return OBSERVATION_CATEGORY_META[v] ?? v
  }
  return String(v)
}

/** 线索三样里的一行：标签加正文；这一样没写就不占行 */
function LeadRow({ label, text }: { label: string; text: string }) {
  if (!text) return null
  return (
    <div className="flex gap-2">
      <span className="w-9 flex-shrink-0 text-text-tertiary">{label}</span>
      <span className="break-words text-text">{text}</span>
    </div>
  )
}

function MetaChip({ label, value, valueClass }: { label: string; value: string; valueClass?: string }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-lg bg-surface-2 px-2 py-1 text-xs text-text-secondary">
      {label}
      <span className={cn('font-medium text-text', valueClass)}>{value}</span>
    </span>
  )
}

/** 否决方（动作 metadata.rejected_by：user / monitor） */
function rejectedBy(node: GraphNode): string {
  const meta = node.metadata
  if (meta && typeof meta === 'object' && 'rejected_by' in meta) {
    const by = (meta as { rejected_by?: unknown }).rejected_by
    if (typeof by === 'string') return by
  }
  return ''
}

/** 否决的说法：谁否决的、否决到哪一步（只否决这一步，还是连它跟进的线索一起）、为什么。 */
function rejectionText(node: GraphNode): string {
  const scope = rejectionScope(node) === 'lead' ? '，连它跟进的线索一起' : '，只否决这一步'
  return `否决（${SOURCE_LABELS[rejectedBy(node)] ?? '未知'}${scope}）：${node.state_reason}`
}

export function NodeDetailPanel({
  node,
  verification,
  direction,
  onClose,
}: {
  node: GraphNode
  verification?: Verification
  /** 动作所属的方向（挂在派生目标下的动作才有）：来路上紧挨着它的那一步 */
  direction?: GraphNode
  onClose: () => void
}) {
  const profile = kindProfile(node.kind)
  const Icon = profile.icon
  const title = nodeTitle(node)
  const state = stateMeta(node.state)
  const verificationLabel = verificationMeta(node.verification_status)
  const lead = leadMeta(node)
  const content = (node.content ?? {}) as Record<string, unknown>
  // 主文本已置顶展示，k/v 里跳过同值字段避免重复
  const contentEntries = Object.entries(content).filter(
    ([, v]) => v != null && v !== '' && String(v) !== title,
  )
  const outcomeLabel = verification?.outcome === 'confirmed' ? '已坐实' : '已证伪'
  // 待执行或执行中的动作可由用户否决（任务已结束时后端 409，弹层内如实提示）。
  const rejectable = node.kind === 'action' && (node.state === 'open' || node.state === 'running')
  const [rejecting, setRejecting] = useState(false)

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
          aria-label="关闭详情"
          title="关闭"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      <div role="region" aria-label="节点详情" tabIndex={0} className="flex-1 space-y-4 overflow-y-auto px-4 py-3">
        {title && <p className="whitespace-pre-wrap break-words text-sm leading-relaxed text-text">{title}</p>}

        <div className="flex flex-wrap gap-1.5">
          {priorityMeta(node.priority) && (
            <MetaChip
              label="优先级"
              value={`${priorityMeta(node.priority)!.code} ${priorityMeta(node.priority)!.label}`}
              valueClass={node.priority === 'critical' ? 'text-sev-critical' : node.priority === 'high' ? 'text-sev-high' : undefined}
            />
          )}
          {node.complexity && COMPLEXITY_META[node.complexity] && (
            <MetaChip label="复杂度" value={COMPLEXITY_META[node.complexity]} />
          )}
          {verificationLabel && <MetaChip label="验证" value={verificationLabel} />}
          {lead && <MetaChip label="线索" value={lead.closed ? '已关闭' : '待跟进'} />}
          <MetaChip label="来源" value={SOURCE_LABELS[node.source_type] ?? node.source_type} />
          <MetaChip label="创建" value={new Date(node.created_at).toLocaleString()} />
        </div>

        {rejectable && (
          <Button variant="danger" size="sm" onClick={() => setRejecting(true)}>
            <Ban />
            否决动作
          </Button>
        )}

        {node.kind === 'action' && node.state_reason && (
          <section className="rounded-lg border border-border bg-background/50 px-3 py-2 text-xs text-text-secondary">
            <div className="text-status-warning">
              {node.state === 'rejected' ? rejectionText(node) : `原因：${node.state_reason}`}
            </div>
          </section>
        )}

        {direction && (
          <section className="rounded-lg border border-border bg-background/50 px-3 py-2 text-xs text-text-secondary">
            <div className="break-words">所属方向：{nodeTitle(direction)}</div>
          </section>
        )}

        {lead && leadHasSpec(lead.spec) && (
          <section
            aria-label="线索"
            className="space-y-1 rounded-lg border border-border bg-background/50 px-3 py-2 text-xs text-text-secondary"
          >
            <LeadRow label="下一步" text={lead.spec.next} />
            <LeadRow label="要弄清" text={lead.spec.question} />
            <LeadRow label="能换来" text={lead.spec.potential} />
          </section>
        )}

        {lead?.closed && lead.detail && (
          <section className="rounded-lg border border-border bg-background/50 px-3 py-2 text-xs text-text-secondary">
            <div className="break-words">线索已关闭（{lead.detail}）</div>
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
                  <dt className="text-xs text-text-tertiary">{CONTENT_FIELD_LABELS[k] ?? k}</dt>
                  <dd className="text-xs text-text">
                    {typeof v === 'object' ? (
                      // 可滚动区域须能用键盘聚焦滚动（WCAG 2.1.1）
                      <pre
                        tabIndex={0}
                        aria-label={CONTENT_FIELD_LABELS[k] ?? k}
                        className="mt-0.5 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-background p-2 font-mono text-2xs leading-relaxed"
                      >
                        {JSON.stringify(v, null, 2)}
                      </pre>
                    ) : (
                      <span className="whitespace-pre-wrap break-words">{displayValue(k, v)}</span>
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
                  'rounded px-1.5 py-0.5 text-2xs font-semibold',
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
                验证时刻：<span className="font-mono">{new Date(verification.created_at).toLocaleString()}</span>
              </div>
              {verification.duration_ms > 0 && <div>复现耗时：{verification.duration_ms} ms</div>}
            </div>
            {verification.evidence != null && (
              <pre tabIndex={0} className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-background p-2 font-mono text-2xs leading-relaxed text-text">
                {typeof verification.evidence === 'string'
                  ? verification.evidence
                  : JSON.stringify(verification.evidence, null, 2)}
              </pre>
            )}
          </section>
        )}
      </div>
      {rejecting && <RejectActionDialog action={node} onOpenChange={(o) => !o && setRejecting(false)} />}
    </div>
  )
}
