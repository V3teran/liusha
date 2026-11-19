import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ScenariosPage } from './ScenariosPage'
import {
  createScenario,
  deleteScenario,
  getScenario,
  listDeletedScenarios,
  listScenariosPaged,
  patchScenario,
  resetScenario,
  restoreScenario,
} from '@/api/config'
import type { Scenario, ScenarioDetail, ScenarioListItem } from '@/api/types'

vi.mock('@/api/config', () => ({
  createScenario: vi.fn(),
  patchScenario: vi.fn(),
  resetScenario: vi.fn(),
  deleteScenario: vi.fn(),
  getScenario: vi.fn(),
  listScenariosPaged: vi.fn(),
  listDeletedScenarios: vi.fn(),
  restoreScenario: vi.fn(),
}))

const mList = vi.mocked(listScenariosPaged)
const mGet = vi.mocked(getScenario)
const mPatch = vi.mocked(patchScenario)
const mCreate = vi.mocked(createScenario)
const mDelete = vi.mocked(deleteScenario)
const mReset = vi.mocked(resetScenario)
const mDeleted = vi.mocked(listDeletedScenarios)
const mRestore = vi.mocked(restoreScenario)

function sc(o: Partial<Scenario> = {}): Scenario {
  return {
    code: 'pentest',
    name: '渗透测试',
    description: '对授权目标做 Web 渗透测试',
    body: '情境正文',
    enabled: true,
    is_builtin: true,
    is_default: true,
    ...o,
  }
}

const item = (s: Scenario, o: Partial<ScenarioListItem> = {}): ScenarioListItem => ({
  ...s,
  customized: [],
  shadows_builtin: false,
  ...o,
})

const detail = (s: Scenario, o: Partial<ScenarioDetail> = {}): ScenarioDetail => ({
  scenario: s,
  defaults: s.is_builtin ? { name: s.name, description: s.description, body: s.body, enabled: true } : null,
  overrides: {},
  shadows_builtin: false,
  ...o,
})

function LocationProbe() {
  const loc = useLocation()
  const navigate = useNavigate()
  return (
    <>
      <output data-testid="loc">{loc.search}</output>
      {/* 模拟浏览器前进 / 后退把 ?selected= 换成别的场景 */}
      <button type="button" data-testid="go-ctf" onClick={() => navigate('/config/scenarios?selected=ctf')} />
    </>
  )
}

let queryClient: QueryClient

