import { useCallback } from 'react'
import { useSearchParams } from 'react-router-dom'

/** 历史记录写法：缺省新增一条（后退可撤销）；replace 改写当前这条（不进历史）。 */
export interface HistoryOptions {
  replace?: boolean
}

/** 列表页打开的条目（详情面板 / 抽屉）在 URL 里的参数名，各列表页同名。 */
export const SELECTED_PARAM = 'selected'

/**
 * 列表页打开的条目写在 URL 的 ?selected=：刷新、分享链接、跳到别处再后退都能还原；窄屏
 * 「列表 / 详情」二选一也由它决定（有选中即详情）。
 *
 * 历史规则（全站一致）：用户主动打开、切换、关闭条目各新增一条历史——后退即撤销这一步
 * （关掉刚打开的详情、回到上一条）；键盘逐行移动、系统自动打开（如范围内只有一条）用
 * replace，不在历史里堆一串中间状态。其它参数（筛选、分页）原样保留。
 */
export function useSelectedParam(): [string, (id: string, opts?: HistoryOptions) => void] {
  const [searchParams, setSearchParams] = useSearchParams()
  const selected = searchParams.get(SELECTED_PARAM) ?? ''
  const select = useCallback(
    (id: string, opts?: HistoryOptions) =>
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev)
          if (id) next.set(SELECTED_PARAM, id)
          else next.delete(SELECTED_PARAM)
          return next
        },
        { replace: opts?.replace },
      ),
    [setSearchParams],
  )
  return [selected, select]
}
