import { useEffect, useId, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { ListChecks, MessageSquare, X } from 'lucide-react'
import type { FindingRow } from '@/api/types'
import { JumpLink } from '@/components/JumpLink'
import { severityColorOf, severityLabel } from '@/lib/severity'
import { FINDING_STATUS_OPTIONS, findingStatusMeta } from './findingStatus'
import { fullTime, daysSince } from '@/lib/format'
import { Badge } from '@/components/ui/Badge'
import { CopyButton } from '@/components/CopyButton'
import { useCopyToClipboard } from '@/hooks/useCopyToClipboard'
import { cn } from '@/lib/utils'
import { buildEvidenceView } from './findingEvidence'
import { Button } from '@/components/ui/Button'
import { Select } from '@/components/ui/Select'
import { conversationPath, sourceColor, sourceLabel } from '@/lib/source'

interface FindingDrawerProps {
  open: boolean
  finding: FindingRow | null
  onOpenChange: (open: boolean) => void
  onSave: (payload: { id: string; status: string; severity: string; note: string }) => void
}

const SEVERITY_OPTIONS = ['critical', 'high', 'medium', 'low', 'info']


/**
 * FindingDrawer - 漏洞详情抽屉
 */
export function FindingDrawer({ open, finding, onOpenChange, onSave }: FindingDrawerProps) {
  const [status, setStatus] = useState('')
  const [severity, setSeverity] = useState('')
  const [note, setNote] = useState('')
  const fieldId = useId()
  const { copiedKey, copy } = useCopyToClipboard()

  useEffect(() => {
    if (finding) {
      setStatus(finding.status)
      setSeverity(finding.severity)
      setNote(finding.triage_note || '')
    }
  }, [finding])

  if (!finding) return null

  const ev = buildEvidenceView(finding.evaluation, finding.repro)
  const sevColor = severityColorOf(finding.severity)
  const changed = status !== finding.status || severity !== finding.severity || note !== (finding.triage_note || '')

  const handleSave = () => {
    onSave({ id: finding.id, status, severity, note })
  }

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="app-overlay" />
        <Dialog.Content
          className={cn(
            'tac-modal fixed right-0 top-0 flex h-full w-[720px] flex-col',
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
              aria-label="关闭"
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
              {/* 跳转：漏洞回它的产出上下文（任务 / 对话），与任务详情板跳转区同一
                  JumpLink 语法。链接缺失（旧数据/无会话）不渲染占位。 */}
              {(finding.task_id || finding.conversation_id) && (
                <section>
                  <h3 className="mb-3 text-sm font-semibold text-text">跳转</h3>
                  <div className="flex flex-wrap gap-2">
                    {finding.task_id && (
                      <JumpLink to={`/tasks?task=${encodeURIComponent(finding.task_id)}`} icon={ListChecks}>
                        任务
                      </JumpLink>
                    )}
                    {finding.conversation_id && (
                      <JumpLink
                        to={conversationPath(finding.source, finding.conversation_id)}
                        icon={MessageSquare}
                      >
                        对话
                      </JumpLink>
                    )}
                  </div>
                </section>
              )}

              {/* 基本信息 */}
              <section>
                <h3 className="mb-3 text-sm font-semibold text-text">基本信息</h3>
                <div className="space-y-2 rounded-lg border border-border bg-surface-2 p-4 text-sm">
                  <div className="flex items-start gap-3">
                    <span className="w-24 flex-shrink-0 text-text-secondary">地址</span>
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
                  {finding.owasp_category && (
                    <div className="flex items-start gap-3">
                      <span className="w-24 flex-shrink-0 text-text-secondary">OWASP</span>
                      <span className="font-mono text-text">{finding.owasp_category}</span>
                    </div>
                  )}
                  <div className="flex items-start gap-3">
                    <span className="w-24 flex-shrink-0 text-text-secondary">来源</span>
                    <Badge color={sourceColor(finding.source)} size="sm" dot={false}>
                      {sourceLabel(finding.source)}
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

              {/* 危害：主张成立后的后果与影响面（evaluation.impact）——
                  业界漏洞报告「描述→危害→复现→证据→修复」结构的第二环；
                  LLM 未产出时不渲染占位。 */}
              {typeof finding.evaluation?.impact === 'string' && finding.evaluation.impact && (
                <section>
                  <h3 className="mb-3 text-sm font-semibold text-text">危害</h3>
                  <div className="rounded-lg border border-sev-high/30 bg-sev-high/5 p-4">
                    <p className="whitespace-pre-wrap text-sm leading-relaxed text-text">
                      {String(finding.evaluation.impact)}
                    </p>
                  </div>
                </section>
              )}

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

              {/* 证据链三段式：复现步骤 / 机器证据 / 裁决——对齐漏洞报告
                  标准结构（复现可执行、证据可核验、结论可追溯）。 */}
              {(ev.reproCommand || ev.assertReasons.length > 0 || ev.exploit || ev.judgeReasoning) && (
                <section>
                  <div className="mb-3 flex items-center justify-between">
                    <h3 className="text-sm font-semibold text-text">证据链</h3>
                    {ev.tier && (
                      <Badge
                        size="sm"
                        dot={false}
                        color={ev.tier === 'full_gate' ? 'var(--status-success)' : 'var(--status-warning)'}
                      >
                        {ev.tier === 'full_gate' ? '机器复放 + 终裁' : ev.tier === 'judge_only' ? '仅终裁' : ev.tier}
                      </Badge>
                    )}
                  </div>
                  <div className="space-y-3">
                    {/* 复现步骤：curl 等价命令，可复制直接重放 */}
                    {ev.reproCommand && (
                      <div className="rounded-lg border border-border bg-surface-2 p-4">
                        <div className="mb-2 flex items-center justify-between">
                          <span className="text-sm font-medium text-text">复现命令</span>
                          <CopyButton
                            copied={copiedKey === 'repro'}
                            onClick={() => copy('repro', ev.reproCommand!)}
                            size="sm"
                          />
                        </div>
                        <pre tabIndex={0} className="overflow-x-auto rounded-lg bg-background p-3 text-xs leading-relaxed text-text">
                          {ev.reproCommand}
                        </pre>
                        {ev.baseline && (
                          <p className="mt-2 text-xs text-text-tertiary">
                            对照基线：<span className="font-mono">{ev.baseline.url}</span>
                            {ev.baseline.status != null && `（HTTP ${ev.baseline.status}）`}——基线不带利用参数，
                            差分即漏洞特征
                          </p>
                        )}
                      </div>
                    )}

                    {/* 机器证据：断言明细 + exploit 响应 */}
                    {(ev.assertReasons.length > 0 || ev.exploit) && (
                      <div className="rounded-lg border border-border bg-surface-2 p-4">
                        <span className="mb-2 block text-sm font-medium text-text">机器证据</span>
                        {ev.exploit && (
                          <div className="mb-2 flex flex-wrap items-baseline gap-x-3 gap-y-1 font-mono text-xs">
                            <Badge size="sm" dot={false} color={ev.exploit.status === 200 ? 'var(--status-success)' : 'var(--status-warning)'}>
                              {ev.exploit.method} {ev.exploit.status ?? '—'}
                            </Badge>
                            <span className="break-all text-text">{ev.exploit.url}</span>
                          </div>
                        )}
                        {ev.exploit?.bodySnippet && (
                          <pre tabIndex={0} className="mb-2 overflow-x-auto rounded-lg bg-background p-3 text-xs leading-relaxed text-text-secondary">
                            {ev.exploit.bodySnippet}
                          </pre>
                        )}
                        {ev.assertReasons.length > 0 && (
                          <ul className="space-y-1">
                            {ev.assertReasons.map((r, i) => (
                              <li key={i} className="flex items-start gap-1.5 text-xs leading-relaxed text-text-secondary">
                                <span className="text-sev-info">▸</span>
                                <span className="font-mono">{r}</span>
                              </li>
                            ))}
                          </ul>
                        )}
                      </div>
                    )}

                    {/* 裁决：为什么判定成立（LLM 终裁理由，引用亲见差分） */}
                    {ev.judgeReasoning && (
                      <div className="rounded-lg border border-border bg-surface-2 p-4">
                        <span className="mb-2 block text-sm font-medium text-text">裁决理由</span>
                        <p className="whitespace-pre-wrap text-sm leading-relaxed text-text-secondary">
                          {ev.judgeReasoning}
                        </p>
                      </div>
                    )}
                  </div>
                </section>
              )}

              {/* 处置区域 */}
              <section>
                <h3 className="mb-3 text-sm font-semibold text-text">漏洞处置</h3>
                <div className="space-y-4 rounded-lg border border-border bg-surface-2 p-4">
                  <div>
                    <label htmlFor={`${fieldId}-status`} className="mb-2 block text-sm font-medium text-text">
                      状态
                    </label>
                    <Select
                      id={`${fieldId}-status`}
                      value={status}
                      onChange={(e) => setStatus(e.target.value)}
                      className="w-full"
                    >
                      {FINDING_STATUS_OPTIONS.map((opt) => (
                        <option key={opt.value} value={opt.value}>
                          {opt.label}
                        </option>
                      ))}
                    </Select>
                  </div>

                  <div>
                    <label htmlFor={`${fieldId}-severity`} className="mb-2 block text-sm font-medium text-text">
                      严重度
                    </label>
                    <Select
                      id={`${fieldId}-severity`}
                      value={severity}
                      onChange={(e) => setSeverity(e.target.value)}
                      className="w-full"
                    >
                      {SEVERITY_OPTIONS.map((sev) => (
                        <option key={sev} value={sev}>
                          {severityLabel[sev] || sev}
                        </option>
                      ))}
                    </Select>
                  </div>

                  <div>
                    <label htmlFor={`${fieldId}-note`} className="mb-2 block text-sm font-medium text-text">
                      备注
                    </label>
                    <textarea
                      id={`${fieldId}-note`}
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
            <Dialog.Close asChild>
              <Button variant="secondary">取消</Button>
            </Dialog.Close>
            <Button disabled={!changed} onClick={handleSave}>
              保存处置
            </Button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
