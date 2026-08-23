import { useMemo, useState } from 'react'
import { LlmAuditFilterBar } from '@/features/llm-audit/LlmAuditFilterBar'
import { LlmAuditTable } from '@/features/llm-audit/LlmAuditTable'
import { LlmAuditToolbar } from '@/features/llm-audit/LlmAuditToolbar'
import { LlmInvocationDrawer } from '@/features/llm-audit/LlmInvocationDrawer'
import { useLlmAuditFilters } from '@/features/llm-audit/useLlmAuditFilters'
import { useLlmAuditFacets, useLlmAuditList, useLlmAuditStat, useLlmInvocationDetail } from '@/features/llm-audit/useLlmAuditQueries'
import type { LLMInvocationSummary } from '@/api/types'
import { humanTokens, compactNumber } from '@/lib/format'
import { PageSizeSelect } from '@/components/ui/PageSizeSelect'

// 分页按钮：图标化前后翻页（‹ ›），对齐流量/漏洞模块的样式。
const PAGER_BTN =
  'inline-flex h-7 w-7 items-center justify-center rounded-md border border-border text-sm text-muted transition-colors hover:border-border-strong hover:text-text disabled:pointer-events-none disabled:opacity-35'

// LLM 审计页：选会话 → 服务端筛选 + offset 分页拉调用明细。
// 布局：工具栏统计 chip（非大卡片）+ 筛选栏 + 单张扁平密集表 + 总数/每页条数/翻页。点行右侧抽屉钻取完整原文。
//
// 数据层走 React Query（见 useLlmAuditQueries）：query key 含 task/filters/page/size，任一变化
// 即视为新查询，过期请求自动被丢弃（不再有「快速切筛选后旧请求覆盖新结果」的竞态）；
// placeholderData: keepPreviousData 让筛选/翻页时旧数据先留在屏幕上，不必每次整表闪成骨架。
// 筛选/分页状态持久化进 URL（见 useLlmAuditFilters）：刷新/前进后退/分享链接都能还原视图。
export function LlmAuditPage() {
  const { taskId, filters, page, size, setTaskId, setFilters, setPage, setSize, resetFilters } = useLlmAuditFilters()

  const facetsQuery = useLlmAuditFacets(taskId)
  const listQuery = useLlmAuditList(taskId, filters, page, size)
  const statQuery = useLlmAuditStat(taskId, filters)

  const rows = listQuery.data?.items ?? []
  const total = listQuery.data?.total ?? 0
  const totalPages = Math.max(1, Math.ceil(total / size))
  const hasFilter = !!(filters.role || filters.model || filters.onlyErr || filters.start || filters.end)

  // provider 只在该 task 下确实出现过多个时才逐行显示——单一 provider 时每行重复同一个值是纯噪声。
  const showProvider = useMemo(() => new Set(rows.map((r) => r.provider)).size > 1, [rows])

  // 详情钻取：选中行 id 驱动查询，抽屉开合与数据加载解耦。
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const detailQuery = useLlmInvocationDetail(taskId, selectedId)
  const openDetail = (row: LLMInvocationSummary) => {
    setSelectedId(row.id)
    setDrawerOpen(true)
  }

  const error = listQuery.isError ? (listQuery.error instanceof Error ? listQuery.error.message : '加载失败') : ''

  return (
    <div className="flex h-full min-h-0 flex-col">
      <LlmAuditToolbar taskId={taskId} onTaskIdChange={setTaskId} stat={statQuery.data} />

      <div className="min-h-0 flex-1 overflow-y-auto px-5.5 py-4.5">
        {!taskId ? (
          <div className="py-16 text-center text-[13.5px] text-muted">请选择一个对话查看 LLM 调用审计</div>
        ) : error ? (
          <div className="py-16 text-center text-[13.5px] text-sev-critical">⚠ {error}</div>
        ) : (
          <>
            <LlmAuditFilterBar
              filters={filters}
              roles={facetsQuery.data?.roles ?? []}
              models={facetsQuery.data?.models ?? []}
              onChange={setFilters}
              onReset={resetFilters}
              trailing={
                (rows.length > 0 || page > 1) ? (
                  <>
                    <span className="text-[12px] text-muted tabular-nums">
                      共 {compactNumber(total)} 条{statQuery.data && ` / ${humanTokens(statQuery.data.calls)}`}
                    </span>
                    <PageSizeSelect value={size} onChange={setSize} />
                    <div className="flex items-center gap-1.5">
                      <button
                        type="button"
                        disabled={page <= 1 || listQuery.isFetching}
                        onClick={() => setPage(page - 1)}
                        aria-label="上一页"
                        className={PAGER_BTN}
                      >
                        <span aria-hidden>‹</span>
                      </button>
                      <span className="min-w-[52px] text-center text-[12px] text-muted tabular-nums">
                        <span className="font-semibold text-text">{page}</span> / {totalPages}
                      </span>
                      <button
                        type="button"
                        disabled={page >= totalPages || listQuery.isFetching}
                        onClick={() => setPage(page + 1)}
                        aria-label="下一页"
                        className={PAGER_BTN}
                      >
                        <span aria-hidden>›</span>
                      </button>
                    </div>
                  </>
                ) : null
              }
            />

            <LlmAuditTable
              rows={rows}
              loading={listQuery.isPending}
              hasFilter={hasFilter}
              showProvider={showProvider}
              onRowClick={openDetail}
            />
          </>
        )}
      </div>

      <LlmInvocationDrawer
        open={drawerOpen}
        detail={detailQuery.data ?? null}
        loading={detailQuery.isPending && selectedId != null}
        error={detailQuery.isError ? (detailQuery.error instanceof Error ? detailQuery.error.message : '加载详情失败') : ''}
        onOpenChange={setDrawerOpen}
      />
    </div>
  )
}
