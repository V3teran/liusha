import { PageHeader } from '@/components/PageHeader'
import { useEffect, useMemo, useState } from 'react'
import { errorText } from '@/api/client'
import { ToolDetailPanel } from '@/features/tools/ToolDetailPanel'
import { KIND_SEGMENTS, ToolMasterList, groupTools, type KindFilter } from '@/features/tools/ToolMasterList'
import { useAssignTool, useToolDetail, useToolList } from '@/features/tools/useToolQueries'
import { FilterBar } from '@/components/FilterBar'
import { SearchInput } from '@/components/SearchInput'
import { ChevronLeft } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { useMediaQuery } from '@/hooks/useMediaQuery'

// 搜索防抖：输入停顿后才发起检索。
const SEARCH_DEBOUNCE_MS = 250

// 工具目录（主从双栏）：左栏按种类/分类分组、可搜索、点选；右栏常驻详情面板。
// 有界目录（代码来源），左栏加载全量并分组滚动，故无翻页（分组与分页天然冲突）。
// 数据经 useToolQueries：详情按工具名分键，装配写入只更新 / 回滚对应工具。
export function ToolsPage() {
  const [kind, setKind] = useState<KindFilter>('')
  const [query, setQuery] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedQuery(query), query ? SEARCH_DEBOUNCE_MS : 0)
    return () => clearTimeout(timer)
  }, [query])

  const list = useToolList(kind, debouncedQuery)
  const tools = useMemo(() => list.data?.tools ?? [], [list.data])

  const [selected, setSelected] = useState<string | null>(null)
  const detailQuery = useToolDetail(selected)
  const assign = useAssignTool()

  // 窄屏（< 768）主从单栏：列表与详情二选一，点选进入详情、返回回到列表。
  const isWide = useMediaQuery('(min-width: 768px)', true)
  const [narrowDetail, setNarrowDetail] = useState(false)
  const showList = isWide || !narrowDetail
  const showDetail = isWide || narrowDetail

  const groups = useMemo(() => groupTools(tools), [tools])

  // 渲染序扁平列表（function 组在前的可视顺序）——默认选中取它的首项，
  // 保证左栏可视第一行就是高亮行（数据序 tools[0] 可能在滚动区深处的 cli 组）。
  const renderOrder = useMemo(
    () => groups.flatMap((g) => g.categories.flatMap((c) => c.tools)),
    [groups],
  )

  // 选中项跟随列表：当前选中仍在列表则保留，否则选中可视首项（主从视图恒有选中）。
  useEffect(() => {
    setSelected((prev) => {
      if (renderOrder.length === 0) return null
      return prev && renderOrder.some((t) => t.name === prev) ? prev : renderOrder[0].name
    })
  }, [renderOrder])

  // 从工具侧装/卸某智能体（后端权威：改该智能体 function_tools/cli_tools 回存）。
  const onToggle = (code: string, involved: boolean) => {
    if (!selected) return
    assign.mutate(
      { tool: selected, code, involved },
      { onError: (e) => window.alert(errorText(e, '保存失败')) },
    )
  }
  const savingCode = assign.isPending && assign.variables?.tool === selected ? assign.variables.code : null

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title="工具" subtitle="智能体可调用的工具清单，按智能体启停装配" />

      {/* 搜索移出标题行，与左栏种类 tab 同一视觉层级——筛选控件挨着数据，不占标题行。 */}
      <FilterBar label="工具筛选">
        <SearchInput className="w-64" value={query} onChange={setQuery} placeholder="搜索工具名 / 描述" ariaLabel="搜索工具" />
      </FilterBar>

      <div className="grid min-h-0 flex-1 grid-cols-1 gap-4 overflow-hidden px-6 py-4 md:grid-cols-[minmax(240px,320px)_1fr]">
        {showList && (
          <ToolMasterList
            groups={groups}
            segments={KIND_SEGMENTS}
            kind={kind}
            onKind={setKind}
            loading={list.isLoading}
            error={list.isError ? errorText(list.error, '加载工具目录失败') : ''}
            empty={tools.length === 0}
            emptyHint={query ? '无匹配工具' : '工具目录为空——检查后端同步是否成功'}
            selected={selected}
            onSelect={(name) => {
              setSelected(name)
              setNarrowDetail(true)
            }}
          />
        )}
        {showDetail && (
          <div className="flex min-h-0 flex-col gap-2">
            {!isWide && (
              <Button variant="ghost" size="sm" className="self-start" onClick={() => setNarrowDetail(false)}>
                <ChevronLeft />
                返回工具列表
              </Button>
            )}
            <ToolDetailPanel
              detail={selected ? (detailQuery.data ?? null) : null}
              loading={detailQuery.isLoading}
              error={detailQuery.isError ? errorText(detailQuery.error, '加载工具详情失败') : ''}
              onRetry={() => void detailQuery.refetch()}
              savingCode={savingCode}
              onToggle={onToggle}
            />
          </div>
        )}
      </div>
    </div>
  )
}
