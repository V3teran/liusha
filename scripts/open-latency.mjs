import { chromium } from 'playwright'
const browser = await chromium.launch({
  executablePath:
    '/Users/Xlbula/Library/Caches/ms-playwright/chromium-1228/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
})
const page = await browser.newPage({ viewport: { width: 1600, height: 950 } })
await page.goto('http://localhost:5173/conversations/manual', { waitUntil: 'domcontentloaded' })
await page.waitForSelector('text=liusha')
for (let i = 1; i <= 3; i++) {
  const t0 = Date.now()
  await page.click('aside a:has-text("日志")')
  await page.waitForSelector('[aria-label^="日志："]', { timeout: 30000 })
  console.log(`第${i}次打开 → 首行数据 ${Date.now() - t0}ms`)
  await page.goto('http://localhost:5173/conversations/manual', { waitUntil: 'domcontentloaded' })
  await page.waitForSelector('text=liusha')
  await page.waitForTimeout(500)
}
await browser.close()
