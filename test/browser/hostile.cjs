// Hostile-output probe: the client must survive a process that prints every
// escape sequence worth attacking a terminal with.
//
//   test/browser/probe.sh hostile
//
// The fixture (fixture-hostile.py) writes clipboard, title, link, image and
// palette canaries, plus three sequences split across separate writes. This
// probe asserts the client's side of the contract: no clipboard call, no title
// change, no navigation, no image, no external request, no palette repaint, and
// still-answered queries on an explicit allowlist.
//
// Exit codes match the other probes: 1 precondition, 2 setup, 3 regression.
const { chromium } = require('./playwright.cjs')
const fs = require('fs')

const BASE = process.env.DFB_BASE
const TOKEN = process.env.DFB_TOKEN
const WID = process.env.DFB_WID
const SID = process.env.DFB_SID1
const INPUT_LOG = process.env.DFB_LOG

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

function fail(code, message) {
  console.error(`FAIL (${code}): ${message}`)
  process.exit(code)
}

function inputLog() {
  try {
    return fs.readFileSync(INPUT_LOG, 'utf8')
  } catch {
    return ''
  }
}

async function signIn(page) {
  await page.goto(`${BASE}/?debug=1&panel=0`)
  await page.fill('#token', TOKEN)
  await page.click('#connect-form button[type=submit]')
  try {
    await page.waitForSelector('#app:not([hidden])', { timeout: 15000 })
  } catch {
    fail(3, 'a valid token never left the connect card')
  }
  await sleep(500)
}

async function openSession(page) {
  const row = `[data-tree-session-open="${SID}"]`
  const visible = await page.locator(row).isVisible().catch(() => false)
  if (!visible) await page.click(`[data-wt-toggle="${WID}"]`)
  await page.waitForSelector(row, { timeout: 15000, state: 'visible' })
  await page.click(row)
  await page.waitForSelector('.xterm-screen', { timeout: 15000 })
  await sleep(1600)
}

async function paintedText(page) {
  return page.$eval('.xterm-rows', (el) => el.textContent || '').catch(() => '')
}

