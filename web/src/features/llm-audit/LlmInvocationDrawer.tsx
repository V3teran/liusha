import * as Dialog from '@radix-ui/react-dialog'
import { ListChecks, MessageSquare, X } from 'lucide-react'
import { JumpLink } from '@/components/JumpLink'
import type { LLMInvocationDetail } from '@/api/types'
import { CopyButton } from '@/components/CopyButton'
import { CopySection } from '@/components/CopySection'
import { useCopyToClipboard } from '@/hooks/useCopyToClipboard'
import { agentAccent, agentLabel } from '@/lib/agentColor'
import { cacheRatePct, fullTime, humanDuration, humanTokens } from '@/lib/format'
import { contentToText, extractReasoning, msgColor, pickField, toMsgView } from './llmMessage'
import { invocationStatus } from './llmInvocationStatus'
import { isTimeout, throughputLabel } from './llmTiming'
import { toToolCalls } from './toolCalls'
import { ToolCallCard } from './ToolCallCard'
import { conversationPath } from '@/lib/source'

interface LlmInvocationDrawerProps {
  open: boolean
  detail: LLMInvocationDetail | null
  loading: boolean
  error: string
  llmTimeoutMs?: number // 单步 LLM 看门狗（runtime 配置现读；超时判定同源）
  onOpenChange: (open: boolean) => void
}

