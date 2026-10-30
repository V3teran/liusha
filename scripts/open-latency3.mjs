import { chromium } from 'playwright'
const browser = await chromium.launch({
  executablePath:
    '/Users/Xlbula/Library/Caches/ms-playwright/chromium-1228/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
})
const page = await browser.newPage({ viewport: { width: 1600, height: 950 } })
await page.goto('http://localhost:4173/conversations/manual', { waitUntil: 'domcontentloaded' })
await page.waitForSelector('text=liusha')
await page.waitForTimeout(800)
for (const [label, sel] of [['LLM 配置', 'h1:has-text("LLM 配置")'], ['日志', 'h1:has-text("日志")'], ['凭证库', 'h1:has-text("凭证库")'], ['日志', 'h1:has-text("日志")']]) {
  const t0 = Date.now()
  await page.click(`aside a:has-text("${label}")`)
  await page.waitForSelector(sel, { timeout: 15000 })
  console.log(`click→壳(${label}): ${Date.now() - t0}ms`)
  await page.goto('http://localhost:4173/conversations/manual', { waitUntil: 'domcontentloaded' })
  await page.waitForSelector('text=liusha')
  await page.waitForTimeout(600)
}
await browser.close()
