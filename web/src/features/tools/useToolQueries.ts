import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { assignTool, getTool, listTools } from '@/api/config'
import type { ToolDetail, ToolKind } from '@/api/types'

// 工具页数据层：统一走 React Query（与凭证库 / 流量等页同一模式）。详情按工具名分键：
// 迟到的详情只落进它自己的缓存，切走或列表变空都不会让旧工具重新出现在右栏；装配的
// 乐观更新与失败回滚也只作用于发起写入的那个工具。

export const toolKeys = {
  list: (kind: ToolKind | '', q: string) => ['tools', 'list', kind, q] as const,
  detail: (name: string) => ['tools', 'detail', name] as const,
}

/** 工具目录（有界，全量）；筛选变化时旧数据先留屏。 */
export function useToolList(kind: ToolKind | '', q: string) {
  return useQuery({
    queryKey: toolKeys.list(kind, q),
    queryFn: () => listTools({ kind: kind || undefined, q: q || undefined }),
    placeholderData: keepPreviousData,
  })
}

/** 单个工具详情；name 为空时不取数。 */
export function useToolDetail(name: string | null) {
  return useQuery({
    queryKey: toolKeys.detail(name ?? ''),
    queryFn: () => getTool(name as string),
    enabled: !!name,
  })
}

interface AssignVars {
  tool: string
  code: string
  involved: boolean
}

/** 从工具侧装 / 卸某智能体：乐观翻转该工具详情里的装配态，失败回滚该工具（不碰其它工具）。 */
export function useAssignTool() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ tool, code, involved }: AssignVars) => assignTool(tool, code, involved),
    onMutate: async ({ tool, code, involved }) => {
      const key = toolKeys.detail(tool)
      await qc.cancelQueries({ queryKey: key })
      const prev = qc.getQueryData<ToolDetail>(key)
      if (prev) {
        qc.setQueryData<ToolDetail>(key, {
          ...prev,
          agents: prev.agents.map((a) => (a.code === code ? { ...a, involved } : a)),
        })
      }
      return { prev }
    },
    onError: (_e, { tool }, ctx) => {
      if (ctx?.prev) qc.setQueryData(toolKeys.detail(tool), ctx.prev)
    },
    onSettled: (_d, _e, { tool }) => {
      void qc.invalidateQueries({ queryKey: toolKeys.detail(tool) })
    },
  })
}
