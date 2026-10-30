import { chromium } from 'playwright'

const browser = await chromium.launch({
  executablePath:
    '/Users/Xlbula/Library/Caches/ms-playwright/chromium-1228/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
})
const page = await browser.newPage({ viewport: { width: 1600, height: 950 } })
page.setDefaultTimeout(15000)

await page.goto('http://localhost:5173/llm-audit', { waitUntil: 'domcontentloaded' })
await page.waitForSelector('table tbody tr td')

// 首行 = cron 历史批次（此前无会话、无按钮）→ 现在补录后应有按钮
await page.locator('table tbody tr').first().click()
await page.waitForSelector('text=打开任务 →')
const convBtn = page.locator('text=打开对话 →')
const n = await convBtn.count()
console.log(`首行 打开对话按钮: ${n > 0 ? 'PASS' : 'FAIL'} (count=${n})`)
const href = await convBtn.first().getAttribute('href')
console.log(`href: ${href}`)
await convBtn.first().click()
await page.waitForURL(/\/conversations\/(manual|auto)\?conv=/, { timeout: 10000 })
console.log(`跳转: PASS → ${page.url()}`)
// 会话页应渲染补录的首条说明
await page.waitForSelector('text=补录', { timeout: 10000 })
console.log('补录说明可见: PASS')
await page.screenshot({ path: '/tmp/backfill-conv.png' })
await browser.close()
