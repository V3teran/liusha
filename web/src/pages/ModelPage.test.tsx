import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { ModelPage } from './ModelPage'
import * as queries from '@/features/model/useModelQueries'

vi.mock('@/features/model/useModelQueries')

const mockedUseProviders = queries.useProviders as unknown as ReturnType<typeof vi.fn>
const mockedUseSaveProvider = queries.useSaveProvider as unknown as ReturnType<typeof vi.fn>
const mockedUseDeleteProvider = queries.useDeleteProvider as unknown as ReturnType<typeof vi.fn>
const mockedUseRouting = queries.useRouting as unknown as ReturnType<typeof vi.fn>
const mockedUseAgents = queries.useAgents as unknown as ReturnType<typeof vi.fn>
const mockedUseSaveRoleRoute = queries.useSaveRoleRoute as unknown as ReturnType<typeof vi.fn>
const mockedUseSaveAgentComplexity = queries.useSaveAgentComplexity as unknown as ReturnType<typeof vi.fn>
const mockedUseRetrievalModel = queries.useRetrievalModel as unknown as ReturnType<typeof vi.fn>
const mockedUseSaveRetrievalModel = queries.useSaveRetrievalModel as unknown as ReturnType<typeof vi.fn>
const mockedUseClearRetrievalModel = queries.useClearRetrievalModel as unknown as ReturnType<typeof vi.fn>
const mockedUseDiagnoseRetrievalModel = queries.useDiagnoseRetrievalModel as unknown as ReturnType<typeof vi.fn>

function renderPage() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <ModelPage />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('ModelPage', () => {
  beforeEach(() => {
    // Mock 所有 ProvidersView 需要的 hooks
    mockedUseSaveProvider.mockReturnValue({ isPending: false, mutate: vi.fn() })
    mockedUseDeleteProvider.mockReturnValue({ isPending: false, mutate: vi.fn() })

    // Mock AssignmentView 需要的 hooks
    mockedUseRouting.mockReturnValue({ data: { routes: [] }, isLoading: false })
    mockedUseAgents.mockReturnValue({ data: [], isLoading: false })
    mockedUseSaveRoleRoute.mockReturnValue({ mutate: vi.fn() })
    mockedUseSaveAgentComplexity.mockReturnValue({ mutate: vi.fn(), isPending: false })

    // Mock RetrievalView 需要的 hooks
    mockedUseRetrievalModel.mockReturnValue({
      data: { key_present: false, key_last4: '', embedding_model: 'e', rerank_model: 'r', updated_at: null },
      isLoading: false,
    })
    mockedUseSaveRetrievalModel.mockReturnValue({ isPending: false, mutate: vi.fn() })
    mockedUseClearRetrievalModel.mockReturnValue({ isPending: false, mutate: vi.fn() })
    mockedUseDiagnoseRetrievalModel.mockReturnValue({ isPending: false, mutate: vi.fn() })
  })

  it('渲染页面标题（单页设置流，无独立导出入口）', () => {
    mockedUseProviders.mockReturnValue({ data: [], isLoading: false })

    renderPage()

    expect(screen.getByRole('heading', { name: '模型配置' })).toBeTruthy()
  })

  it('渲染三个分区：模型提供商、能力路由、检索模型', () => {
    mockedUseProviders.mockReturnValue({ data: [], isLoading: false })

    renderPage()

    expect(screen.getByText('模型提供商')).toBeTruthy()
    expect(screen.getByText('能力路由')).toBeTruthy()
    expect(screen.getByText('检索模型')).toBeTruthy()
  })
})
