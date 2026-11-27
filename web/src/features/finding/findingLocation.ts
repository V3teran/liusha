import type { FindingRow } from '@/api/types'

/**
 * 发现的位置：「方法 主机路径」。定位不到任何一项时为空串——坐实材料与任务目标里都没有
 * 地址的发现以空主机入账，调用处显示占位，不伪造位置。
 */
export function findingLocation(f: Pick<FindingRow, 'host' | 'target'>): string {
  return [f.target?.method, `${f.host}${f.target?.path ?? ''}`].filter(Boolean).join(' ')
}
