// Drive the actual Ghostty button and take the same hosted PTY back in browser.
const { chromium } = require(process.env.DF_PLAYWRIGHT_MODULE || 'playwright')
const base = process.env.DFB_BASE
const sid = process.env.DFB_SID1
const wid = process.env.DFB_WID
const token = process.env.DFB_TOKEN
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms))

;(async () => {
  const browser = await chromium.launch({headless: true, ...(process.env.DF_CHROMIUM ? {executablePath: process.env.DF_CHROMIUM} : {})})
  const page = await browser.newPage({viewport: {width:1280, height:900}})
  page.on('pageerror', error => console.error('pageerror:', error.message))
  try {
    await page.goto(base)
    await page.fill('#token', token)
    await page.click('#connect-form button[type=submit]')
    await page.waitForSelector('#app:not([hidden])')
    const row = `[data-tree-session-open="${sid}"]`
    if (!(await page.locator(row).isVisible().catch(() => false))) await page.click(`[data-wt-toggle="${wid}"]`)
    await page.locator(row).click()
    await page.waitForFunction(() => document.querySelector('#term-status')?.textContent === 'streaming')
    await page.locator('#btn-term-native').click()
    await page.waitForFunction(() => document.querySelector('#term-status')?.textContent === 'detached', null, {timeout:15000})
    await page.locator(row).click()
    await page.waitForFunction(() => document.querySelector('#term-status')?.textContent === 'streaming', null, {timeout:15000})
    await sleep(1000)
    await page.locator('.xterm-helper-textarea').focus()
    await page.keyboard.type('qzxvjkmb', {delay:30})
    await page.waitForFunction(() => document.querySelector('.xterm-rows')?.textContent.includes('qzxvjkmb'), null, {timeout:10000})
    console.log(JSON.stringify({ok:true, nativeTakeover:true, browserReattached:true, browserInput:true}))
  } finally {
    await browser.close()
  }
})().catch(error => { console.error(error.message); process.exit(1) })