;(async () => {
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.DF_CHROMIUM ? { executablePath: process.env.DF_CHROMIUM } : {})
  })
  // A fractional scale keeps the DOM renderer, where the link run is a span and
  // the palette can be inspected as computed style. It is also the desktop case
  // a 125%/150% display produces.
  const context = await browser.newContext({
    viewport: { width: 1280, height: 900 },
    deviceScaleFactor: 1.5
  })
  const dialogs = []
  const popups = []
  const requests = []
  await context.addInitScript(() => {
    const clip = { write: 0, read: 0, calls: [] }
    const api = navigator.clipboard
    if (api) {
      const wrap = (name, key) => {
        const original = api[name]
        if (typeof original !== 'function') return
        api[name] = function (...args) {
          clip[key] += 1
          clip.calls.push(name)
          return original.apply(this, args)
        }
      }
      wrap('writeText', 'write')
      wrap('readText', 'read')
    }
    window.__clip = clip
    window.__csp = []
    document.addEventListener('securitypolicyviolation', (event) => {
      window.__csp.push(`${event.violatedDirective} ${event.blockedURI}`)
    })
  })

  const page = await context.newPage()
  // Only pages the client opens later are popups; the main page is created
  // after this listener in some Playwright versions and would look like one.
  context.on('page', (opened) => {
    if (opened !== page) popups.push(opened)
  })
  page.on('dialog', (dialog) => {
    dialogs.push(dialog.message())
    dialog.dismiss().catch(() => {})
  })
  page.on('request', (request) => requests.push(request.url()))
  page.on('pageerror', (error) => console.error('pageerror:', error.message))

  try {
    await signIn(page)
    await openSession(page)

    // The fixture writes the canaries, then sleeps and splits three sequences
    // across writes, then prints the done line. Seeing it means every byte
    // before it was parsed.
    let text = ''
    for (let i = 0; i < 60; i++) {
      await sleep(200)
      text = await paintedText(page)
      if (text.includes('hostile-fixture done')) break
    }
    if (!text.includes('hostile-fixture done')) {
      fail(2, `the hostile fixture never finished painting: ${JSON.stringify(text.slice(0, 120))}`)
    }
    if (!text.includes('LINKTEXT')) {
      fail(2, `the OSC 8 link text was never rendered: ${JSON.stringify(text.slice(0, 400))}`)
    }
    // Replies have to come back through the daemon and into the fixture's log.
    await sleep(700)

    const title = await page.title()
    if (title !== 'DarkFactory') fail(3, `OSC 0/2 changed the page title to ${JSON.stringify(title)}`)

    const clip = await page.evaluate(() => window.__clip)
    if (!clip) fail(2, 'the clipboard stub was not installed')
    if (clip.write || clip.read) {
      fail(3, `terminal output reached the clipboard: ${JSON.stringify(clip.calls)}`)
    }

    // OSC 8: a click on the run must do nothing at all.
    const link = await page.evaluate(() => {
      for (const el of document.querySelectorAll('#terminal-pane span')) {
        if ((el.textContent || '').includes('LINKTEXT')) {
          const box = el.getBoundingClientRect()
          if (box.width > 0 && box.height > 0) {
            return { x: box.x + box.width / 2, y: box.y + box.height / 2 }
          }
        }
      }
      return null
    })
    if (!link) fail(2, 'the OSC 8 link run has no clickable box')
    const before = page.url()
    await page.mouse.click(link.x, link.y)
    await sleep(700)
    if (page.url() !== before) fail(3, `an OSC 8 click navigated to ${page.url()}`)
    if (popups.length) {
      const urls = []
      for (const opened of popups) urls.push(opened.url())
      fail(3, `an OSC 8 click opened ${urls.join(', ')}`)
    }
    if (dialogs.length) fail(3, `an OSC 8 click raised a dialog: ${dialogs.join(' / ')}`)

    const dom = await page.evaluate(() => {
      const pane = document.getElementById('terminal-pane')
      if (!pane) return null
      const banned = ['rgb(255, 0, 0)', 'rgb(16, 0, 0)', 'rgb(0, 16, 0)', 'rgb(0, 0, 16)']
      const backgrounds = new Set()
      let repainted = ''
      let dataRefs = 0
      for (const el of pane.querySelectorAll('*')) {
        const style = getComputedStyle(el)
        const bg = style.backgroundColor
        if (bg && bg !== 'rgba(0, 0, 0, 0)') backgrounds.add(bg)
        if (banned.includes(bg)) repainted = `${el.className || el.tagName}: ${bg}`
        const src = el.getAttribute('src') || ''
        if (/^(data|blob):/.test(src)) dataRefs += 1
        if (/url\("?(data|blob):/.test(style.backgroundImage || '')) dataRefs += 1
      }
      return {
        repainted,
        dataRefs,
        images: pane.querySelectorAll('img, image, picture').length,
        backgrounds: [...backgrounds]
      }
    })
    if (!dom) fail(2, 'the terminal pane disappeared')
    if (dom.repainted) fail(3, `terminal output repainted the palette: ${dom.repainted}`)
    if (dom.images) fail(3, `terminal output created ${dom.images} image element(s)`)
    if (dom.dataRefs) fail(3, `terminal output created ${dom.dataRefs} data:/blob: reference(s)`)

    const csp = await page.evaluate(() => window.__csp || [])
    if (csp.length) fail(3, `the page raised CSP violations: ${csp.join(' / ')}`)

    const external = requests.filter((url) => !url.startsWith(BASE) && !url.startsWith('data:'))
    if (external.length) fail(3, `the page made external requests: ${external.join(', ')}`)
    if (requests.some((url) => url.includes('evil.invalid'))) fail(3, 'the OSC 8 URL was requested')

    // The terminal's own replies are the only output allowed to reach the PTY.
    const log = inputLog()
    const need = [
      [/\x1b\]10;rgb:[0-9a-f]+\/[0-9a-f]+\/[0-9a-f]+/, 'OSC 10 query reply'],
      [/\x1b\]11;rgb:[0-9a-f]+\/[0-9a-f]+\/[0-9a-f]+/, 'OSC 11 query reply'],
      [/\x1b\]4;1;rgb:[0-9a-f]+\/[0-9a-f]+\/[0-9a-f]+/, 'OSC 4 query reply'],
      [/\x1b\[\?1;2c/, 'DA1 reply'],
      [/\x1bP[01]\$r/, 'DECRQSS reply']
    ]
    for (const [pattern, label] of need) {
      if (!pattern.test(log)) fail(3, `the ${label} never reached the PTY`)
    }
    if (/stolen-clipboard/.test(log)) fail(3, 'the OSC 52 payload echoed into the PTY')

    console.log(
      `PASS hostile: 12 canaries; no clipboard, title, navigation, image, ` +
        `external request or palette repaint; ${need.length} allowlisted replies; ` +
        `backgrounds ${dom.backgrounds.join(' ')}`
    )
  } finally {
    await browser.close()
  }
})().catch((error) => {
  console.error(error)
  process.exit(1)
})
