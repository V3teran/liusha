import { PageHeader } from '@/components/PageHeader'
import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { ConversationDetail } from '@/features/conversation/ConversationDetail'
import { ConversationList, type ConversationListHandle } from '@/features/conversation/ConversationList'

interface ConversationsPageProps {
  source: 'self' | 'proxy'
}

// 对话页：主从双栏——左 ConversationList + 右 ConversationDetail。source 由路由决定
// （/conversations/direct、/conversations/proxy 共用本组件），直连下发/代理捕获两个 tab
// 差异仅在列表文案/是否可发起新对话，详情区的 SSE/用量/状态/插话全在 ConversationDetail 内。
export function ConversationsPage({ source }: ConversationsPageProps) {
  const [searchParams] = useSearchParams()
  const [currentConv, setCurrentConv] = useState('')
  const convListRef = useRef<ConversationListHandle>(null)

  // 自主下发 tab 下：从其他页跳来（?conv=xxx）时自动打开该会话（实时观察 + 插话）。
  const queryConv = searchParams.get('conv')
  useEffect(() => {
    // ?conv= 深链接对两个 tab 都生效（流量详情/任务模块等跨页跳转带 conv 打开对应会话）
    if (queryConv) setCurrentConv(queryConv)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [source, queryConv])

  const newConversation = () => setCurrentConv('') // 空=新建态，ConversationDetail 露空状态 + 可发起 Composer（仅 manual）

  // 新会话建立：切到它 + 刷左列表（否则新会话不出现，要手动点 ↻）。
  const onStarted = (convID: string) => {
    setCurrentConv(convID)
    void convListRef.current?.refresh()
  }
  // 删除的若是当前打开会话→清空选中；删别的不影响当前视图。
  const onConvDeleted = (convID: string) => {
    if (convID === currentConv) setCurrentConv('')
  }

  const heading = source === 'proxy' ? '代理批次' : undefined
  const emptyState =
    source === 'proxy'
      ? {
          mark: '⇄',
          title: '暂无代理批次',
          desc: '把浏览器代理指向本机 8888 端口后浏览目标站，捕获的流量会自动成批进入分析队列。',
        }
      : {
          mark: '⌖',
          title: '暂无自主下发的对话',
          desc: '点击上方「新对话」，描述目标与测试方向即可发起一次渗透测试任务。',
        }

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* 统一头部 */}
      <PageHeader
        title="对话"
        subtitle={source === 'self' ? '自主下发的扫描任务与对话记录' : '代理捕获流量的自动分析对话'}
      />

      {/* 主从双栏 */}
      <div className="grid min-h-0 flex-1 grid-cols-[304px_1fr]">
        <div className="flex h-full min-h-0 flex-col border-r border-border p-2.5">
          <ConversationList
          ref={convListRef}
          activeId={currentConv || undefined}
          source={source}
          allowNew={source === 'self'}
          heading={heading}
          emptyState={emptyState}
          autoSelect
          onSelect={setCurrentConv}
          onNew={newConversation}
          onDeleted={onConvDeleted}
        />
      </div>
      <ConversationDetail
        convId={currentConv || undefined}
        source={source}
        onStarted={onStarted}
        onRunningChanged={() => void convListRef.current?.refresh()}
      />
      </div>
    </div>
  )
}
