import { useEffect, useState } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import type { FindingRow } from '@/api/types'
import { severityColor, severityLabel } from '@/lib/severity'
import { FINDING_STATUS_OPTIONS, findingStatusMeta } from '@/lib/findingStatus'
import { fullTime } from '@/lib/format'
import { Badge } from '@/components/ui/badge'
import { CopyButton } from '@/components/CopyButton'
import { useCopyToClipboard } from '@/hooks/useCopyToClipboard'

interface FindingDrawerProps {
  open: boolean
  finding: FindingRow | null
  onOpenChange: (open: boolean) => void
  onSave: (payload: { id: string; status: string; severity: string; note: string }) => void
}

const SEVERITY_OPTIONS = ['critical', 'high', 'medium', 'low', 'info']

// evidence key → 人类可读中文标签（常见 key 映射，未知 key 原样）。
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

// evidence 展示优先级：先复现（命令/步骤/payload），再观察，再结论/影响，其余未列举 key 排最后。
// 原实现按 Object.entries 原始插入顺序渲染——顺序跟数据库/LLM 写入顺序走，不同 finding 间
// 展示顺序不一致，用户体验上"信息类别"跳来跳去。改成固定语义顺序后，任意 finding 都遵循
// 同一套"先怎么复现、再看到了什么、最后结论是什么"的阅读节奏。
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

