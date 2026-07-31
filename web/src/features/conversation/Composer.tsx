import { useState } from 'react'
import { Send, Square } from 'lucide-react'
import { followUp, startChat } from '@/api/client'
import { useConversationStore } from '@/stores/conversation'
import { RolePicker } from './RolePicker'

interface ComposerProps {
  convId?: string
  scanning?: boolean
  mode?: 'active' | 'passive'
  onStarted: (convID: string) => void
  onAppended: (afterSeq: number) => void
  onStop: () => void
}

// 发起器：选角色 + 写 brief。
// 有 convId 走追加（followUp），否则新建会话（startChat）并向上抛新会话 ID。
//
// 追加发送不再由前端按 scanning 一刀切拦截——passive 会话的 task 往往长期 active（持续收流量），
// 若在此按 scanning 拦，QA 类追问（passive 下最常见用法）会被永久锁死。真正该拦的只是「意图为
// action 且扫描在跑」，这个判断后端已经做了（HandleMessage 先判意图，仅 action+busy 才 409）。
// 前端一律放行，交给后端按 409 busy 精确拒绝，qa 类追问随时可用。
export function Composer({ convId, scanning, mode, onStarted, onAppended, onStop }: ComposerProps) {
  const lastSeq = useConversationStore((s) => s.lastSeq)
  const [brief, setBrief] = useState('')
  const [roleID, setRoleID] = useState('')
  const [busyMsg, setBusyMsg] = useState('')
  const [sending, setSending] = useState(false)

  const send = async () => {
    if (!brief.trim() || sending) return
    setBusyMsg('')
    setSending(true)
    try {
      if (convId) {
        // followUp 前快照 seq——user 消息 seq 必 > 此（发送后才新增）；后续 SSE 推高 lastSeq 不影响此快照值。
        const beforeSeq = lastSeq
        const r = await followUp(convId, brief)
        setBrief('')
        setBusyMsg(r.intent === 'qa' ? '正在回答…' : '已触发扫描')
        onAppended(beforeSeq)
      } else {
        const { conversation_id } = await startChat(brief, roleID)
        setBrief('')
        onStarted(conversation_id)
      }
    } catch (e) {
      const err = e as Error & { busy?: boolean }
      setBusyMsg(err.busy ? '扫描进行中，先点停止再发' : '发送失败')
    } finally {
      setSending(false)
    }
  }

  // Enter 发送、Shift+Enter 换行——聊天类输入框的通行范式（Slack/Discord/Linear 评论框），
  // 比原来的 Cmd/Ctrl+Enter 更符合直觉（后者常见于表单提交场景，不是聊天场景）。
  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      void send()
    }
  }

  const placeholder = convId
    ? mode === 'passive'
      ? '针对这批流量继续提问…（Enter 发送，Shift+Enter 换行）'
      : '追加指令（在同一目标上继续）…（Enter 发送，Shift+Enter 换行）'
    : '描述要扫的目标 / 任务（URL、账号、测试方向）…'

  return (
    <div className="flex flex-shrink-0 flex-col gap-2.5 border-t border-border bg-surface px-4 py-3.5">
      {!convId && (
        <div className="flex items-center gap-3">
          <RolePicker value={roleID} onChange={setRoleID} mode="active" />
          <span className="text-xs text-muted">选择场景，Enter 发送，Shift+Enter 换行</span>
        </div>
      )}
      <div className="rounded-lg border border-border bg-background transition-colors focus-within:border-accent">
        <textarea
          value={brief}
          onChange={(e) => setBrief(e.target.value)}
          onKeyDown={onKeyDown}
          placeholder={placeholder}
          className="w-full min-h-[56px] max-h-[200px] resize-y bg-transparent px-3 pt-3 text-sm leading-relaxed text-text outline-none"
        />
        <div className="flex items-center justify-end gap-3 px-2.5 py-2">
          {busyMsg && <span className="text-[12.5px] text-muted">{busyMsg}</span>}
          {convId && scanning && (
            <button
              type="button"
              onClick={onStop}
              className="inline-flex items-center gap-1.5 rounded-lg border border-sev-critical px-3.5 py-2 text-[13px] font-semibold text-sev-critical hover:bg-sev-critical/15"
            >
              <Square className="h-3.5 w-3.5 fill-current" />
              停止扫描
            </button>
          )}
          <button
            type="button"
            disabled={!brief.trim() || sending}
            onClick={() => void send()}
            className="inline-flex items-center gap-1.5 rounded-lg bg-accent px-4.5 py-2 text-[13.5px] font-semibold text-white hover:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-50"
          >
            {sending ? (
              '发送中…'
            ) : (
              <>
                <Send className="h-3.5 w-3.5" />
                {convId ? '追加' : '发起扫描'}
              </>
            )}
          </button>
        </div>
      </div>
    </div>
  )
}
