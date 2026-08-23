// 分页大小候选：流量表与漏洞台账共用同一套选项，与后端 defaultTrafficPageSize/
// maxFindingPageSize 等口径对齐（10/50/100 覆盖「快速看几条」到「批量核对」的常见诉求）。
export const PAGE_SIZE_OPTIONS = [10, 50, 100] as const
export const DEFAULT_PAGE_SIZE = 50
