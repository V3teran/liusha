import { chromium } from 'playwright'
const browser = await chromium.launch({
  executablePath:
    '/Users/Xlbula/Library/Caches/ms-playwright/chromium-1228/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
})
const page = await browser.newPage({ viewport: { width: 1600, height: 950 } })
// 热会话：一次性加载应用，之后全部 SPA 内导航（真实使用形态）
await page.goto('http://localhost:4173/conversations/manual', { waitUntil: 'domcontentloaded' })
await page.waitForSelector('text=liusha')
await page.waitForTimeout(1000)
// 预热日志 chunk（首次进入会话的懒加载只发生一次）
await page.click('aside a:has-text("日志")')
await page.waitForSelector('h1:has-text("日志")')
await page.click('aside a:has-text("对话")')
await page.waitForTimeout(500)
for (let i = 1; i <= 4; i++) {
  const t0 = Date.now()
  await page.click('aside a:has-text("日志")')
  await page.waitForSelector('h1:has-text("日志")')
  const tH1 = Date.now() - t0
  await page.waitForSelector('[aria-label^="日志："]', { timeout: 30000 })
  console.log(`热会话第${i}次 click→壳 ${tH1}ms | click→首行数据 ${Date.now() - t0}ms`)
  await page.click('aside a:has-text("对话")')
  await page.waitForTimeout(400)
}
await browser.close()