// LLM 调用详情抽屉：点审计表某行 → 右侧滑出，展示元信息 KV + 输入消息 + 返回结果原文。
// messages 是 OpenAI 风格消息数组，按条渲染（role 标签 + 正文）；结构不符时退化为 JSON 展示。
// 解析逻辑（contentToText/toMsgView/reasoning 提取）已抽到同目录 llmMessage.ts 独立测试；
// 复制状态机抽到 useCopyToClipboard；重复的「标题+复制按钮+内容」结构抽到 CopySection。
export function LlmInvocationDrawer({ open, detail, loading, error, llmTimeoutMs, onOpenChange }: LlmInvocationDrawerProps) {
  const { copiedKey, copy } = useCopyToClipboard()

  // messages 是「每轮完整重发的对话历史快照」——第 N 次调用的 messages 几乎完整包含第 N-1 次的。
  // 这里只取本次增量：末条消息即"这次新喂进去的东西"。完整历史可「复制完整上下文」取走。
  const rawMessages = detail?.messages
  const inputDelta = Array.isArray(rawMessages) && rawMessages.length > 0 ? toMsgView(rawMessages[rawMessages.length - 1]) : null
  const historyCount = Array.isArray(rawMessages) ? rawMessages.length : 0

  // result 解析带键兼容（pickField）：新行是小写 json tag 键，历史行是 Go 大写键，
  // 两代数据在抽屉里同等结构化渲染，不再整体退化为原始 JSON。
  // result 为纯字符串（jsonb 兜底形态）时原样展示。
  const rawResult = detail?.result
  const resultText =
    typeof rawResult === 'string' ? rawResult : contentToText(pickField(rawResult, ['content', 'Content']))
  const resultCalls = toToolCalls(
    pickField(rawResult, ['tool_calls', 'ToolCalls']) ?? pickField(rawResult, ['function_call', 'FunctionCall']),
  )
  // 「返回结果」节的复制：只复制本节展示的东西（正文 + 工具调用）。
  const resultCopyText = [resultText, ...resultCalls.map((tc) => (tc.args ? `${tc.name}(${tc.args})` : `${tc.name}()`))]
    .filter(Boolean)
    .join('\n\n')

  const reasoning = extractReasoning(rawResult)

  // 兜底：result 既无 content 也无 tool_calls 时，整体 JSON 展示。
  const resultRaw = !resultText && resultCalls.length === 0 && rawResult != null ? JSON.stringify(rawResult, null, 2) : ''
  const messagesRaw = !inputDelta && rawMessages != null ? JSON.stringify(rawMessages, null, 2) : ''

  const { accent: roleColor } = agentAccent(detail?.role)
  const status = detail ? invocationStatus(detail, llmTimeoutMs) : null

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="app-overlay fixed inset-0 z-50" />
        <Dialog.Content className="fixed inset-y-0 right-0 z-50 flex h-full w-full flex-col bg-surface shadow-2xl focus:outline-none sm:max-w-[620px]">
          <Dialog.Title className="sr-only">LLM 调用详情</Dialog.Title>
          <div
            className="border-b border-border border-l-4 px-5 pb-3.5 pt-4"
            style={{ borderLeftColor: roleColor, background: `linear-gradient(90deg, ${roleColor}14, transparent 60%)` }}
          >
            <div className="flex items-center gap-2.5">
              <span className="text-sm font-bold" style={{ color: roleColor }}>
                {detail ? agentLabel(detail.role) || detail.role || '调用详情' : '调用详情'}
              </span>
              {status ? (
                <span
                  className="rounded px-2 py-0.5 text-xs font-semibold"
                  style={{
                    color: `var(--sev-${status.level === 'critical' ? 'critical' : 'medium'})`,
                    background: `color-mix(in srgb, var(--sev-${status.level === 'critical' ? 'critical' : 'medium'}) 14%, transparent)`,
                  }}
                  title={status.detail}
                >
                  {status.label}
                </span>
              ) : detail ? (
                <span className="rounded bg-surface-2 px-2 py-0.5 text-xs font-semibold text-text-secondary">{detail.finish_reason || '完成'}</span>
              ) : null}
              <Dialog.Close className="ml-auto text-text-secondary hover:text-text" aria-label="关闭">
                <X className="h-4 w-4" aria-hidden="true" />
              </Dialog.Close>
            </div>
            {detail && (
              <div className="mt-2 font-mono text-sm text-text">
                {detail.model}
                <span className="text-text-secondary"> · {detail.provider}</span>
              </div>
            )}
          </div>

          <div className="flex flex-1 min-h-0 flex-col gap-6 overflow-y-auto px-5 py-4">
            {loading ? (
              <div className="py-10 text-center text-sm text-text-secondary">加载中…</div>
            ) : error ? (
              <div className="py-10 text-center text-sm text-sev-critical">⚠ {error}</div>
            ) : detail ? (
              <>
                {/* 元信息：语义化定义列表，屏幕阅读器能理解字段-值对应关系。 */}
                <section>
                  <h3 className="mb-2.5 text-sm font-semibold text-text">元信息</h3>
                  <dl className="grid grid-cols-[84px_1fr] items-baseline gap-x-3 gap-y-1.5">
                    <dt className="text-xs text-text-secondary">request_id</dt>
                    <dd className="flex items-center gap-2 break-all font-mono text-sm text-text">
                      {detail.request_id}
                      <CopyButton copied={copiedKey === 'rid'} onClick={() => void copy('rid', detail.request_id)} />
                    </dd>
                    <dt className="text-xs text-text-secondary">时间</dt>
                    <dd className="font-mono text-sm text-text">{fullTime(detail.created_at)}</dd>
                    <dt className="text-xs text-text-secondary">Tokens</dt>
                    <dd className="font-mono text-sm text-text">
                      输入 {humanTokens(detail.in_tokens)} · 输出 {humanTokens(detail.out_tokens)}
                      {detail.cached_tokens > 0 && (
                        <>
                          {' · 缓存 '}
                          {humanTokens(detail.cached_tokens)}
                          {detail.in_tokens + detail.cached_tokens > 0 && (
                            <span className="font-semibold text-status-warning">
                              {' ('}
                              {cacheRatePct(detail.in_tokens, detail.cached_tokens).toFixed(1)}
                              {'%)'}
                            </span>
                          )}
                        </>
                      )}
                    </dd>
                    <dt className="text-xs text-text-secondary">响应耗时</dt>
                    <dd className="font-mono text-sm text-text">
                      总时长 {humanDuration(detail.latency_ms)}
                      {isTimeout(detail, llmTimeoutMs) ? (
                        <span className="font-semibold text-sev-critical"> · 看门狗超时</span>
                      ) : (
                        throughputLabel(detail, llmTimeoutMs) && ` · ${throughputLabel(detail, llmTimeoutMs)}`
                      )}
                    </dd>
                    {detail.agent_run_id && (
                      <>
                        <dt className="text-xs text-text-secondary">agent</dt>
                        <dd className="font-mono text-sm text-text">{detail.agent_run_id}</dd>
                      </>
                    )}
                  </dl>
                </section>

                {/* 跳转：与漏洞详情抽屉同一节式语法（JumpLink 盒）。对话 tab 按下发
                    来源定（见 conversationPath：缺 source 落自主下发 tab——
                    纯聊天/轻量路由调用必归自主侧，代理分析行后端恒带 source）。
                    链接缺失（未链会话的孤儿任务/任务级后台调用）不渲染占位。 */}
                {(detail.task_id || detail.conversation_id) && (
                  <section>
                    <h3 className="mb-2.5 text-sm font-semibold text-text">跳转</h3>
                    <div className="flex flex-wrap gap-2">
                      {detail.task_id && (
                        <JumpLink to={`/tasks?task=${detail.task_id}`} icon={ListChecks} onClick={() => onOpenChange(false)}>
                          任务
                        </JumpLink>
                      )}
                      {detail.conversation_id && (
                        <JumpLink
                          to={conversationPath(detail.source, detail.conversation_id)}
                          icon={MessageSquare}
                          onClick={() => onOpenChange(false)}
                        >
                          对话
                        </JumpLink>
                      )}
                    </div>
                  </section>
                )}

                {/* 错误单独成节，最先看到 */}
                {detail.error_message && (
                  <section>
                    <h3 className="mb-2.5 text-sm font-semibold text-text">错误</h3>
                    <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border px-2.5 py-2 font-mono text-xs leading-relaxed text-sev-critical" style={{ borderColor: 'color-mix(in srgb, var(--sev-critical) 40%, var(--border))' }}>
                      {detail.error_message}
                    </pre>
                  </section>
                )}

                {/* 本次输入增量 */}
                <CopySection
                  title="本次输入"
                  hint={historyCount > 1 ? `增量（上下文共 ${historyCount} 条）` : undefined}
                  copyText={rawMessages != null ? JSON.stringify(rawMessages, null, 2) : undefined}
                  copied={copiedKey === 'msgs'}
                  onCopy={() => void copy('msgs', JSON.stringify(detail.messages, null, 2))}
                >
                  {inputDelta && (
                    <div className="border-l-[3px] pl-2.5" style={{ borderColor: msgColor(inputDelta.role) }}>
                      <div className="mb-1">
                        <span className="text-xs font-bold uppercase tracking-wide" style={{ color: msgColor(inputDelta.role) }}>
                          {inputDelta.role}
                        </span>
                      </div>
                      {inputDelta.text && (
                        <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-border bg-surface-2 px-2.5 py-2 font-mono text-xs leading-relaxed text-text">
                          {inputDelta.text}
                        </pre>
                      )}
                      {inputDelta.calls.map((tc) => (
                        <ToolCallCard key={tc.id} call={tc} />
                      ))}
                      {!inputDelta.text && inputDelta.calls.length === 0 && <div className="text-sm text-text-secondary opacity-70">(空)</div>}
                    </div>
                  )}
                  {messagesRaw && (
                    <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-border bg-surface-2 px-2.5 py-2 font-mono text-xs leading-relaxed text-text">
                      {messagesRaw}
                    </pre>
                  )}
                  {!inputDelta && !messagesRaw && <div className="text-sm text-text-secondary opacity-70">无输入消息</div>}
                </CopySection>

                {/* 模型思考过程 */}
                {reasoning && (
                  <CopySection
                    title="思考过程"
                    hint="推理链"
                    copyText={reasoning}
                    copied={copiedKey === 'rsn'}
                    onCopy={() => void copy('rsn', reasoning)}
                  >
                    <pre
                      className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border-l-[3px] bg-surface-2 px-2.5 py-2 font-mono text-xs leading-relaxed text-text-secondary"
                      style={{ borderLeftColor: 'color-mix(in srgb, var(--accent) 30%, var(--border))' }}
                    >
                      {reasoning}
                    </pre>
                  </CopySection>
                )}

                {/* 返回结果 */}
                <CopySection
                  title="返回结果"
                  copyText={resultCopyText}
                  copied={copiedKey === 'res'}
                  onCopy={() => void copy('res', resultCopyText)}
                >
                  {resultText && (
                    <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-border bg-surface-2 px-2.5 py-2 font-mono text-xs leading-relaxed text-text">
                      {resultText}
                    </pre>
                  )}
                  {resultCalls.length > 0 && (
                    <>
                      <p className="my-2 text-xs text-text-secondary">工具调用 · {resultCalls.length}</p>
                      {resultCalls.map((tc) => (
                        <ToolCallCard key={tc.id} call={tc} />
                      ))}
                    </>
                  )}
                  {resultRaw && (
                    <pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-border bg-surface-2 px-2.5 py-2 font-mono text-xs leading-relaxed text-text">
                      {resultRaw}
                    </pre>
                  )}
                  {!resultText && resultCalls.length === 0 && !resultRaw && (
                    <div className="text-sm text-text-secondary opacity-70">无返回内容</div>
                  )}

                  {/* 原始响应：取证用，含思考过程/extra/response_meta 等完整字段。
                      右对齐——与各节标题行的复制按钮同侧，孤零零留在左下角像误放的。 */}
                  {detail.result != null && (
                    <div className="mt-3 flex justify-end border-t border-dashed border-border pt-2.5">
                      <CopyButton
                        copied={copiedKey === 'raw'}
                        onClick={() => void copy('raw', JSON.stringify(detail.result, null, 2))}
                        className="text-xs opacity-85"
                      />
                    </div>
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
