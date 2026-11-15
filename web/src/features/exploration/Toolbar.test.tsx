import { describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Toolbar } from './Toolbar'
import { listConversations } from '@/api/conversations'

vi.mock('@/api/conversations', () => ({
  listConversations: vi.fn(async ({ search }: { search?: string }) => ({
    conversations: search
      ? [{ id: 'c-9', title: '测试商城越权', task_id: 't', source: 'direct', created_at: '', updated_at: '2026-11-14T08:30:00' }]
      : [],
    hasMore: false,
    total: search ? 1 : 0,
  })),
}))

describe('探索图 Toolbar', () => {
  it('对话由可搜索的选择框选取（服务端按标题 / 任务目标 / ID 检索），不再手输 ID', async () => {
    const onChange = vi.fn()
    const user = userEvent.setup()
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <Toolbar
          conversationId=""
          onConversationChange={onChange}
          disabled={false}
          refreshing={false}
          onRefresh={vi.fn()}
          showFilters={false}
          filterKind="all"
          onFilterKind={vi.fn()}
          filterState="all"
          onFilterState={vi.fn()}
          stats={{ total: 0, edgeCount: 0, byKind: {} }}
        />
      </QueryClientProvider>,
    )
    expect(screen.queryByRole('textbox', { name: '对话 ID' })).toBeNull()
    await user.click(screen.getByRole('button', { name: '选择对话：选择对话' }))
    await user.type(screen.getByRole('combobox', { name: '搜索对话标题、任务目标、ID' }), '商城')
    await waitFor(() => expect(listConversations).toHaveBeenCalledWith({ search: '商城', limit: 20 }))
    await user.click(await screen.findByRole('option', { name: /测试商城越权/ }))
    expect(onChange).toHaveBeenCalledWith('c-9')
  })
})
