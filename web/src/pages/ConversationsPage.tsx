import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { ConversationDetail } from '@/features/conversation/ConversationDetail'
import { ConversationList, type ConversationListHandle } from '@/features/conversation/ConversationList'

interface ConversationsPageProps {
  mode: 'active' | 'passive'
}

// 对话页：主从双栏——左 ConversationList + 右 ConversationDetail。mode 由路由决定
// （/conversations/active、/conversations/passive 共用本组件），active/passive 两个 tab
// 差异仅在列表文案/是否可发起新对话，详情区的 SSE/用量/状态/插话全在 ConversationDetail 内。
export function ConversationsPage({ mode }: ConversationsPageProps) {
  const [searchParams] = useSearchParams()
  const [currentConv, setCurrentConv] = useState('')
  const convListRef = useRef<ConversationListHandle>(null)

  // 主动 tab 下：从其他页跳来（?conv=xxx）时自动打开该会话（实时观察 + 插话）。
  const queryConv = searchParams.get('conv')
  useEffect(() => {
    if (mode === 'active' && queryConv) setCurrentConv(queryConv)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, queryConv])

  const newConversation = () => setCurrentConv('') // 空=新建态，ConversationDetail 露空状态 + 可发起 Composer（仅 active）

  // 新会话建立：切到它 + 刷左列表（否则新会话不出现，要手动点 ↻）。
  const onStarted = (convID: string) => {
    setCurrentConv(convID)
    void convListRef.current?.refresh()
  }
  // 删除的若是当前打开会话→清空选中；删别的不影响当前视图。
  const onConvDeleted = (convID: string) => {
    if (convID === currentConv) setCurrentConv('')
  }

  const heading = mode === 'passive' ? '流量批次' : undefined
  const emptyHint =
    mode === 'passive' ? '暂无流量分析对话——挂代理（passive 8888）收到流量后自动逐批分析' : undefined

  return (
    <div className="grid h-full min-h-0 grid-cols-[304px_1fr]">
      <div className="flex h-full min-h-0 flex-col border-r border-border p-2.5">
        <ConversationList
          ref={convListRef}
          activeId={currentConv || undefined}
          mode={mode}
          allowNew={mode === 'active'}
          heading={heading}
          emptyHint={emptyHint}
          onSelect={setCurrentConv}
          onNew={newConversation}
          onDeleted={onConvDeleted}
        />
      </div>
      <ConversationDetail
        convId={currentConv || undefined}
        mode={mode}
        onStarted={onStarted}
        onRunningChanged={() => void convListRef.current?.refresh()}
      />
    </div>
  )
}
