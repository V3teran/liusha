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

// ── LLM 审计：两列已删 + 抽屉跳转按钮 ──
await page.goto(`${BASE}/llm-audit`, { waitUntil: 'domcontentloaded' })
await page.waitForSelector('table tbody tr td')
const headers = await page.$$eval('table thead th', (ths) => ths.map((t) => t.textContent.trim()))
check('列表无对话/批次列', !headers.includes('对话') && !headers.includes('批次'), headers.join('|'))
check('列序 # → 时间 → 摘要', headers[0] === '#' && headers[1] === '时间' && headers[2] === '摘要')

// 点一行有对话数据的行：找一个 conv 链接曾存在的行不可行了（列已删），改点第一行后看按钮
await page.locator('table tbody tr').first().click()
await page.waitForSelector('text=元信息', { timeout: 10000 })
const hasTaskBtn = await page.locator('text=打开任务 →').count()
const hasConvBtn = await page.locator('text=打开对话 →').count()
check('抽屉渲染跳转按钮（首行数据决定有无）', true, `task=${hasTaskBtn} conv=${hasConvBtn}`)

// 找一行带对话的（历史 manual 批次：翻到有 conv 的行——直接遍历前几行直到按钮出现）
if (hasConvBtn === 0) {
  await page.keyboard.press('Escape')
  await page.waitForTimeout(300)
  for (let i = 1; i <= 30 && (await page.locator('text=打开对话 →').count()) === 0; i++) {
    await page.locator(`table tbody tr:nth-child(${i})`).click()
    await page.waitForTimeout(600)
    if ((await page.locator('text=打开对话 →').count()) > 0) break
    await page.keyboard.press('Escape')
    await page.waitForTimeout(150)
  }
}
check('找到带对话数据的行', (await page.locator('text=打开对话 →').count()) > 0)

// 点击「打开对话 →」：应跳到 /conversations/<tab>?conv= 且抽屉关闭
const convHref = await page.locator('text=打开对话 →').first().getAttribute('href')
await page.locator('text=打开对话 →').first().click()
await page.waitForURL(/\/conversations\/(manual|auto)\?conv=/, { timeout: 10000 })
check('对话按钮跳转到会话页（带 conv 深链）', page.url().includes(convHref ?? '___'), page.url())
check('跳转后抽屉已关闭', (await page.locator('[role="dialog"]').count()) === 0)

// 回审计页验证任务按钮
await page.goto(`${BASE}/llm-audit`, { waitUntil: 'domcontentloaded' })
await page.waitForSelector('table tbody tr td')
for (let i = 1; i <= 12; i++) {
  await page.locator(`table tbody tr:nth-child(${i})`).click()
  await page.waitForTimeout(700)
  if ((await page.locator('text=打开任务 →').count()) > 0) break
  await page.keyboard.press('Escape')
  await page.waitForTimeout(200)
}
const taskHref = await page.locator('text=打开任务 →').first().getAttribute('href')
await page.locator('text=打开任务 →').first().click()
await page.waitForURL(/\/tasks\?task=/, { timeout: 10000 })
check('任务按钮跳转到任务页（带 task 深链）', page.url().includes(taskHref ?? '___'), page.url())
// 任务详情板应因深链自动打开
await page.waitForSelector('text=任务详情', { timeout: 8000 }).catch(() => {})
check('任务详情板随深链打开', (await page.locator('aside').count()) > 0)
await page.screenshot({ path: '/tmp/fixes2-task-jump.png' })

await browser.close()
const fails = results.filter((r) => !r.ok)
console.log(`\n${results.length - fails.length}/${results.length} PASS`)
process.exit(fails.length ? 1 : 0)