// evidence 分类渲染：命令类 key（含 cmd/命令）用代码块 + 复制；对象/数组 JSON 折行；其余纯文本。
function buildEvidenceItems(evidence?: Record<string, unknown>): EvItem[] {
  if (!evidence) return []
  const items: EvItem[] = []
  for (const [k, raw] of Object.entries(evidence)) {
    if (raw == null || raw === '') continue
    let kind: EvItem['kind'] = 'text'
    let value: string
    if (typeof raw === 'object') {
      kind = 'json'
      value = JSON.stringify(raw, null, 2)
    } else {
      value = String(raw)
      // 命令/请求类 → 等宽代码块（便于复制复现）
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

// 漏洞详情抽屉：点台账某行→右侧滑出，展示完整 evidence(PoC/复现命令/观察)、修复建议、
// CWE/OWASP、聚合扫描次数，并支持 triage（状态 + 备注，两者解耦可单独存）。
export function FindingDrawer({ open, finding, onOpenChange, onSave }: FindingDrawerProps) {
  const [editStatus, setEditStatus] = useState('open')
  const [editSeverity, setEditSeverity] = useState('info')
  const [editNote, setEditNote] = useState('')
  const { copiedKey, copy } = useCopyToClipboard()

  // 本地编辑态：抽屉打开/切换 finding 时，从当前 finding 初始化。
  useEffect(() => {
    setEditStatus(finding?.status ?? 'open')
    setEditSeverity(finding?.severity ?? 'info')
    setEditNote(finding?.triage_note ?? '')
  }, [finding])

  const dirty =
    !!finding &&
    (editStatus !== finding.status || editSeverity !== finding.severity || editNote !== (finding.triage_note ?? ''))

  const sevColorVar = severityColor[(finding?.severity ?? 'info').toLowerCase()] ?? '#6e7681'
  const sevLabelText = severityLabel[(finding?.severity ?? '').toLowerCase()] || finding?.severity || ''
  const statusMeta = findingStatusMeta(editStatus)
  const evidenceItems = buildEvidenceItems(finding?.evidence)
  // 定位行（方法 + host + path）：渗透测试场景下最常需要复制去粘贴到 curl/Burp 复现的目标 URL。
  const targetLine = finding ? [finding.target?.method, finding.host, finding.target?.path].filter(Boolean).join(' ') : ''

  const handleSave = () => {
    if (!finding || !dirty) return
    onSave({ id: finding.id, status: editStatus, severity: editSeverity, note: editNote })
  }

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Content className="fixed inset-y-0 right-0 z-50 flex h-full w-[560px] flex-col bg-surface shadow-2xl focus:outline-none">
          <Dialog.Title className="sr-only">漏洞详情</Dialog.Title>
          {finding && (
            <>
              {/* 头部：severity + summary + 模式 + 定位 */}
              <div
                className="border-b border-border border-l-4 px-5 pb-4 pt-4.5"
                style={{
                  borderLeftColor: sevColorVar,
                  background: `linear-gradient(90deg, ${sevColorVar}14, transparent 60%)`,
                }}
              >
                <div className="flex items-center gap-2.5">
                  <span
                    className="rounded-full px-3 py-0.5 text-xs font-bold tracking-wide text-white"
                    style={{ background: sevColorVar }}
                  >
                    {sevLabelText}
                  </span>
                  <Badge
                    dot={false}
                    color={finding.source === 'auto' ? 'var(--source-auto)' : 'var(--source-manual)'}
                  >
                    {finding.source === 'auto' ? '被动代理' : '主动下发'}
                  </Badge>
                  <Dialog.Close
                    aria-label="关闭"
                    className="ml-auto text-muted hover:text-text"
                  >
                    <X className="h-4 w-4" />
                  </Dialog.Close>
                </div>
                <h2 className="mb-2 mt-3 text-base font-semibold leading-relaxed text-text">{finding.summary}</h2>
                <div className="flex items-start gap-2">
                  <div className="break-all font-mono text-[12.5px] text-muted">
                    {finding.target?.method && (
                      <span className="mr-1.5 font-bold text-accent">{finding.target.method}</span>
                    )}
                    {finding.host}
                    {finding.target?.path}
                  </div>
                  {/* 定位行复制：渗透测试场景下最常需要粘贴到 curl/Burp 去复现的目标 URL。 */}
                  <CopyButton compact copied={copiedKey === 'target'} onClick={() => void copy('target', targetLine)} className="mt-0.5" />
                </div>
              </div>

              <div className="flex flex-1 min-h-0 flex-col gap-5.5 overflow-y-auto px-5 py-4.5">
                {/* Triage 区 */}
                <section className="rounded-xl bg-surface-2 p-3.5">
                  <h3 className="mb-2.5 text-[13px] font-semibold text-text">处置</h3>
                  <div className="mb-2.5 flex items-center gap-2">
                    <label className="w-11 flex-shrink-0 text-xs text-muted">状态</label>
                    <span className="h-2.5 w-2.5 flex-shrink-0 rounded-full" style={{ background: statusMeta.color }} />
                    <select
                      value={editStatus}
                      onChange={(e) => setEditStatus(e.target.value)}
                      className="w-36 rounded-md border border-border bg-surface px-2 py-1 text-sm text-text outline-none focus:border-accent"
                    >
                      {FINDING_STATUS_OPTIONS.map((o) => (
                        <option key={o.value} value={o.value}>
                          {o.label}
                        </option>
                      ))}
                    </select>
                  </div>
                  <div className="mb-2.5 flex items-center gap-2">
                    <label className="w-11 flex-shrink-0 text-xs text-muted">严重度</label>
                    <select
                      value={editSeverity}
                      onChange={(e) => setEditSeverity(e.target.value)}
                      className="w-36 rounded-md border border-border bg-surface px-2 py-1 text-sm text-text outline-none focus:border-accent"
                    >
                      {SEVERITY_OPTIONS.map((s) => (
                        <option key={s} value={s}>
                          {s}
                        </option>
                      ))}
                    </select>
                    <span className="text-[11px] text-muted">可覆盖扫描定级</span>
                  </div>
                  <textarea
                    value={editNote}
                    onChange={(e) => setEditNote(e.target.value)}
                    rows={2}
                    placeholder="处置备注（如误报原因、修复责任人、验证方式…）"
                    className="w-full rounded-md border border-border bg-surface px-2.5 py-1.5 text-[13px] text-text outline-none focus:border-accent"
                  />
                  <div className="mt-2.5 flex items-center justify-between gap-3">
                    {finding.triaged_at && (
                      <span className="font-mono text-[11.5px] text-muted">最后处置 {fullTime(finding.triaged_at)}</span>
                    )}
                    <button
                      type="button"
                      disabled={!dirty}
                      onClick={handleSave}
                      className="ml-auto rounded-lg bg-accent px-4.5 py-1.5 text-[13px] text-white hover:bg-accent-hover disabled:cursor-default disabled:opacity-40"
                    >
                      保存
                    </button>
                  </div>
                </section>

                {/* Evidence */}
                {evidenceItems.length > 0 && (
                  <section>
                    <h3 className="mb-2.5 flex items-baseline gap-2 text-[13px] font-semibold text-text">
                      证据 / 复现<span className="text-[11px] font-normal text-muted">{evidenceItems.length} 项</span>
                    </h3>
                    {evidenceItems.map((item) => (
                      <div key={item.key} className="mb-3 last:mb-0">
                        <div className="mb-1 flex items-center justify-between">
                          <span className="text-xs font-semibold text-muted">{EV_LABELS[item.key] || item.key}</span>
                          {/* code（命令/curl）与 json（数组/对象证据，如受影响端点列表）都是代码块展示，
                              同样需要复制——之前只判 code，json 块被漏掉了复制入口。 */}
                          {(item.kind === 'code' || item.kind === 'json') && (
                            <CopyButton copied={copiedKey === item.key} onClick={() => void copy(item.key, item.value)} />
                          )}
                        </div>
                        {item.kind === 'code' || item.kind === 'json' ? (
                          <pre className="whitespace-pre-wrap break-all rounded-lg border border-border bg-surface-2 px-3 py-2.5 font-mono text-xs leading-relaxed text-text">
                            {item.value}
                          </pre>
                        ) : (
                          <div className="whitespace-pre-wrap text-[13px] leading-relaxed text-text">{item.value}</div>
                        )}
                      </div>
                    ))}
                  </section>
                )}

                {/* 修复建议 */}
                {finding.remediation && (
                  <section>
                    <h3 className="mb-2.5 text-[13px] font-semibold text-text">修复建议</h3>
                    <div className="rounded-lg border-l-3 border-sev-info bg-sev-info/10 px-3.5 py-3 text-[13px] leading-relaxed text-text">
                      {finding.remediation}
                    </div>
                  </section>
                )}

                {/* 元信息 */}
                <section>
                  <h3 className="mb-2.5 text-[13px] font-semibold text-text">元信息</h3>
                  <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-[12.5px]">
                    {finding.cwe_id && (
                      <>
                        <span className="text-muted">CWE</span>
                        <span className="font-mono text-text">{finding.cwe_id}</span>
                      </>
                    )}
                    {finding.owasp_category && (
                      <>
                        <span className="text-muted">OWASP</span>
                        <span className="font-mono text-text">{finding.owasp_category}</span>
                      </>
                    )}
                    <span className="text-muted">首次发现</span>
                    <span className="font-mono text-text">{fullTime(finding.created_at)}</span>
                  </div>
                </section>
              </div>
            </>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
