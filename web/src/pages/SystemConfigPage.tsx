import { SearchInput } from '@/components/SearchInput'
import { PageHeader } from '@/components/PageHeader'
import { FilterBar } from '@/components/FilterBar'
import { CompactionSection } from '@/features/settings/CompactionSection'
import { RuntimeSection } from '@/features/settings/RuntimeSection'
import { ProxyFilterSection } from '@/features/settings/ProxyFilterSection'
import { useState, useMemo } from 'react'

// 系统配置页：三组设置（会话压缩 / 工具运行时 / 代理流量过滤）分区独立保存。
// 出厂默认来自程序与 config.yaml，这里编辑的是用户自定义：只提交改动的字段，与出厂
// 相同的值不算自定义，每个已自定义字段可对比出厂、单独恢复默认。保存后经后端失效广播
// 生效——runner 现读即新值，proxy 热换过滤链（无需重启）。
export function SystemConfigPage() {
  const [query, setQuery] = useState('')

  // 定义各分区的元数据，用于搜索匹配
  const sections = useMemo(() => [
    {
      id: 'compaction',
      title: '上下文压缩',
      keywords: '上下文 压缩 历史 token 预算 占比 规划 执行 裁决 监察 纪要',
      component: <CompactionSection />,
    },
    {
      id: 'runtime',
      title: '工具运行时',
      keywords: '工具 运行时 超时 看门狗 llm 输出 尾部 截尾 字节 钳制',
      component: <RuntimeSection />,
    },
    {
      id: 'proxy_filter',
      title: '代理流量过滤规则',
      keywords: '代理 流量 过滤 黑名单 白名单 host 方法 协议 后缀 状态码',
      component: <ProxyFilterSection />,
    },
  ], [])

  const filteredSections = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return sections
    return sections.filter(s =>
      s.title.toLowerCase().includes(q) ||
      s.keywords.toLowerCase().includes(q)
    )
  }, [sections, query])

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title="系统配置" subtitle="上下文压缩、代理过滤与工具运行时参数，保存即生效" />

      {/* 搜索归筛选栏（全站筛选控件唯一位置）：放进页头右槽会在窄屏把标题挤到零宽。 */}
      <FilterBar label="系统配置筛选">
        <SearchInput
          className="w-full sm:w-72"
          value={query}
          onChange={setQuery}
          placeholder="搜索配置项"
          ariaLabel="搜索配置项"
        />
      </FilterBar>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-4xl flex-col gap-6 px-6 py-6">
          {filteredSections.length === 0 ? (
            <div className="py-12 text-center text-sm text-text-secondary">
              未找到匹配的配置项
            </div>
          ) : (
            filteredSections.map(section => (
              <div key={section.id}>{section.component}</div>
            ))
          )}
        </div>
      </div>
    </div>
  )
}
