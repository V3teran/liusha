import * as Dialog from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import type { TrafficDetail } from '@/api/types'
import { CopySection } from '@/components/CopySection'
import { Badge } from '@/components/ui/badge'
import { useCopyToClipboard } from '@/hooks/useCopyToClipboard'
import { fullTime, humanDuration } from '@/lib/format'
import { methodColor, statusColor } from '@/lib/httpStatus'

interface TrafficDrawerProps {
  open: boolean
  detail: TrafficDetail | null
  loading: boolean
  error: string
  onOpenChange: (open: boolean) => void
}

// header map（header→值列表）拍平成规范的多行文本，供展示/复制。结构不符时退化为 JSON。
function headersToText(h: unknown): string {
  if (h == null || typeof h !== 'object') return ''
  const entries = Object.entries(h as Record<string, unknown>)
  if (entries.length === 0) return ''
  return entries
    .map(([k, v]) => {
      const val = Array.isArray(v) ? v.join(', ') : String(v)
      return `${k}: ${val}`
    })
    .join('\n')
}

const PRE_CLASS =
  'max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-border bg-surface-2 px-2.5 py-2 font-mono text-[11.5px] leading-relaxed text-text'

// 流量详情抽屉：点列表某行 → 右侧滑出，展示元信息 KV + 请求/响应 headers + body 原文。
// proxy_traffic 是只读的取证数据，无编辑操作；body 已在落库时按 32 KiB 截断。
export function TrafficDrawer({ open, detail, loading, error, onOpenChange }: TrafficDrawerProps) {
  const { copiedKey, copy } = useCopyToClipboard()

  const statusHue = detail ? statusColor(detail.status_code) : 'var(--muted)'
  const reqHeaders = detail ? headersToText(detail.request_headers) : ''
  const respHeaders = detail ? headersToText(detail.response_headers) : ''

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Content className="fixed inset-y-0 right-0 z-50 flex h-full w-full flex-col bg-surface shadow-2xl focus:outline-none sm:max-w-[640px]">
          <Dialog.Title className="sr-only">流量详情</Dialog.Title>
          <div
            className="border-b border-border border-l-4 px-5 pb-3.5 pt-4"
            style={{ borderLeftColor: statusHue, background: `linear-gradient(90deg, ${statusHue}14, transparent 60%)` }}
          >
            <div className="flex items-center gap-2.5">
              {detail && (
                <Badge dot={false} color={methodColor(detail.method)}>
                  {detail.method}
                </Badge>
              )}
              {detail && (
                <span className="font-mono text-sm font-bold tabular-nums" style={{ color: statusHue }}>
                  {detail.status_code || '—'}
                </span>
              )}
              <span className="text-[12.5px] text-muted">{detail ? humanDuration(detail.duration_ms) : ''}</span>
              <Dialog.Close className="ml-auto text-muted hover:text-text" aria-label="关闭">
                <X className="h-4 w-4" aria-hidden="true" />
              </Dialog.Close>
            </div>
            {detail && (
              <div className="mt-2 break-all font-mono text-[12.5px] text-text">
                {detail.method} {detail.url || `${detail.scheme}://${detail.host}${detail.path}`}
              </div>
            )}
          </div>

          <div className="flex flex-1 min-h-0 flex-col gap-5.5 overflow-y-auto px-5 py-4.5">
            {loading ? (
              <div className="py-10 text-center text-[13px] text-muted">加载中…</div>
            ) : error ? (
              <div className="py-10 text-center text-[13px] text-sev-critical">⚠ {error}</div>
            ) : detail ? (
              <>
                {/* 元信息：语义化定义列表，屏幕阅读器能理解字段-值对应关系。 */}
                <section>
                  <h3 className="mb-2.5 text-[13px] font-semibold text-text">元信息</h3>
                  <dl className="grid grid-cols-[92px_1fr] items-baseline gap-x-3 gap-y-1.5">
                    <dt className="text-xs text-muted">主机</dt>
                    <dd className="break-all font-mono text-[12.5px] text-text">{detail.host}</dd>
                    <dt className="text-xs text-muted">路径</dt>
                    <dd className="break-all font-mono text-[12.5px] text-text">{detail.path || '—'}</dd>
                    <dt className="text-xs text-muted">时间</dt>
                    <dd className="font-mono text-[12.5px] text-text">{fullTime(detail.captured_at)}</dd>
                    <dt className="text-xs text-muted">耗时</dt>
                    <dd className="font-mono text-[12.5px] text-text">{humanDuration(detail.duration_ms)}</dd>
                    <dt className="text-xs text-muted">消费任务</dt>
                    <dd className="break-all font-mono text-[12.5px] text-text">
                      {detail.consumed_by_task_id || <span className="text-muted opacity-60">未消费</span>}
                    </dd>
                  </dl>
                </section>

                {/* 请求 headers */}
                <CopySection
                  title="请求头"
                  copyText={reqHeaders || undefined}
                  copied={copiedKey === 'reqh'}
                  onCopy={() => void copy('reqh', reqHeaders)}
                >
                  {reqHeaders ? <pre className={PRE_CLASS}>{reqHeaders}</pre> : <div className="text-[12.5px] text-muted opacity-70">无请求头</div>}
                </CopySection>

                {/* 请求 body */}
                <CopySection
                  title="请求体"
                  copyText={detail.request_body || undefined}
                  copied={copiedKey === 'reqb'}
                  onCopy={() => void copy('reqb', detail.request_body)}
                >
                  {detail.request_body ? (
                    <pre className={PRE_CLASS}>{detail.request_body}</pre>
                  ) : (
                    <div className="text-[12.5px] text-muted opacity-70">无请求体</div>
                  )}
                </CopySection>

                {/* 响应 headers */}
                <CopySection
                  title="响应头"
                  copyText={respHeaders || undefined}
                  copied={copiedKey === 'resh'}
                  onCopy={() => void copy('resh', respHeaders)}
                >
                  {respHeaders ? <pre className={PRE_CLASS}>{respHeaders}</pre> : <div className="text-[12.5px] text-muted opacity-70">无响应头</div>}
                </CopySection>

                {/* 响应 body */}
                <CopySection
                  title="响应体"
                  copyText={detail.response_body || undefined}
                  copied={copiedKey === 'resb'}
                  onCopy={() => void copy('resb', detail.response_body)}
                >
                  {detail.response_body ? (
                    <pre className={PRE_CLASS}>{detail.response_body}</pre>
                  ) : (
                    <div className="text-[12.5px] text-muted opacity-70">无响应体</div>
                  )}
                </CopySection>
              </>
            ) : null}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
