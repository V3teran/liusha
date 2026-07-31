import { useMemo, useState } from 'react'
import { LlmAuditFilterBar } from '@/features/llm-audit/LlmAuditFilterBar'
import { LlmAuditTable } from '@/features/llm-audit/LlmAuditTable'
import { LlmAuditToolbar } from '@/features/llm-audit/LlmAuditToolbar'
import { LlmInvocationDrawer } from '@/features/llm-audit/LlmInvocationDrawer'
import { useLlmAuditFilters } from '@/features/llm-audit/useLlmAuditFilters'
import { useLlmAuditFacets, useLlmAuditList, useLlmAuditStat, useLlmInvocationDetail } from '@/features/llm-audit/useLlmAuditQueries'
import type { LLMInvocationSummary } from '@/api/types'
import { humanTokens } from '@/lib/format'

// LLM 审计页：选会话 → 服务端筛选 + id 游标分页拉调用明细。
// 布局：工具栏统计 chip（非大卡片）+ 筛选栏 + 单张扁平密集表 + 上下页。点行右侧抽屉钻取完整原文。
//
// 数据层走 React Query（见 useLlmAuditQueries）：query key 含 task/filters/游标，任一变化
// 即视为新查询，过期请求自动被丢弃（不再有「快速切筛选后旧请求覆盖新结果」的竞态）；
// placeholderData: keepPreviousData 让筛选/翻页时旧数据先留在屏幕上，不必每次整表闪成骨架。
// 筛选/分页状态持久化进 URL（见 useLlmAuditFilters）：刷新/前进后退/分享链接都能还原视图。
export function LlmAuditPage() {
  const { taskId, filters, cursors, pageNo, setTaskId, setFilters, goNextPage, goPrevPage, resetFilters } = useLlmAuditFilters()
  const after = cursors[cursors.length - 1] ?? 0

  const facetsQuery = useLlmAuditFacets(taskId)
  const listQuery = useLlmAuditList(taskId, filters, after)
  const statQuery = useLlmAuditStat(taskId, filters)

  const rows = listQuery.data?.items ?? []
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
                <>
                  第 {pageNo} 页 · 本页 {rows.length} 条{statQuery.data && ` / 共 ${humanTokens(statQuery.data.calls)}`}
                </>
              }
            />

            <LlmAuditTable
              rows={rows}
              loading={listQuery.isPending}
              hasFilter={hasFilter}
              showProvider={showProvider}
              onRowClick={openDetail}
            />

            {(rows.length > 0 || pageNo > 1) && (
              <div className="flex items-center justify-center gap-3.5 pb-1 pt-3.5">
                <button
                  type="button"
                  disabled={pageNo <= 1 || listQuery.isFetching}
                  onClick={goPrevPage}
                  className="rounded-md border border-border px-3 py-1 text-xs text-text disabled:opacity-40"
                >
                  上一页
                </button>
                <span className="text-[12.5px] text-muted tabular-nums">第 {pageNo} 页</span>
                <button
                  type="button"
                  disabled={!listQuery.data?.has_more || listQuery.isFetching}
                  onClick={() => goNextPage(listQuery.data?.next_after ?? 0)}
                  className="rounded-md border border-border px-3 py-1 text-xs text-text disabled:opacity-40"
                >
                  下一页
                </button>
              </div>
            )}
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
