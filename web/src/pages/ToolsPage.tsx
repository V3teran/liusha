import { PageHeader } from '@/components/PageHeader'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { listTools, getTool, assignTool } from '@/api/config'
import type { Tool, ToolDetail } from '@/api/types'
import { ToolDetailPanel } from '@/features/tools/ToolDetailPanel'
import { KIND_SEGMENTS, ToolMasterList, groupTools, type KindFilter } from '@/features/tools/ToolMasterList'
import { FilterBar } from '@/components/FilterBar'
import { SearchInput } from '@/components/SearchInput'
import { ChevronLeft } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { useMediaQuery } from '@/hooks/useMediaQuery'

// 工具目录（主从双栏）：左栏按种类/分类分组、可搜索、点选；右栏常驻详情面板。
// 有界目录（代码来源），左栏加载全量并分组滚动，故无翻页（分组与分页天然冲突）。
export function ToolsPage() {
  const [kind, setKind] = useState<KindFilter>('')
  const [query, setQuery] = useState('')
  const [tools, setTools] = useState<Tool[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const [selected, setSelected] = useState<string | null>(null)
  const [detail, setDetail] = useState<ToolDetail | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)

  // savingCode 标记装配回存中的智能体，禁用其胶囊防重复点。装配态源出 detail.agents（后端权威）。
  const [savingCode, setSavingCode] = useState<string | null>(null)

  // 窄屏（< 768）主从单栏：列表与详情二选一，点选进入详情、返回回到列表。
  const isWide = useMediaQuery('(min-width: 768px)', true)
  const [narrowDetail, setNarrowDetail] = useState(false)
  const showList = isWide || !narrowDetail
  const showDetail = isWide || narrowDetail

  const listSeq = useRef(0)
  const detailSeq = useRef(0)

  // 列表取数：kind 即时、query 防抖 250ms；seq 丢弃过期响应。走无 page 全量接口。
  useEffect(() => {
    const seq = ++listSeq.current
    const run = async () => {
      setLoading(true)
      setError('')
      try {
        const res = await listTools({ kind: kind || undefined, q: query || undefined })
        if (seq !== listSeq.current) return
        setTools(res.tools)
      } catch (e) {
        if (seq !== listSeq.current) return
        setError(e instanceof Error ? e.message : '加载工具目录失败')
        setTools([])
      } finally {
        if (seq === listSeq.current) setLoading(false)
      }
    }
    const timer = setTimeout(run, query ? 250 : 0)
    return () => clearTimeout(timer)
  }, [kind, query])

  const groups = useMemo(() => groupTools(tools), [tools])

  // 渲染序扁平列表（function 组在前的可视顺序）——默认选中取它的首项，
  // 保证左栏可视第一行就是高亮行（数据序 tools[0] 可能在滚动区深处的 cli 组）。
  const renderOrder = useMemo(
    () => groups.flatMap((g) => g.categories.flatMap((c) => c.tools)),
    [groups],
  )

  // 选中项跟随列表：当前选中仍在列表则保留，否则选中可视首项（主从视图恒有选中）。
  useEffect(() => {
    if (renderOrder.length === 0) {
      setSelected(null)
      return
    }
    setSelected((prev) =>
      prev && renderOrder.some((t) => t.name === prev) ? prev : renderOrder[0].name,
    )
  }, [renderOrder])

  // 详情取数：选中变化即拉，seq 丢弃过期。
  useEffect(() => {
    if (!selected) {
      setDetail(null)
      return
    }
    const seq = ++detailSeq.current
    setDetailLoading(true)
    getTool(selected)
      .then((d) => {
        if (seq === detailSeq.current) setDetail(d)
      })
      .catch(() => {
        if (seq === detailSeq.current) setDetail(null)
      })
      .finally(() => {
        if (seq === detailSeq.current) setDetailLoading(false)
      })
  }, [selected])

  // 从工具侧装/卸某智能体的该工具：乐观翻转 detail.agents，调后端 assignTool；
  // 失败回滚并提示。写入是后端权威（改该智能体 function_tools/cli_tools 回存）。
  const onToggle = useCallback(
    async (code: string, next: boolean) => {
      const tool = detail?.tool
      if (!tool) return
      const flip = (involved: boolean): ToolDetail | null => {
        setDetail((prev) =>
          prev
            ? {
                ...prev,
                agents: prev.agents.map((a) => (a.code === code ? { ...a, involved } : a)),
              }
            : prev,
        )
        return null
      }
      setSavingCode(code)
      flip(next)
      try {
        await assignTool(tool.name, code, next)
      } catch (e) {
        flip(!next) // 回滚
        alert(e instanceof Error ? e.message : '保存失败')
      } finally {
        setSavingCode(null)
      }
    },
    [detail],
  )

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
            loading={loading}
            error={error}
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
              detail={detail}
              loading={detailLoading}
              savingCode={savingCode}
              onToggle={onToggle}
            />
          </div>
        )}
      </div>
    </div>
  )
}
