import { PageHeader } from '@/components/PageHeader'
import { useCallback, useRef } from 'react'
import { useLocation, useSearchParams } from 'react-router-dom'
import type { HistoryOptions } from '@/hooks/useSelectedParam'
import { ChevronLeft } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { ConversationDetail } from '@/features/conversation/ConversationDetail'
import { ConversationList, type ConversationListHandle } from '@/features/conversation/ConversationList'
import { PROXY_SETUP_HINT } from '@/lib/source'
import { ProxyAccess } from '@/features/proxy/ProxyAccess'

interface ConversationsPageProps {
  source: 'direct' | 'proxy'
}

// 对话页：主从双栏——左 ConversationList + 右 ConversationDetail。source 由路由决定
// （/conversations/direct、/conversations/proxy 共用本组件），人工下发/代理捕获两个 tab
// 差异仅在列表文案/是否可发起新对话，详情区的 SSE/用量/状态/插话全在 ConversationDetail 内。
// 窄屏（< 768）主从单栏：同一时刻只显示列表或详情，详情顶部带返回；列表不自动选中，
// 否则一进页面就被推进详情、看不到列表。
//
// 打开的对话以 URL 的 ?conversation= 为唯一状态：刷新、分享链接、前进后退都还原同一视图，
// 跨页跳转也走这个入口；窄屏「列表 / 详情」由它决定。历史规则与列表页一致：用户点开、
// 切换、返回列表各新增一条历史（后退即撤销这一步）；进页自动打开最新对话、新对话建立后
// 落到它、删除当前对话用 replace。
//
// 执行轨迹按哪个智能体筛选（?agent=）同样在 URL 上：它是用户主动的取舍，换一个对话即清除——
// 筛选属于它所在的那个对话。
export function ConversationsPage({ source }: ConversationsPageProps) {
  const [searchParams, setSearchParams] = useSearchParams()
  const location = useLocation()
  const convListRef = useRef<ConversationListHandle>(null)
  const isWide = useMediaQuery('(min-width: 768px)', true)
  const currentConv = searchParams.get('conversation') ?? ''
  const agent = searchParams.get('agent')
  // 窄屏「新对话」还没有对话 ID：用这条历史记录的 state 标记停在详情（输入框）——后退回列表；
  // 刷新后回到列表（空输入框不值得还原）。
  const composing = Boolean((location.state as { composing?: boolean } | null)?.composing)
  const narrowDetail = currentConv !== '' || composing
  const showList = isWide || !narrowDetail
  const showDetail = isWide || narrowDetail

  const setCurrentConv = useCallback(
    (id: string, opts?: HistoryOptions & { composing?: boolean }) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev)
          if (id) next.set('conversation', id)
          else next.delete('conversation')
          next.delete('agent')
          return next
        },
        { replace: opts?.replace, state: opts?.composing ? { composing: true } : undefined },
      )
    },
    [setSearchParams],
  )

  const setAgent = useCallback(
    (name: string | null) =>
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev)
        if (name) next.set('agent', name)
        else next.delete('agent')
        return next
      }),
    [setSearchParams],
  )

  const selectConversation = (id: string, opts?: HistoryOptions) => setCurrentConv(id, opts)
  // 空 = 新建态，ConversationDetail 露空状态 + 可发起 Composer（仅 direct）
  const newConversation = () => setCurrentConv('', { composing: true })
  const backToList = () => setCurrentConv('')

  // 新会话建立：落到它（改写「新对话」这条历史）+ 刷左列表（否则新会话不出现，要手动点 ↻）。
  const onStarted = (convID: string) => {
    setCurrentConv(convID, { replace: true })
    void convListRef.current?.refresh()
  }
  // 删除的若是当前打开会话→清空选中（改写当前历史，后退不再回到已删除的对话）；删别的不影响当前视图。
  const onConvDeleted = (convID: string) => {
    if (convID === currentConv) setCurrentConv('', { replace: true })
  }

  const emptyState =
    source === 'proxy'
      ? { noun: '代理批次', hint: PROXY_SETUP_HINT }
      : { noun: '对话', hint: '点击「新对话」，描述目标与测试方向即可发起任务。' }

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* 统一头部 */}
      <PageHeader
        title="对话"
      >
        {source === 'proxy' && <ProxyAccess />}
      </PageHeader>

      {/* 主从双栏（窄屏单栏） */}
      <div className="grid min-h-0 flex-1 grid-cols-1 md:grid-cols-[304px_1fr]">
        {showList && (
          <div className="flex h-full min-h-0 flex-col p-2.5 md:border-r md:border-border">
            <ConversationList
              ref={convListRef}
              activeId={currentConv || undefined}
              source={source}
              allowNew={source === 'direct'}
              emptyState={emptyState}
              autoSelect={isWide}
              onSelect={selectConversation}
              onNew={newConversation}
              onDeleted={onConvDeleted}
            />
          </div>
        )}
        {showDetail && (
          <div className="flex h-full min-h-0 min-w-0 flex-col">
            {!isWide && (
              <div className="flex-shrink-0 border-b border-border px-2 py-1.5">
                <Button variant="ghost" size="sm" onClick={backToList}>
                  <ChevronLeft />
                  返回对话列表
                </Button>
              </div>
            )}
            <ConversationDetail
              convId={currentConv || undefined}
              source={source}
              onStarted={onStarted}
              onRunningChanged={() => void convListRef.current?.refresh()}
              agent={agent}
              onAgentChange={setAgent}
            />
          </div>
        )}
      </div>
    </div>
  )
}
