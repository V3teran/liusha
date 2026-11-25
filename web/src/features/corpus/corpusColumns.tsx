import { createColumnHelper } from '@tanstack/react-table'
import { ChevronRight } from 'lucide-react'
import type { CorpusEntry, CorpusSearchResult } from '@/api/corpus'
import { Badge } from '@/components/ui/Badge'
import { corpusScopeLabel, corpusSourceColor, corpusSourceLabel } from '@/lib/corpus'
import { shortDateTime } from '@/lib/format'

// 知识列表与检索结果共用的单元格：同一条目，同一种呈现。

/** 标题与一行正文摘要：不用逐条打开也能浏览。 */
function KnowledgeCell({ entry }: { entry: CorpusEntry }) {
  return (
    <span className="flex min-w-0 flex-col gap-0.5 py-0.5">
      <span className="truncate text-text" title={entry.title}>
        {entry.title}
      </span>
      <span className="truncate text-xs text-text-tertiary">{entry.content.replace(/\s+/g, ' ')}</span>
    </span>
  )
}

function ScopeCell({ entry }: { entry: CorpusEntry }) {
  const sc = entry.scenario
  return (
    <span className={sc?.deleted ? 'text-text-tertiary' : sc ? 'text-text' : 'text-text-secondary'}>
      {corpusScopeLabel(sc)}
    </span>
  )
}

function SourceCell({ entry }: { entry: CorpusEntry }) {
  return (
    <Badge dot={false} size="sm" color={corpusSourceColor(entry.source)}>
      {corpusSourceLabel(entry.source)}
    </Badge>
  )
}

function ConfirmationsCell({ entry }: { entry: CorpusEntry }) {
  return (
    <span className="block text-xs tabular-nums text-text-secondary">
      {entry.confirmations || '—'}
    </span>
  )
}

const expandColumn = {
  id: 'expand',
  // 箭头只示意「整行可点开」：表头给读屏一个列名，图标本身不播报
  header: () => <span className="sr-only">详情</span>,
  size: 40,
  cell: () => <ChevronRight aria-hidden className="h-4 w-4 text-text-tertiary opacity-50" />,
}

const entryColumn = createColumnHelper<CorpusEntry>()

export const corpusColumns = [
  entryColumn.display({
    id: 'knowledge',
    header: '知识',
    size: 460,
    cell: (c) => <KnowledgeCell entry={c.row.original} />,
  }),
  entryColumn.display({
    id: 'scope',
    header: '适用范围',
    size: 140,
    cell: (c) => <ScopeCell entry={c.row.original} />,
  }),
  entryColumn.display({
    id: 'source',
    header: '来源',
    size: 96,
    cell: (c) => <SourceCell entry={c.row.original} />,
  }),
  entryColumn.display({
    id: 'confirmations',
    header: '印证',
    size: 64,
    meta: { align: 'right' },
    cell: (c) => <ConfirmationsCell entry={c.row.original} />,
  }),
  entryColumn.accessor('updated_at', {
    header: '更新',
    size: 120,
    cell: (c) => <span className="font-mono text-xs text-text-tertiary">{shortDateTime(c.getValue())}</span>,
  }),
  entryColumn.display(expandColumn),
]

type Hit = CorpusSearchResult['hits'][number]
const hitColumn = createColumnHelper<Hit>()

/** 检索结果：得分在前，其余与知识列表一致（不列更新时间：排序依据是得分）。 */
export const corpusHitColumns = [
  hitColumn.accessor('score', {
    header: '得分',
    size: 64,
    meta: { align: 'right' },
    cell: (c) => (
      <span className="block text-xs tabular-nums text-text">{c.getValue().toFixed(2)}</span>
    ),
  }),
  hitColumn.display({
    id: 'knowledge',
    header: '知识',
    size: 460,
    cell: (c) => <KnowledgeCell entry={c.row.original.entry} />,
  }),
  hitColumn.display({
    id: 'scope',
    header: '适用范围',
    size: 140,
    cell: (c) => <ScopeCell entry={c.row.original.entry} />,
  }),
  hitColumn.display({
    id: 'source',
    header: '来源',
    size: 96,
    cell: (c) => <SourceCell entry={c.row.original.entry} />,
  }),
  hitColumn.display({
    id: 'confirmations',
    header: '印证',
    size: 64,
    meta: { align: 'right' },
    cell: (c) => <ConfirmationsCell entry={c.row.original.entry} />,
  }),
  hitColumn.display(expandColumn),
]
