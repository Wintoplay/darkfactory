// Browser probes for the phone client, driven against a real PTY.
//
//   node probe.cjs attach   -> C1: clicks survive attach, tab switch, reload
//   node probe.cjs session  -> C2: the DOM session-tab "+" creates a live PTY
//
// The daemon, the token and the session ids come from test/browser/lib.sh.
// Run through test/browser/probe.sh so the fixture is started and torn down.
const { chromium } = require('./playwright.cjs')
const fs = require('fs')

const BASE = process.env.DFB_BASE
const TOKEN = process.env.DFB_TOKEN
const WID = process.env.DFB_WID
const SID1 = process.env.DFB_SID1
const SID2 = process.env.DFB_SID2
const INPUT_LOG = process.env.DFB_LOG
const MODE = process.argv[2] || 'attach'

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

// Every SGR mouse report is a press or a release. Counting presses proves the
// click reached the PTY without depending on unrelated keystrokes.
function sgrPresses(text) {
  return (text.match(/\x1b\[<0;\d+;\d+M/g) || []).length
}

async function signIn(page) {
  // Make the cold-load cookie probe answer late, which is what a real phone
  // network does. The user can paste a token before that 401 lands; it must not
  // tear down the session the newest attempt just established.
  await page.route('**/api/v1/state', async (route) => {
    if (!route.request().headers().authorization) await sleep(400)
    await route.continue()
  })
  await page.goto(`${BASE}/?debug=1&panel=0`)
  await page.fill('#token', TOKEN)
  await page.click('#connect-form button[type=submit]')
  try {
    await page.waitForSelector('#app:not([hidden])', { timeout: 15000 })
  } catch {
    fail(3, 'a valid token never left the connect card (lost to a racing unauthenticated probe)')
  }
  await sleep(700)
  if (await page.$eval('#connect', (el) => !el.hidden)) {
    fail(3, 'a valid token left the connect card visible')
  }
}

async function openSession(page, sid) {
  await page.click(`[data-wt-toggle="${WID}"]`)
  await page.waitForSelector(`[data-tree-session-open="${sid}"]`, { timeout: 15000 })
  await page.click(`[data-tree-session-open="${sid}"]`)
  await page.waitForSelector('.xterm-screen', { timeout: 15000 })
  await sleep(1600)
}

async function probeAttach(page) {
  await signIn(page)
  await openSession(page, SID1)
  const painted = await page.$eval('.xterm-rows', (el) => el.textContent || '')
  if (!painted.includes('mouse-tui')) {
    fail(2, `the pane never painted the fixture: ${JSON.stringify(painted.slice(0, 80))}`)
  }

  const box = await (await page.$('.xterm-screen')).boundingBox()
  const click = async (label) => {
    const before = sgrPresses(inputLog())
    // A pointer click, not a tap: xterm encodes both the same way once mouse
    // tracking is on, and a mouse event is what this driver can synthesize.
    await page.mouse.click(box.x + box.width * 0.4, box.y + box.height * 0.4)
    for (let i = 0; i < 30; i++) {
      await sleep(100)
      if (sgrPresses(inputLog()) > before) return
    }
    fail(3, `${label}: the click never reached the PTY (no SGR mouse report)`)
  }

  await click('first attach')

  await page.click(`#session-tabs [data-session="${SID2}"]`)
  await sleep(1500)
  await page.click(`#session-tabs [data-session="${SID1}"]`)
  await sleep(1600)
  await click('after switching away and back')

  // A reload throws xterm away, including any mode state it had learned. The
  // restore has to come from the daemon, which is what makes reconnect work.
  await page.reload()
  await page.waitForSelector('#app:not([hidden])', { timeout: 15000 })
  await openSession(page, SID1)
  await click('after reload')

  const log = inputLog()
  if (!/\x1b\[<0;\d+;\d+m/.test(log)) {
    fail(3, 'the PTY received a press but no release')
  }
  console.log(`PASS attach: 3 clicks encoded, ${sgrPresses(log)} SGR presses`)
}

async function probeSession(page) {
  await signIn(page)
  await openSession(page, SID1)
  const before = await page.$$eval('#session-tabs [data-session]', (els) =>
    els.map((el) => el.dataset.session))

  // The outer DOM control, not OpenCode's drawn "+" inside the TUI. With more
  // tabs than fit, the add control must still be inside the viewport: a phone
  // has no obvious way to discover it past the last tab.
  const addBox = await (await page.$('[data-session-add]')).boundingBox()
  if (!addBox) fail(3, 'the session-tab add control is not rendered')
  if (addBox.x < 0 || addBox.x + addBox.width > 396.5) {
    fail(3, `the session-tab add control is scrolled out of view: x=${Math.round(addBox.x)} w=${Math.round(addBox.width)}`)
  }
  await page.click('[data-session-add]')
  await sleep(300)
  const dialog = await page.evaluate(() => {
    const dlg = document.getElementById('dlg-session')
    return {
      open: Boolean(dlg && dlg.open),
      target: document.getElementById('sess-target')?.textContent || '',
      repoHidden: document.getElementById('sess-repo-field')?.hidden,
      agent: document.getElementById('sess-agent')?.value || ''
    }
  })
  if (!dialog.open) fail(2, 'the session-tab "+" did not open the session dialog')
  if (!dialog.repoHidden) fail(3, 'the dialog did not scope itself to the open worktree')
  if (!dialog.target.includes('repo')) fail(3, `unexpected dialog target: ${dialog.target}`)

  const created = page.waitForResponse(
    (r) => r.url().endsWith('/api/v1/sessions') && r.request().method() === 'POST')
  await page.click('#dlg-session button[type=submit]')
  const response = await created
  const body = await response.json()
  if (response.status() !== 201) fail(2, `create returned ${response.status()}`)
  if (!body.id || body.worktreeId !== WID) fail(2, `unexpected session: ${JSON.stringify(body)}`)
  if (body.live !== true) fail(3, 'the created session is not live')
  if (before.includes(body.id)) fail(2, 'create reused an existing session id')

  await page.waitForSelector(`#session-tabs [data-session="${body.id}"]`, { timeout: 10000 })
  await sleep(1600)
  const status = await page.$eval('#term-status', (el) => el.textContent)
  if (status !== 'streaming') fail(3, `terminal status after create = ${status}`)

  // Input must reach the new PTY, not just the UI state. A TUI hides the
  // compose row, so type through the terminal itself after focusing it.
  const marker = `probe-${Date.now()}`
  const pane = await (await page.$('.xterm-screen')).boundingBox()
  await page.mouse.click(pane.x + pane.width * 0.3, pane.y + pane.height * 0.3)
  await page.keyboard.type(marker)
  for (let i = 0; i < 30; i++) {
    await sleep(100)
    // The fixture logs one read per line, so a marker split across reads would
    // not match with the separators in place.
    if (inputLog().replace(/\n/g, '').includes(marker)) break
  }
  if (!inputLog().replace(/\n/g, '').includes(marker)) fail(3, 'typed input never reached the new PTY')

  if (await page.$eval('#connect', (el) => !el.hidden)) {
    fail(3, 'the client bounced back to the connect card after a valid login')
  }
  console.log(`PASS session: created ${body.id} in ${body.worktreeId}, live, input delivered`)
}

;(async () => {
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.DF_CHROMIUM ? { executablePath: process.env.DF_CHROMIUM } : {})
  })
  const context = await browser.newContext({
    viewport: { width: 396, height: 719 },
    deviceScaleFactor: 2.72727
  })
  const page = await context.newPage()
  page.on('pageerror', (error) => console.error('pageerror:', error.message))
  try {
    if (MODE === 'attach') await probeAttach(page)
    else if (MODE === 'session') await probeSession(page)
    else fail(1, `unknown probe mode ${MODE}`)
  } finally {
    await browser.close()
  }
})().catch((error) => {
  console.error(error)
  process.exit(1)
})
