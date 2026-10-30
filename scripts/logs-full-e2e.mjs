import { chromium } from 'playwright'
import { execSync } from 'node:child_process'

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

const countText = async () =>
  (await page.locator('span', { hasText: /^共 \d+ 条/ }).first().textContent()).match(/\d+/)?.[0]
const waitCount = async (min = 1) =>
  page.waitForFunction(
    (m) => {
      const el = [...document.querySelectorAll('span')].find((s) => /^共 \d+ 条/.test(s.textContent ?? ''))
      return el && parseInt(el.textContent.match(/\d+/)[0], 10) >= m
    },
    min,
  )

// ══ 1. 加载 ══
await page.goto(`${BASE}/logs`, { waitUntil: 'domcontentloaded' })
await page.waitForSelector('h1:has-text("日志")')
await waitCount(1)
const c0 = parseInt(await countText(), 10)
check('加载渲染（行 + 计数）', c0 > 0, `count=${c0}`)
const truncatedHint = await page.locator('text=窗口外更早未加载').count()
check('窗口诚实性提示', truncatedHint === 1)
const liveBtn = page.locator('button:has-text("实时跟随中")')
check('实时跟随默认开', (await liveBtn.count()) === 1)

// ══ 2. 统一行展开（任意行）══
const firstRow = page.locator('[role="button"][aria-label^="日志"]').first()
await firstRow.click()
const openDetail = await page.locator('div').filter({ hasText: /^message: / }).count()
check('任意行可展开（message 全文）', openDetail > 0)
await firstRow.click()
check('再点收起', (await page.evaluate(() => document.querySelectorAll('[aria-expanded="true"]').length)) === 0)

