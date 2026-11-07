// This is an evaluation client, not a production dependency.
const { chromium } = require(process.env.DF_PLAYWRIGHT_MODULE || 'playwright')

async function main() {
  const dpr = Number(process.env.DF_DPR || 1)
  if (!(dpr > 0)) throw new Error('DF_DPR must be a positive number')
  const browser = await chromium.launch({
    headless: process.env.DF_BENCH_HEADLESS === '1',
    ...(process.env.DF_CHROMIUM ? { executablePath: process.env.DF_CHROMIUM } : {})
  })
  const context = await browser.newContext({ viewport: { width: 1200, height: 800 }, deviceScaleFactor: dpr })
  const page = await context.newPage()
  page.on('pageerror', (error) => { console.error(error); process.exitCode = 1 })
  await page.goto(process.argv[2])
  const close = async () => { await browser.close(); process.exit(process.exitCode || 0) }
  process.on('SIGTERM', close)
  process.on('SIGINT', close)
  await new Promise(() => {})
}

main().catch((error) => { console.error(error); process.exit(1) })
