import { useEffect, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import type { FindingRow } from '@/api/types'
import { severityColor, severityLabel } from '@/lib/severity'
import { FINDING_STATUS_OPTIONS, findingStatusMeta } from '@/lib/findingStatus'
import { fullTime, daysSince } from '@/lib/format'
import { Badge } from '@/components/ui/badge'
import { CopyButton } from '@/components/CopyButton'
import { useCopyToClipboard } from '@/hooks/useCopyToClipboard'
import { cn } from '@/lib/utils'

interface FindingDrawerProps {
  open: boolean
  finding: FindingRow | null
  onOpenChange: (open: boolean) => void
  onSave: (payload: { id: string; status: string; severity: string; note: string }) => void
}

const SEVERITY_OPTIONS = ['critical', 'high', 'medium', 'low', 'info']

const EV_LABELS: Record<string, string> = {
  repro_cmd: '复现命令',
  repro_cmd_time: '复现命令（时间盲注）',
  repro_cmd_boolean: '复现命令（布尔盲注）',
  repro_steps: '复现步骤',
  repro_response: '复现响应',
  payload: 'Payload',
  observation: '观察',
  key_observation: '关键观察',
  conclusion: '结论',
  impact: '影响',
  analysis: '分析',
  description: '描述',
  result: '结果',
  issue: '问题',
  time_delay: '时间延迟',
  response_excerpt: '响应片段',
  response_body: '响应体',
  response_status: '响应状态',
  vulnerable_endpoints: '受影响端点',
  affected_users: '受影响用户',
}

const EV_PRIORITY: string[] = [
  'repro_cmd',
  'repro_cmd_time',
  'repro_cmd_boolean',
  'repro_steps',
  'payload',
  'repro_response',
  'response_excerpt',
  'response_body',
  'response_status',
  'time_delay',
  'observation',
  'key_observation',
  'vulnerable_endpoints',
  'affected_users',
  'analysis',
  'description',
  'issue',
  'conclusion',
  'impact',
  'result',
]

interface EvItem {
  key: string
  value: string
  kind: 'code' | 'text' | 'json'
}

function buildEvaluationItems(evaluation?: Record<string, unknown>): EvItem[] {
  if (!evaluation) return []
  const items: EvItem[] = []
  for (const [k, raw] of Object.entries(evaluation)) {
    if (raw == null || raw === '') continue
    let kind: EvItem['kind'] = 'text'
    let value: string
    if (typeof raw === 'object') {
      kind = 'json'
      value = JSON.stringify(raw, null, 2)
    } else {
      value = String(raw)
      if (/cmd|command|curl|repro|request|payload/i.test(k)) kind = 'code'
    }
    items.push({ key: k, value, kind })
  }
  const rank = (k: string) => {
    const i = EV_PRIORITY.indexOf(k)
    return i === -1 ? EV_PRIORITY.length : i
  }
  return items.sort((a, b) => rank(a.key) - rank(b.key))
}

/**
 * FindingDrawer - 漏洞详情抽屉
 *
 * 设计改进：
 * 1. 更现代的抽屉样式（圆角、阴影、背景模糊）
 * 2. 更清晰的信息层次
 * 3. 更精致的表单控件
 * 4. 统一的间距和圆角
 */
export function FindingDrawer({ open, finding, onOpenChange, onSave }: FindingDrawerProps) {
  const [status, setStatus] = useState('')
  const [severity, setSeverity] = useState('')
  const [note, setNote] = useState('')
  const { copiedKey, copy } = useCopyToClipboard()

  useEffect(() => {
    if (finding) {
      setStatus(finding.status)
      setSeverity(finding.severity)
      setNote(finding.triage_note || '')
    }
  }, [finding])

  if (!finding) return null

  const evItems = buildEvaluationItems(finding.evaluation)
  const sevColor = severityColor[finding.severity.toLowerCase()] || '#6e7681'
  const changed = status !== finding.status || severity !== finding.severity || note !== (finding.triage_note || '')

  const handleSave = () => {
    onSave({ id: finding.id, status, severity, note })
  }

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="app-overlay fixed inset-0 z-50" />
        <Dialog.Content
          className={cn(
            'tac-modal fixed right-0 top-0 z-50 flex h-full w-[720px] flex-col',
            'rounded-l-2xl border-l border-border bg-surface shadow-2xl'
          )}
        >
          {/* 头部 */}
          <div className="flex items-start justify-between gap-4 border-b border-border px-6 py-5">
            <div className="min-w-0 flex-1">
              <div className="mb-2 flex items-center gap-3">
                <Badge color={sevColor} size="md">
                  {severityLabel[finding.severity.toLowerCase()] || finding.severity}
                </Badge>
                <Badge
                  color={findingStatusMeta(finding.status).color}
                  size="md"
                  dot={false}
                >
                  {findingStatusMeta(finding.status).label}
                </Badge>
              </div>
              <Dialog.Title className="text-lg font-semibold text-text">
                {finding.summary}
              </Dialog.Title>
            </div>
            <Dialog.Close
              className={cn(
                'flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg',
                'text-text-secondary transition-all hover:bg-surface-2 hover:text-text'
              )}
            >
              <X className="h-5 w-5" />
            </Dialog.Close>
          </div>

          {/* 内容区域 */}
          <div className="flex-1 overflow-y-auto px-6 py-5">
            <div className="space-y-6">
              {/* 基本信息 */}
              <section>
                <h3 className="mb-3 text-sm font-semibold text-text">基本信息</h3>
                <div className="space-y-2 rounded-lg border border-border bg-surface-2 p-4 text-sm">
                  <div className="flex items-start gap-3">
                    <span className="w-24 flex-shrink-0 text-text-secondary">位置</span>
                    <span className="min-w-0 flex-1 font-mono text-xs text-text">
                      {finding.target?.method} {finding.host}
                      {finding.target?.path}
                    </span>
                  </div>
                  {finding.cwe_id && (
                    <div className="flex items-start gap-3">
                      <span className="w-24 flex-shrink-0 text-text-secondary">CWE ID</span>
                      <span className="font-mono text-text">{finding.cwe_id}</span>
                    </div>
                  )}
                  <div className="flex items-start gap-3">
                    <span className="w-24 flex-shrink-0 text-text-secondary">来源</span>
                    <Badge
                      color={finding.source === 'auto' ? 'var(--source-auto)' : 'var(--source-manual)'}
                      size="sm"
                      dot={false}
                    >
                      {finding.source === 'auto' ? '被动代理' : '主动下发'}
                    </Badge>
                  </div>
                  <div className="flex items-start gap-3">
                    <span className="w-24 flex-shrink-0 text-text-secondary">首次发现</span>
                    <span className="text-text-secondary" title={fullTime(finding.first_seen_at)}>
                      {fullTime(finding.first_seen_at)}
                      {finding.first_seen_at !== finding.created_at && (
                        <span className="ml-2 text-xs text-text-tertiary">
                          (存在 {daysSince(finding.first_seen_at)})
                        </span>
                      )}
                    </span>
                  </div>
                  {finding.first_seen_at !== finding.created_at && (
                    <div className="flex items-start gap-3">
                      <span className="w-24 flex-shrink-0 text-text-secondary">本次发现</span>
                      <span className="text-text-secondary" title={fullTime(finding.created_at)}>
                        {fullTime(finding.created_at)}
                      </span>
                    </div>
                  )}
                  {finding.first_seen_at === finding.created_at && (
                    <div className="flex items-start gap-3">
                      <span className="w-24 flex-shrink-0 text-text-secondary">发现时间</span>
                      <span className="text-text-secondary" title={fullTime(finding.created_at)}>
                        {fullTime(finding.created_at)}
                      </span>
                    </div>
                  )}
                </div>
              </section>

              {/* 修复建议（坐实主张随 remediation 落台账；无值不渲染） */}
              {finding.remediation?.trim() && (
                <section>
                  <h3 className="mb-3 text-sm font-semibold text-text">修复建议</h3>
                  <div className="rounded-lg border border-border bg-surface-2 p-4">
                    <p className="whitespace-pre-wrap text-sm leading-relaxed text-text">
                      {finding.remediation}
                    </p>
                  </div>
                </section>
              )}

              {/* Evaluation 评估信息 */}
              {evItems.length > 0 && (
                <section>
                  <h3 className="mb-3 text-sm font-semibold text-text">评估详情</h3>
                  <div className="space-y-3">
                    {evItems.map((item) => (
                      <div key={item.key} className="rounded-lg border border-border bg-surface-2 p-4">
                        <div className="mb-2 flex items-center justify-between">
                          <span className="text-sm font-medium text-text">
                            {EV_LABELS[item.key] || item.key}
                          </span>
                          {item.kind === 'code' && (
                            <CopyButton
                              copied={copiedKey === item.key}
                              onClick={() => copy(item.key, item.value)}
                              size="sm"
                            />
                          )}
                        </div>
                        {item.kind === 'code' ? (
                          <pre className="overflow-x-auto rounded-lg bg-background p-3 text-xs leading-relaxed text-text">
                            {item.value}
                          </pre>
                        ) : item.kind === 'json' ? (
                          <pre className="overflow-x-auto rounded-lg bg-background p-3 text-xs leading-relaxed text-text">
                            {item.value}
                          </pre>
                        ) : (
                          <p className="whitespace-pre-wrap text-sm leading-relaxed text-text-secondary">
                            {item.value}
                          </p>
                        )}
                      </div>
                    ))}
                  </div>
                </section>
              )}

              {/* 关联漏洞（组合漏洞依赖链） */}
              {finding.depends_on && finding.depends_on.length > 0 && (
                <section>
                  <h3 className="mb-3 text-sm font-semibold text-text">关联漏洞</h3>
                  <div className="rounded-lg border border-border bg-surface-2 p-4">
                    <p className="mb-3 text-xs text-text-tertiary">
                      本漏洞依赖于以下漏洞组合而成：
                    </p>
                    <div className="space-y-2">
                      {finding.depends_on.map((depId) => (
                        <div
                          key={depId}
                          className="flex items-center gap-2 rounded-md border border-border bg-background px-3 py-2 text-sm"
                        >
                          <span className="font-mono text-xs text-text-tertiary">→</span>
                          <span className="font-mono text-text">{depId}</span>
                        </div>
                      ))}
                    </div>
                  </div>
                </section>
              )}

              {/* 处置区域 */}
              <section>
                <h3 className="mb-3 text-sm font-semibold text-text">漏洞处置</h3>
                <div className="space-y-4 rounded-lg border border-border bg-surface-2 p-4">
                  <div>
                    <label className="mb-2 block text-sm font-medium text-text">
                      状态
                    </label>
                    <select
                      value={status}
                      onChange={(e) => setStatus(e.target.value)}
                      className={cn(
                        'w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-text',
                        'outline-none transition-all focus:border-accent focus:glow-accent'
                      )}
                    >
                      {FINDING_STATUS_OPTIONS.map((opt) => (
                        <option key={opt.value} value={opt.value}>
                          {opt.label}
                        </option>
                      ))}
                    </select>
                  </div>

                  <div>
                    <label className="mb-2 block text-sm font-medium text-text">
                      严重度
                    </label>
                    <select
                      value={severity}
                      onChange={(e) => setSeverity(e.target.value)}
                      className={cn(
                        'w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm text-text',
                        'outline-none transition-all focus:border-accent focus:glow-accent'
                      )}
                    >
                      {SEVERITY_OPTIONS.map((sev) => (
                        <option key={sev} value={sev}>
                          {severityLabel[sev] || sev}
                        </option>
                      ))}
                    </select>
                  </div>

                  <div>
                    <label className="mb-2 block text-sm font-medium text-text">
                      备注
                    </label>
                    <textarea
                      value={note}
                      onChange={(e) => setNote(e.target.value)}
                      rows={4}
                      placeholder="记录处置思路、修复方案或风险评估…"
                      className={cn(
                        'w-full resize-y rounded-lg border border-border bg-surface px-3 py-2',
                        'text-sm leading-relaxed text-text placeholder:text-text-tertiary',
                        'outline-none transition-all focus:border-accent focus:glow-accent'
                      )}
                    />
                  </div>
                </div>
              </section>
            </div>
          </div>

          {/* 底部操作栏 */}
          <div className="flex items-center justify-end gap-3 border-t border-border px-6 py-4">
            <Dialog.Close
              className={cn(
                'rounded-lg border border-border px-4 py-2 text-sm font-medium text-text',
                'transition-all hover:bg-surface-2'
              )}
            >
              取消
            </Dialog.Close>
            <button
              type="button"
              disabled={!changed}
              onClick={handleSave}
              className={cn(
                'rounded-lg px-4 py-2 text-sm font-semibold',
                'transition-all',
                changed
                  ? 'bg-accent-soft text-accent hover:bg-accent/10 hover:text-accent-hover'
                  : 'cursor-not-allowed bg-surface-2 text-text-disabled'
              )}
            >
              保存处置
            </button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
