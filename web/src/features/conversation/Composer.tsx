import { useEffect, useRef, useState } from 'react'
import { FileText, Loader2, Paperclip, Send, X } from 'lucide-react'
import { followUp, startChat } from '@/api/conversations'
import { uploadAttachment } from '@/api/attachments'
import type { AttachmentRef } from '@/api/types'
import { useConversationStore } from '@/stores/conversation'
import { humanBytes } from '@/lib/format'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/Button'

interface ComposerProps {
  convId?: string
  /** 禁用态：框架仍在（聊天界面不随数据缺失消失），输入与发送关闭。 */
  disabled?: boolean
  /** 禁用态的输入框占位文案（缺省走通用文案）。 */
  placeholder?: string
  onStarted: (convID: string) => void
  onAppended: (afterSeq: number) => void
}

/**
 * Composer - 消息输入组件（含附件）：📎 选择即上传（逐个落位），
 * 芯片挂在输入框上方可摘除；发送时随消息提交引用，服务端收口进任务工作区。
 * 会话未创建（新会话首条）时上传落 pending 暂存区，随 startChat 一并认领——
 * 「先传字典再一句话下发」的主流程不打断。条数上界与服务端同源（10 条）：
 * 发送按钮在「上传进行中」禁用——部分已传的引用先发出去会让用户误以为
 * 全部附件随行。
 */
