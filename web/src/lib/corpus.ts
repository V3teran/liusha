// 知识条目的来源（corpus.source）→ 展示文案 / 色。单一真相源：列表、详情、检索预览、筛选下拉
// 全部从此取值。取值与 DB corpus_source_check 一致：agent 任务沉淀 / expert 人工录入。

import type { CorpusScenarioRef, CorpusSource } from '@/api/corpus'

const CORPUS_SOURCE_META: Record<CorpusSource, { label: string; color: string }> = {
  agent: { label: '任务沉淀', color: 'var(--corpus-agent)' },
  expert: { label: '人工录入', color: 'var(--corpus-expert)' },
}

export function corpusSourceLabel(source: string): string {
  return CORPUS_SOURCE_META[source as CorpusSource]?.label ?? source
}

export function corpusSourceColor(source: string): string {
  return CORPUS_SOURCE_META[source as CorpusSource]?.color ?? 'var(--status-neutral)'
}

export const CORPUS_SOURCE_OPTIONS: ReadonlyArray<{ value: CorpusSource; label: string }> = [
  { value: 'agent', label: CORPUS_SOURCE_META.agent.label },
  { value: 'expert', label: CORPUS_SOURCE_META.expert.label },
]

/** 适用范围的文案：null = 通用；场景已删除时标注。 */
export function corpusScopeLabel(scenario: CorpusScenarioRef | null): string {
  if (!scenario) return '通用'
  if (scenario.deleted) return `${scenario.code}（场景已删除）`
  return scenario.name || scenario.code
}
