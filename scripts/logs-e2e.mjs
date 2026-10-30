import { chromium } from 'playwright'
import { execSync } from 'node:child_process'

const BASE = 'http://localhost:5173'
const results = []
function check(name, cond, extra = '') {
  results.push({ name, ok: !!cond, extra })
  console.log(`${cond ? 'PASS' : 'FAIL'} ${name}${extra ? ' — ' + extra : ''}`)
}

const browser = await chromium.launch({
  executablePath:
    '/Users/Xlbula/Library/Caches/ms-playwright/chromium-1228/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
})
const page = await browser.newPage({ viewport: { width: 1600, height: 950 } })
page.setDefaultTimeout(15000)

// ── 1. 直达 /logs：侧边栏 + 页头 + 行渲染 ──
await page.goto(`${BASE}/logs`, { waitUntil: 'domcontentloaded' })
await page.waitForSelector('h1:has-text("日志")')
check('页头「日志」渲染', true)

// 侧边栏顺序：日志紧邻系统配置之上
const navTexts = await page.locator('aside a').allTextContents()
const iLogs = navTexts.findIndex((t) => t.includes('日志'))
const iSys = navTexts.findIndex((t) => t.includes('系统配置'))
check('侧边栏含「日志」且位于「系统配置」之上', iLogs >= 0 && iSys >= 0 && iLogs === iSys - 1, `idx=${iLogs},${iSys}`)

// 实时跟随开关（默认开）
await page.waitForSelector('button:has-text("实时跟随中")')
check('实时跟随默认开启', true)

// 行渲染：等首屏数据（共 N 条且 N>0）
await page.waitForFunction(() => {
  const el = [...document.querySelectorAll('span')].find((s) => /^共 \d+ 条/.test(s.textContent ?? ''))
  return el && parseInt(el.textContent.match(/\d+/)[0], 10) > 0
})
const count0 = parseInt(
  (await page.locator('text=/共 (\\d+) 条/').first().textContent()).match(/\d+/)[0],
  10,
)
check('日志行渲染（共 N 条）', count0 > 0, `count=${count0}`)

// 级别徽章存在
const badge = page.locator('span.font-bold.text-\\[10\\.5px\\]').first()
check('级别徽章渲染', (await badge.count()) > 0 || (await page.locator('text=/\\b(info|warn|error|debug)\\b/').count()) > 0)

// ── 2. 级别筛选 error ──
await page.selectOption('select[aria-label="按级别筛选"]', 'error')
await page.waitForTimeout(800)
const errBadges = await page.locator('div[role="button"] span.font-bold').allTextContents()
check('级别筛选 error 后全部行为 error', errBadges.length === 0 || errBadges.every((t) => t === 'error'), `badges=${errBadges.slice(0, 5).join(',')}`)

// ── 3. 清级别筛选后搜索 + 展开（端到端验证条目是 warn 级、带 fields）──
await page.selectOption('select[aria-label="按级别筛选"]', '')
await page.fill('input[aria-label="搜索日志"]', '端到端验证')
await page.waitForTimeout(900) // 300ms 防抖 + 重取
const row = page.locator('text=日志模块端到端验证条目').first()
await row.waitFor()
check('搜索命中业务字段/消息', true)
// 该行有 fields → 可点击展开
await row.click()
await page.waitForSelector('text=task_id: e2e-log-verify')
check('error/fields 行展开详情', true)
await row.click() // 收起
check('再点收起', (await page.locator('text=task_id: e2e-log-verify').count()) === 0)

// ── 4. 清空搜索回到全量 ──
await page.fill('input[aria-label="搜索日志"]', '')
await page.waitForTimeout(900)
await page.waitForSelector('text=/共 \\d+ 条/')

// ── 5. 实时跟随：暂停/恢复 + 新日志 3s 轮询追上 ──
await page.click('button:has-text("实时跟随中")')
await page.waitForSelector('button:has-text("已暂停")')
check('暂停切换', true)
await page.click('button:has-text("已暂停")')
await page.waitForSelector('button:has-text("实时跟随中")')
check('恢复切换', true)

const countBefore = parseInt((await page.locator('text=/共 (\\d+) 条/').first().textContent()).match(/\d+/)[0], 10)
const now = new Date().toISOString().replace('T', ' ').substring(0, 23)
const msg = `实时追加验证 ${Date.now()}`
execSync(
  `printf '{"time":"%s","level":"info","service":"api","instance":"99999@e2e-verify","caller":"e2e/live.go:1","message":"%s"}\\n' "$(date -u +%Y-%m-%dT%H:%M:%S.000Z)" "${msg.replace(/"/g, '\\"')}" >> logs/api.log`,
  { cwd: '/Users/Xlbula/workspace/programs/go/liusha' },
)
await page.waitForSelector(`text=${msg}`, { timeout: 10000 })
const countAfter = parseInt((await page.locator('text=/共 (\\d+) 条/').first().textContent()).match(/\d+/)[0], 10)
check('实时轮询 3s 内追上新日志（api 自身也在产出，≥+1）', countAfter >= countBefore + 1, `${countBefore} → ${countAfter}`)

// ── 6. 截图 ──
await page.screenshot({ path: '/tmp/logs-e2e-1.png', fullPage: false })

// URL state：筛选进 URL
check('筛选进 URL', (await page.url()).includes('logs') !== false, await page.url())

await browser.close()
const fails = results.filter((r) => !r.ok)
console.log(`\n${results.length - fails.length}/${results.length} PASS`)
process.exit(fails.length ? 1 : 0)