const MAX_ATTACHMENTS = 10
export function Composer({ convId, disabled = false, placeholder, onStarted, onAppended }: ComposerProps) {
  const lastSeq = useConversationStore((s) => s.lastSeq)
  const beginThinking = useConversationStore((s) => s.beginThinking)
  const [brief, setBrief] = useState('')
  const [busyMsg, setBusyMsg] = useState('')
  const [sending, setSending] = useState(false)
  const [attachments, setAttachments] = useState<AttachmentRef[]>([])
  const [uploadingCount, setUploadingCount] = useState(0)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const convIdRef = useRef(convId)
  convIdRef.current = convId

  // 附件引用是会话域资源（服务端按会话合法根校验）：Composer 实例跨会话
  // 复用，切会话必须清芯片——否则 A 会话的引用随 B 会话的消息提交，只会
  // 换来一句「引用非法」。draft 文本不随切会话清空（既有行为，保留）。
  useEffect(() => {
    setAttachments([])
    setBusyMsg('')
  }, [convId])

  const removeAttachment = (path: string) =>
    setAttachments((prev) => prev.filter((a) => a.path !== path))

  // 选择即上传：逐文件落位（成功追加芯片，失败就地报错不中断其余文件）。
  // 上传途中切了会话的迟到引用直接丢弃——落进了旧会话的暂存区，追加到
  // 新会话的芯片只会制造下一次发送的非法引用。
  const onFilesPicked = async (files: FileList | null) => {
    if (!files || files.length === 0) return
    setBusyMsg('')
    setUploadingCount((n) => n + files.length)
    for (const f of Array.from(files)) {
      try {
        const ref = await uploadAttachment(f, convId)
        if (convIdRef.current === convId) setAttachments((prev) => [...prev, ref])
      } catch (e) {
        setBusyMsg((e as Error).message)
      } finally {
        setUploadingCount((n) => n - 1)
      }
    }
    if (fileInputRef.current) fileInputRef.current.value = '' // 允许重选同一文件
  }

  // 发送闸门的单一事实源：按钮与 Enter 都经 send()，上传中/禁用时一律不发——
  // 否则 Enter 会提交不完整的附件集合，迟到的附件还会挂到下一条消息上。
  const canSend = (brief.trim() !== '' || attachments.length > 0) && !sending && !disabled && uploadingCount === 0

  const send = async () => {
    if (!canSend) return
    setBusyMsg('')
    setSending(true)
    try {
      if (convId) {
        const beforeSeq = lastSeq
        await followUp(convId, brief, attachments)
        setBrief('')
        setAttachments([])
        beginThinking()
        onAppended(beforeSeq)
      } else {
        const { conversation_id } = await startChat(brief, attachments)
        setBrief('')
        setAttachments([])
        beginThinking()
        onStarted(conversation_id)
      }
    } catch {
      setBusyMsg('发送失败')
    } finally {
      setSending(false)
    }
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      void send()
    }
  }

  const effectivePlaceholder = placeholder ?? (convId
    ? '继续提问 / 追加指令…'
    : '描述要扫的目标 / 任务（URL、账号、测试方向）…')

  return (
    <div className="flex flex-shrink-0 flex-col gap-3 border-t border-border bg-surface px-5 py-4">
      {/* 输入区域：1px 边框（与全站 INPUT_CLASS/弹层同一权重，border-2 是体系外的异类），
          focus 走 accent + 辉光；错误态整体转红。 */}
      <div
        className={cn(
          'rounded-xl border bg-background transition-all duration-200',
          'focus-within:border-accent focus-within:glow-accent',
          busyMsg ? 'border-status-error' : 'border-border'
        )}
      >
        {attachments.length > 0 && (
          <div className="flex flex-wrap gap-1.5 border-b border-border px-3 pt-2.5">
            {attachments.map((a) => (
              <span
                key={a.path}
                className="inline-flex max-w-[240px] items-center gap-1 rounded-md bg-surface-2 px-2 py-1 text-xs text-text-secondary"
                title={`${a.name}（${humanBytes(a.size)}）· ${a.path}`}
              >
                <FileText className="h-3 w-3 flex-shrink-0 text-text-tertiary" />
                <span className="truncate">{a.name}</span>
                <span className="flex-shrink-0 tabular-nums text-text-tertiary">{humanBytes(a.size)}</span>
                <button
                  type="button"
                  aria-label={`移除附件 ${a.name}`}
                  onClick={() => removeAttachment(a.path)}
                  className="ml-0.5 flex-shrink-0 rounded text-text-tertiary hover:text-status-error"
                >
                  <X className="h-3 w-3" />
                </button>
              </span>
            ))}
            {uploadingCount > 0 && (
              <span className="inline-flex items-center gap-1 px-1 py-1 text-xs text-text-tertiary">
                <Loader2 className="h-3 w-3 animate-spin" /> 上传中…
              </span>
            )}
          </div>
        )}

        <textarea
          disabled={disabled || sending}
          aria-label="消息输入"
          value={brief}
          onChange={(e) => setBrief(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder={effectivePlaceholder}
          className={cn(
            'w-full min-h-[72px] max-h-[240px] resize-y bg-transparent px-4 pt-3.5 pb-2',
            'text-sm leading-relaxed text-text placeholder:text-text-tertiary',
            'outline-none disabled:opacity-50 disabled:cursor-not-allowed'
          )}
        />

        {/* 底部操作栏：状态一行收口，优先级 错误 > 上传中 > 字数 > 快捷键提示。
            📎 附件按钮贴输入（业界 IM 惯例位置），点击即唤起文件选择。 */}
        <div className="flex items-center justify-between gap-3 border-t border-border px-3 py-2.5">
          <div className="flex min-w-0 items-center gap-2">
            <input
              ref={fileInputRef}
              type="file"
              multiple
              aria-label="选择附件文件"
              className="hidden"
              onChange={(e) => void onFilesPicked(e.target.files)}
            />
            <button
              type="button"
              disabled={disabled || sending || attachments.length >= MAX_ATTACHMENTS}
              onClick={() => fileInputRef.current?.click()}
              aria-label="添加附件"
              title={
                attachments.length >= MAX_ATTACHMENTS
                  ? `单条消息最多 ${MAX_ATTACHMENTS} 个附件`
                  : '添加附件（随消息发给 agent，扫描任务可直接读取）'
              }
              className={cn(
                'flex-shrink-0 rounded-md p-1 transition-colors hover:bg-surface-2 hover:text-text',
                disabled || sending || attachments.length >= MAX_ATTACHMENTS
                  ? 'cursor-not-allowed text-text-disabled opacity-50'
                  : 'text-text-secondary'
              )}
            >
              <Paperclip className="h-4 w-4" />
            </button>
            <div className="min-w-0 truncate text-xs text-text-tertiary">
              {busyMsg ? (
                <span className="animate-slide-down font-medium text-status-error">{busyMsg}</span>
              ) : uploadingCount > 0 ? (
                <span>上传中…</span>
              ) : brief.length > 0 ? (
                <span className="tabular-nums">{brief.length} 字符</span>
              ) : (
                <span>
                  <kbd className="rounded bg-surface-2 px-1.5 py-0.5 font-mono text-xs">Enter</kbd> 发送 ·
                  <kbd className="ml-1.5 rounded bg-surface-2 px-1.5 py-0.5 font-mono text-xs">Shift+Enter</kbd> 换行
                </span>
              )}
            </div>
          </div>

          {/* 发送按钮 */}
          <Button disabled={disabled || !canSend} onClick={() => void send()}>
            {sending ? (
              <>
                <Loader2 className="h-4 w-4 animate-spin" />
                发送中
              </>
            ) : (
              <>
                <Send className="h-4 w-4" />
                发送
              </>
            )}
          </Button>
        </div>
      </div>
    </div>
  )
}
