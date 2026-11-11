import { PageHeader } from '@/components/PageHeader'
import { useCallback, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { ChevronLeft } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { useMediaQuery } from '@/hooks/useMediaQuery'
import { ConversationDetail } from '@/features/conversation/ConversationDetail'
import { ConversationList, type ConversationListHandle } from '@/features/conversation/ConversationList'

interface ConversationsPageProps {
  source: 'direct' | 'proxy'
}

// 对话页：主从双栏——左 ConversationList + 右 ConversationDetail。source 由路由决定
// （/conversations/direct、/conversations/proxy 共用本组件），人工下发/代理捕获两个 tab
// 差异仅在列表文案/是否可发起新对话，详情区的 SSE/用量/状态/插话全在 ConversationDetail 内。
// 窄屏（< 768）主从单栏：同一时刻只显示列表或详情，详情顶部带返回；列表不自动选中，
// 否则一进页面就被推进详情、看不到列表。
export function ConversationsPage({ source }: ConversationsPageProps) {
  const [searchParams, setSearchParams] = useSearchParams()
  const convListRef = useRef<ConversationListHandle>(null)
  const isWide = useMediaQuery('(min-width: 768px)', true)
  // 窄屏当前是否停在详情：深链带 ?conv= 时直接进详情。
  const [narrowDetail, setNarrowDetail] = useState(() => Boolean(searchParams.get('conv')))
  const showList = isWide || !narrowDetail
  const showDetail = isWide || narrowDetail

  // 当前会话以 URL 的 ?conv= 为唯一状态：刷新、分享链接、浏览器前进后退都还原同一视图，
  // 跨页深链接（流量详情 / 任务详情带 conv 跳转）也走同一入口。选择用 replace 写入，
  // 列表的自动选中不会在历史栈里堆出一串条目。
  const currentConv = searchParams.get('conv') ?? ''
  const setCurrentConv = useCallback(
    (id: string) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev)
          if (id) next.set('conv', id)
          else next.delete('conv')
          return next
        },
        { replace: true },
      )
    },
    [setSearchParams],
  )

  const selectConversation = (id: string) => {
    setCurrentConv(id)
    setNarrowDetail(true)
  }
  const newConversation = () => {
    setCurrentConv('') // 空=新建态，ConversationDetail 露空状态 + 可发起 Composer（仅 direct）
    setNarrowDetail(true)
  }

  // 新会话建立：切到它 + 刷左列表（否则新会话不出现，要手动点 ↻）。
  const onStarted = (convID: string) => {
    setCurrentConv(convID)
    void convListRef.current?.refresh()
  }
  // 删除的若是当前打开会话→清空选中；删别的不影响当前视图。
  const onConvDeleted = (convID: string) => {
    if (convID === currentConv) {
      setCurrentConv('')
      setNarrowDetail(false)
    }
  }

  const heading = source === 'proxy' ? '代理批次' : undefined
  const emptyState =
    source === 'proxy'
      ? {
          mark: '⇄',
          title: '暂无代理批次',
          desc: '把浏览器代理指向 liusha 代理入口（本机直接运行为 8588 端口，容器部署默认宿主端口 58588）后浏览目标站，捕获的流量会自动成批进入分析队列。',
        }
      : {
          mark: '⌖',
          title: '暂无人工下发的对话',
          desc: '点击上方「新对话」，描述目标与测试方向即可发起一次渗透测试任务。',
        }

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* 统一头部 */}
      <PageHeader
        title="对话"
        subtitle={source === 'direct' ? '实时观看 agent 干活，可随时插话补充指令' : '选一批捕获的流量，看 AI 逐批分析，可插话追问'}
      />

      {/* 主从双栏（窄屏单栏） */}
      <div className="grid min-h-0 flex-1 grid-cols-1 md:grid-cols-[304px_1fr]">
        {showList && (
          <div className="flex h-full min-h-0 flex-col p-2.5 md:border-r md:border-border">
            <ConversationList
              ref={convListRef}
              activeId={currentConv || undefined}
              source={source}
              allowNew={source === 'direct'}
              heading={heading}
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
                <Button variant="ghost" size="sm" onClick={() => setNarrowDetail(false)}>
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
            />
          </div>
        )}
      </div>
    </div>
  )
}