// ══ 3. 进程筛选 ══
await page.selectOption('select[aria-label="按进程筛选"]', 'runner')
// 冷扫描（runner 33MB 窗口）秒级：等 DOM 稳定为「只有 runner 行」，而非量到旧帧
await page.waitForFunction(
  () => {
    const spans = [...document.querySelectorAll('span')]
    const runner = spans.filter((x) => /\[runner ·/.test(x.textContent ?? '')).length
    const other = spans.filter((x) => /\[(api|proxy) ·/.test(x.textContent ?? '')).length
    return runner > 0 && other === 0
  },
  { timeout: 15000 },
)
check('进程筛选 runner：只有 runner 行', true)
await page.selectOption('select[aria-label="按进程筛选"]', '')
await page.waitForTimeout(1200)

// ══ 4. 实例筛选（选项来自 /logs/processes）══
const instOptions = await page.locator('select[aria-label="按实例筛选"] option').allTextContents()
check('实例下拉有候选', instOptions.length > 1, instOptions.slice(0, 3).join(','))
const instVal = await page.locator('select[aria-label="按实例筛选"] option:nth-child(2)').getAttribute('value')
await page.selectOption('select[aria-label="按实例筛选"]', instVal)
await page.waitForTimeout(1200)
const pickedPid = instVal.split('@')[0]
// 所有行内 instance 短标都是被选中的 pid
const mismatch = await page.evaluate((pid) => {
  const srcs = [...document.querySelectorAll('main span')].filter((x) => /^\[.+ · .+\]$/.test(x.textContent ?? ''))
  return srcs.filter((x) => !x.textContent.includes(` · ${pid}`)).length
}, pickedPid)
check('实例筛选：所有行都是选中实例', mismatch === 0, `picked=${pickedPid} mismatch=${mismatch}`)
await page.selectOption('select[aria-label="按实例筛选"]', '')

// ══ 5. 级别筛选 ══
await page.selectOption('select[aria-label="按级别筛选"]', 'error')
await page.waitForFunction(
  () => {
    // 限定 main：侧边栏品牌字也是 font-bold，会混入全页匹配
    const badges = [...document.querySelectorAll('main span.font-bold')]
    return badges.length > 0 && badges.every((b) => b.textContent === 'error')
  },
  { timeout: 15000 },
)
check('级别筛选 error：全部行 error', true)
await page.selectOption('select[aria-label="按级别筛选"]', '')

// ══ 6. 搜索（服务端；消息 + 业务字段）══
await page.fill('input[aria-label="搜索日志"]', 'ZZZ-无匹配-XYZ')
await page.waitForSelector('text=当前筛选无匹配日志', { timeout: 15000 })
check('空态：无匹配文案', true)
await page.fill('input[aria-label="搜索日志"]', '端到端验证')
await page.locator('text=端到端验证').first().waitFor({ timeout: 15000 })
check('搜索命中（消息子串）', true)
await page.fill('input[aria-label="搜索日志"]', '')
await page.waitForTimeout(500)
await waitCount(1)

// ══ 7. 实时追新 ══
const before = parseInt(await countText(), 10)
const msg = `联调追新 ${Date.now()}`
execSync(
  `printf '{"time":"%s","level":"info","service":"api","instance":"99999@e2e-verify","caller":"e2e/live.go:1","message":"%s"}\\n' "$(date -u +%Y-%m-%dT%H:%M:%S.000Z)" "${msg}" >> logs/api.log`,
  { cwd: '/Users/Xlbula/workspace/programs/go/liusha' },
)
await page.waitForSelector(`text=${msg}`, { timeout: 10000 })
const after = parseInt(await countText(), 10)
check('实时 3s 轮询追新', after >= before + 1, `${before} → ${after}`)

// ══ 8. 暂停/恢复 ══
await page.click('button:has-text("实时跟随中")')
await page.waitForSelector('button:has-text("已暂停")')
const pausedCount = parseInt(await countText(), 10)
execSync(
  `printf '{"time":"%s","level":"info","service":"api","instance":"99999@e2e-verify","caller":"e2e/live.go:2","message":"暂停期间写入 %s"}\\n' "$(date -u +%Y-%m-%dT%H:%M:%S.000Z)" "${Date.now()}" >> logs/api.log`,
  { cwd: '/Users/Xlbula/workspace/programs/go/liusha' },
)
await page.waitForTimeout(4500)
const stillPaused = parseInt(await countText(), 10)
check('暂停期间不拉新', stillPaused === pausedCount, `${pausedCount} == ${stillPaused}`)
await page.click('button:has-text("已暂停")')
await page.waitForSelector('button:has-text("实时跟随中")')
await page.waitForTimeout(1000)
const resumed = parseInt(await countText(), 10)
check('恢复后游标补齐', resumed >= stillPaused + 1, `${stillPaused} → ${resumed}`)

// ══ 9. 上滚暂停 + 回底条 ══
await waitCount(1)
await page.evaluate(() => {
  // 日志滚动容器 = main 下唯一的 overflow-y-auto（侧边栏 nav 在 aside，不会误中）
  const el = document.querySelector('main .overflow-y-auto')
  el.scrollTop = 0
  el.dispatchEvent(new Event('scroll'))
})
await page.waitForTimeout(4000) // 等一轮新日志触发回底条
execSync(
  `printf '{"time":"%s","level":"info","service":"api","instance":"99999@e2e-verify","caller":"e2e/live.go:3","message":"上滚期间新日志 %s"}\\n' "$(date -u +%Y-%m-%dT%H:%M:%S.000Z)" "${Date.now()}" >> logs/api.log`,
  { cwd: '/Users/Xlbula/workspace/programs/go/liusha' },
)
const followBar = page.locator('button').filter({ hasText: /有新日志（\d+）/ })
await followBar.waitFor({ timeout: 10000 })
check('上滚出现回底条（含堆积数）', true)
await followBar.click()
await page.waitForTimeout(300)
check('点击回底后提示条消失', (await followBar.count()) === 0)

// ══ 10. URL state 持久化 ══
await page.selectOption('select[aria-label="按进程筛选"]', 'proxy')
await page.waitForTimeout(400) // 两次 select 间隔一帧，避免第二个 onChange 闭包读到旧 filters
await page.selectOption('select[aria-label="按级别筛选"]', 'warn')
await page.waitForTimeout(1200)
check('筛选进 URL', page.url().includes('process=proxy') && page.url().includes('level=warn'), page.url())
await page.reload({ waitUntil: 'domcontentloaded' })
await page.waitForSelector('select[aria-label="按进程筛选"] option[value="proxy"]', { state: 'attached' }) // options 来自 /processes 查询
const procVal = await page.locator('select[aria-label="按进程筛选"]').inputValue()
const levelVal = await page.locator('select[aria-label="按级别筛选"]').inputValue()
check('刷新后筛选还原', procVal === 'proxy' && levelVal === 'warn', `${procVal}/${levelVal}`)

// ══ 11. 明暗主题视觉 ══
await page.goto(`${BASE}/logs`, { waitUntil: 'domcontentloaded' })
await waitCount(1)
await page.screenshot({ path: '/tmp/logs-theme-dark.png' })
await page.evaluate(() => {
  document.documentElement.classList.remove('dark')
  document.documentElement.style.colorScheme = 'light'
})
await page.waitForTimeout(300)
await page.screenshot({ path: '/tmp/logs-theme-light.png' })

await browser.close()
const fails = results.filter((r) => !r.ok)
console.log(`\n${results.length - fails.length}/${results.length} PASS`)
process.exit(fails.length ? 1 : 0)
