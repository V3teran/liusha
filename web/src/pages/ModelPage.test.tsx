import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import { ModelPage } from './ModelPage'
import * as queries from '@/features/model/useModelQueries'

vi.mock('@/features/model/useModelQueries')
vi.mock('@/components/OwnerPicker', () => ({
  OwnerPicker: () => <div>OwnerPicker</div>,
}))

const mockedUseProviders = queries.useProviders as unknown as ReturnType<typeof vi.fn>
const mockedUseAssignments = queries.useAssignments as unknown as ReturnType<typeof vi.fn>
const mockedUseLLMInvocationStat = queries.useLLMInvocationStat as unknown as ReturnType<typeof vi.fn>
const mockedUseSaveProvider = queries.useSaveProvider as unknown as ReturnType<typeof vi.fn>
const mockedUseDeleteProvider = queries.useDeleteProvider as unknown as ReturnType<typeof vi.fn>
const mockedUseRestoreProviderEnvKey = queries.useRestoreProviderEnvKey as unknown as ReturnType<typeof vi.fn>
const mockedUseRouting = queries.useRouting as unknown as ReturnType<typeof vi.fn>
const mockedUseAgentConfigs = queries.useAgentConfigs as unknown as ReturnType<typeof vi.fn>
const mockedUseSaveRoleRoute = queries.useSaveRoleRoute as unknown as ReturnType<typeof vi.fn>
const mockedUseSaveAgentComplexity = queries.useSaveAgentComplexity as unknown as ReturnType<typeof vi.fn>

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
    mockedUseRestoreProviderEnvKey.mockReturnValue({ mutate: vi.fn() })

    // Mock AssignmentView 需要的 hooks
    mockedUseRouting.mockReturnValue({ data: { routes: [] }, isLoading: false })
    mockedUseAgentConfigs.mockReturnValue({ data: [], isLoading: false })
    mockedUseSaveRoleRoute.mockReturnValue({ mutate: vi.fn() })
    mockedUseSaveAgentComplexity.mockReturnValue({ mutate: vi.fn(), isPending: false })
  })

  it('渲染页面标题与副标题（单页设置流，无独立导出入口）', () => {
    mockedUseProviders.mockReturnValue({ data: [], isLoading: false })
    mockedUseAssignments.mockReturnValue({ data: [], isLoading: false })
    mockedUseLLMInvocationStat.mockReturnValue({ data: null, isLoading: false })

    renderPage()

    expect(screen.getByText('LLM 配置')).toBeTruthy()
    expect(screen.getByText('配置模型提供商和能力路由')).toBeTruthy()
  })

  it('渲染两个分区：模型提供商、能力路由', () => {
    mockedUseProviders.mockReturnValue({ data: [], isLoading: false })
    mockedUseAssignments.mockReturnValue({ data: [], isLoading: false })
    mockedUseLLMInvocationStat.mockReturnValue({ data: null, isLoading: false })

    renderPage()

    expect(screen.getByText('模型提供商')).toBeTruthy()
    expect(screen.getByText('能力路由')).toBeTruthy()
  })
})
