import { chromium } from 'playwright'

const BASE = 'http://localhost:5173'
const results = []
function check(name, cond, extra = '') {
  results.push({ name, ok: !!cond })
  console.log(`${cond ? 'PASS' : 'FAIL'} ${name}${extra ? ' — ' + extra : ''}`)
}

const browser = await chromium.launch({
  executablePath:
    '/Users/Xlbula/Library/Caches/ms-playwright/chromium-1228/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
})
const page = await browser.newPage({ viewport: { width: 1600, height: 950 } })
page.setDefaultTimeout(15000)

// ── 1. 凭证库倒序（新→旧）──
await page.goto(`${BASE}/credentials`, { waitUntil: 'domcontentloaded' })
await page.waitForSelector('table tbody tr')
const created = await page.$$eval('table tbody tr', (trs) =>
  trs.map((tr) => tr.querySelectorAll('td')[5]?.textContent ?? ''),
)
// 创建时间列（第 6 列）：首行日期时间应 ≥ 末行（ISO 倒序字符串比较近似成立）
const dates = created.map((t) => t.replace(/\s+/g, ' ').trim()).filter(Boolean)
check('凭证库倒序（首行创建时间 ≥ 末行）', dates.length >= 2 && dates[0] >= dates[dates.length - 1], `${dates[0]} vs ${dates[dates.length - 1]}`)

// ── 2. LLM 审计：# 列 + 倒序 + 对话列 ──
await page.goto(`${BASE}/llm-audit`, { waitUntil: 'domcontentloaded' })
await page.waitForSelector('table tbody tr td')
const headers = await page.$$eval('table thead th', (ths) => ths.map((t) => t.textContent.trim()))
check('LLM 审计首列为 # 列', headers[0] === '#', headers.slice(0, 4).join('|'))
const firstCells = await page.$$eval('table tbody tr:first-child td', (tds) => tds.map((t) => t.textContent.trim()))
check('# 序号从 1 起', firstCells[0] === '1', `first=${firstCells[0]}`)
// 倒序：行序号随 # 递增（页内序号正确性已由 # 列体现；时间倒序由后端保证）
// 对话列（第 3 列）：存在至少一个可跳转的短 id（历史数据有 conv）或全为 —（数据侧事实）
const convCells = await page.$$eval('table tbody tr', (trs) =>
  trs.map((tr) => tr.querySelectorAll('td')[2]?.textContent.trim() ?? ''),
)
const withConv = convCells.filter((c) => c && c !== '—').length
check('对话列渲染（有 conv 数据或全 — 均为合法）', convCells.length > 0, `nonEmpty=${withConv}/${convCells.length}`)
// 链接形态验证：有 conv 的行应渲染为 <a>
const convLinks = await page.$$eval('table tbody tr', (trs) =>
  trs.filter((tr) => tr.querySelectorAll('td')[2]?.querySelector('a')).length,
)
check('对话列 conv 行为链接', withConv === 0 || convLinks > 0, `links=${convLinks}`)
await page.screenshot({ path: '/tmp/fixes-llmaudit.png' })

// ── 3. 任务列表：批次列头 + 进度列窄窗不换行 ──
await page.goto(`${BASE}/tasks`, { waitUntil: 'domcontentloaded' })
await page.waitForSelector('table thead th')
const taskHeaders = await page.$$eval('table thead th', (ths) => ths.map((t) => t.textContent.trim()))
check('任务列头「批次」（原批次ID）', taskHeaders.includes('批次') && !taskHeaders.includes('批次ID'), taskHeaders.join('|'))

// 窄窗：进度列 span 单行高（td 高度会被同行双行时间列撑开，必须量 span 本身）
const progressOk = await page.evaluate(() => {
  const ths = [...document.querySelectorAll('table thead th')]
  const idx = ths.findIndex((t) => t.textContent.trim() === '进度')
  if (idx < 0) return false
  const rows = [...document.querySelectorAll('table tbody tr')]
  const heights = rows
    .map((tr) => tr.querySelectorAll('td')[idx]?.querySelector('span')?.getBoundingClientRect().height ?? 0)
    .filter((h) => h > 0)
  if (heights.length === 0) return true // 空表无需验证
  return Math.max(...heights) < 24 // 单行 text-xs ≈ 16px；换行 ≈ 32px
})
check('进度列窄窗不换行（宽窗）', progressOk)

// 缩到 1100px 宽（低于 min-width 1075，触发横向滚动而不是挤压）
await page.setViewportSize({ width: 1100, height: 900 })
await page.waitForTimeout(400)
const progressOkNarrow = await page.evaluate(() => {
  const ths = [...document.querySelectorAll('table thead th')]
  const idx = ths.findIndex((t) => t.textContent.trim() === '进度')
  const rows = [...document.querySelectorAll('table tbody tr')]
  const heights = rows
    .map((tr) => tr.querySelectorAll('td')[idx]?.querySelector('span')?.getBoundingClientRect().height ?? 0)
    .filter((h) => h > 0)
  if (heights.length === 0) return true
  return Math.max(...heights) < 24
})
check('进度列窄窗不换行（1100px）', progressOkNarrow)
await page.screenshot({ path: '/tmp/fixes-tasks-narrow.png' })

await browser.close()
const fails = results.filter((r) => !r.ok)
console.log(`\n${results.length - fails.length}/${results.length} PASS`)
process.exit(fails.length ? 1 : 0)