function renderPage(entry = '/config/scenarios') {
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[entry]}>
        <ScenariosPage />
        <LocationProbe />
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('ScenariosPage', () => {
  beforeEach(() => {
    for (const m of [mList, mGet, mPatch, mCreate, mDelete, mReset, mDeleted, mRestore]) m.mockReset()
    mDeleted.mockResolvedValue([])
    // spyOn 对已被监视的函数返回同一个 spy：清零调用记录，测试之间互不串扰。
    vi.spyOn(window, 'alert').mockImplementation(() => {}).mockClear()
    vi.spyOn(window, 'confirm').mockReturnValue(true).mockClear()
  })

  it('列表标出缺省、来源、自定义、遮蔽与停用', async () => {
    mList.mockResolvedValue({
      scenarios: [
        item(sc(), { customized: ['body'] }),
        item(sc({ code: 'ctf', name: '我的 CTF', is_builtin: false, is_default: false, enabled: false }), { shadows_builtin: true }),
      ],
      total: 2,
    })
    renderPage()
    expect(await screen.findByText('渗透测试')).toBeTruthy()
    for (const badge of ['缺省', '已自定义', '自建', '遮蔽同名内置', '已停用']) expect(screen.getByText(badge)).toBeTruthy()
  })

  it('打开的场景写在 ?selected=；内置场景也可停用、删除；修改只提交改动字段', async () => {
    mList.mockResolvedValue({ scenarios: [item(sc())], total: 1 })
    mGet.mockResolvedValue(detail(sc()))
    mPatch.mockResolvedValue(detail(sc({ name: '渗透' })))
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: /渗透测试/ }))
    expect(screen.getByTestId('loc').textContent).toBe('?selected=pentest')
    expect(((await screen.findByRole('checkbox')) as HTMLInputElement).disabled).toBe(false)
    expect(screen.getByText('删除')).toBeTruthy()
    const name = screen.getByDisplayValue('渗透测试')
    await user.clear(name)
    await user.type(name, '渗透')
    await user.click(screen.getByText('保存'))
    await waitFor(() => expect(mPatch).toHaveBeenCalledWith('pentest', { name: '渗透' }))
    await waitFor(() => expect(screen.getByTestId('loc').textContent).toBe(''))
  })

  it('按链接直接打开场景；场景不存在时提示并关闭', async () => {
    mList.mockResolvedValue({ scenarios: [], total: 0 })
    mGet.mockRejectedValue(new Error('资源不存在'))
    renderPage('/config/scenarios?selected=gone')
    await waitFor(() => expect(window.alert).toHaveBeenCalledWith('资源不存在'))
    await waitFor(() => expect(screen.getByTestId('loc').textContent).toBe(''))
  })

  it('内容已加载后后台重取失败：不关闭抽屉，草稿保留', async () => {
    mList.mockResolvedValue({ scenarios: [item(sc())], total: 1 })
    mGet.mockResolvedValueOnce(detail(sc()))
    const user = userEvent.setup()
    renderPage('/config/scenarios?selected=pentest')
    const name = await screen.findByDisplayValue('渗透测试')
    await user.clear(name)
    await user.type(name, '草稿中')
    mGet.mockRejectedValue(new Error('网络断开'))
    await queryClient.invalidateQueries()
    await waitFor(() => expect(mGet).toHaveBeenCalledTimes(2))
    expect(vi.mocked(window.alert).mock.calls).toEqual([])
    expect(screen.getByDisplayValue('草稿中')).toBeTruthy()
    expect(screen.getByTestId('loc').textContent).toBe('?selected=pentest')
  })

  it('保存在途时切到别的场景：迟到的结果不关闭新打开的编辑器', async () => {
    mList.mockResolvedValue({ scenarios: [item(sc()), item(sc({ code: 'ctf', name: 'CTF 夺旗', is_default: false }))], total: 2 })
    mGet.mockImplementation(async (code: string) =>
      detail(code === 'ctf' ? sc({ code: 'ctf', name: 'CTF 夺旗', is_default: false }) : sc()),
    )
    let finish!: (d: ScenarioDetail) => void
    mPatch.mockReturnValue(new Promise((r) => (finish = r)))
    const user = userEvent.setup()
    renderPage('/config/scenarios?selected=pentest')
    const name = await screen.findByDisplayValue('渗透测试')
    await user.clear(name)
    await user.type(name, '渗透')
    await user.click(screen.getByText('保存'))
    fireEvent.click(screen.getByTestId('go-ctf'))
    expect(await screen.findByDisplayValue('CTF 夺旗')).toBeTruthy()
    finish(detail(sc({ name: '渗透' })))
    await waitFor(() => expect(mPatch).toHaveBeenCalled())
    await new Promise((r) => setTimeout(r, 50))
    expect(screen.getByTestId('loc').textContent).toBe('?selected=ctf')
    expect(screen.getByDisplayValue('CTF 夺旗')).toBeTruthy()
  })

  it('全部恢复出厂经确认对话框，草稿回到出厂内容', async () => {
    const edited = sc({ body: '我改过的', description: '原说明' })
    mList.mockResolvedValue({ scenarios: [item(edited, { customized: ['body'] })], total: 1 })
    mGet.mockResolvedValue(detail(edited, { overrides: { body: { kind: 'replace', default: '情境正文', baseline: '情境正文', default_changed: false, matches_default: false } } }))
    mReset.mockResolvedValue(detail(sc({ description: '原说明' })))
    const user = userEvent.setup()
    renderPage('/config/scenarios?selected=pentest')
    const desc = await screen.findByDisplayValue('原说明')
    await user.clear(desc)
    await user.type(desc, '新说明')
    await user.click(screen.getByText('全部恢复出厂'))
    await user.click(await screen.findByRole('button', { name: '确认恢复' }))
    await waitFor(() => expect(mReset).toHaveBeenCalledWith('pentest', undefined))
    await waitFor(() => expect(screen.getByDisplayValue('原说明')).toBeTruthy())
  })

  it('自建场景经确认对话框删除，删除后关闭抽屉', async () => {
    const mine = sc({ code: 'audit', name: '代码审计', is_builtin: false, is_default: false })
    mList.mockResolvedValue({ scenarios: [item(mine)], total: 1 })
    mGet.mockResolvedValue(detail(mine))
    mDelete.mockResolvedValue()
    const user = userEvent.setup()
    renderPage('/config/scenarios?selected=audit')
    await user.click(await screen.findByText('删除'))
    await user.click(await screen.findByRole('button', { name: '确认删除' }))
    await waitFor(() => expect(mDelete).toHaveBeenCalledWith('audit'))
    await waitFor(() => expect(screen.getByTestId('loc').textContent).toBe(''))
  })

  it('新建：标识、名称、正文必填，超长不可保存', async () => {
    mList.mockResolvedValue({ scenarios: [], total: 0 })
    mCreate.mockResolvedValue(sc({ code: 'ops' }))
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: /新建/ }))
    const save = () => screen.getByText('保存').closest('button') as HTMLButtonElement
    expect(save().disabled).toBe(true)
    const inputs = screen.getAllByRole('textbox')
    await user.type(inputs[0], 'ops')
    await user.click(inputs[1])
    await user.paste('名'.repeat(65))
    expect(screen.getByText(/名称超过 64 字上限/)).toBeTruthy()
    await user.clear(inputs[1])
    await user.type(inputs[1], '运维排障')
    await user.click(screen.getByRole('tab', { name: '场景内容' }))
    const body = screen.getByRole('textbox')
    await user.click(body)
    await user.paste('长'.repeat(8001))
    expect(save().disabled).toBe(true)
    expect(screen.getByText(/超过 8000 字上限/)).toBeTruthy()
    await user.clear(body)
    await user.type(body, '排障要点')
    await user.click(save())
    await waitFor(() =>
      expect(mCreate).toHaveBeenCalledWith({ code: 'ops', name: '运维排障', description: '', body: '排障要点' }),
    )
  })

  it('内置场景删除走确认框；已删除的内置可从入口恢复', async () => {
    mList.mockResolvedValue({ scenarios: [item(sc())], total: 1 })
    mGet.mockResolvedValue(detail(sc()))
    mDelete.mockResolvedValue()
    mDeleted.mockResolvedValue([sc({ code: 'ctf', name: 'CTF 夺旗', is_default: false })])
    mRestore.mockResolvedValue(detail(sc({ code: 'ctf', name: 'CTF 夺旗', is_default: false })))
    const user = userEvent.setup()
    renderPage('/config/scenarios?selected=pentest')
    await user.click(await screen.findByText('删除'))
    expect(await screen.findByText(/可在「已删除的内置」里恢复/)).toBeTruthy()
    expect(screen.getByText(/删除后缺省即无场景/)).toBeTruthy()
    await user.click(screen.getByRole('button', { name: '确认删除' }))
    await waitFor(() => expect(mDelete).toHaveBeenCalledWith('pentest'))
    await user.click(await screen.findByRole('button', { name: '已删除的内置（1）' }))
    await user.click(await screen.findByRole('button', { name: '恢复场景「CTF 夺旗」' }))
    await waitFor(() => expect(mRestore).toHaveBeenCalledWith('ctf'))
  })
})
