// The in-app terminal pane must yield to a modal dialog.
//
// The desktop shell puts a GTK VTE pane *above* the WebView, so any HTML modal
// that lands on the terminal rectangle is painted under it: invisible and
// unclickable. "Stop session" and "New session" both open a modal from the
// session view, so both looked like a hang. The page hides the pane (it does
// not detach it) while a dialog is open and shows it again when the dialog
// ends; this probe drives the real page against a real daemon with a stub for
// the shell's message bridge and asserts that sequence.
//
//   test/browser/probe.sh native-dialog
//
// Exit codes match the other probes: 1 precondition, 2 setup, 3 regression.
const { chromium } = require('./playwright.cjs')

const BASE = process.env.DFB_BASE
const TOKEN = process.env.DFB_TOKEN
const WID = process.env.DFB_WID
const SID1 = process.env.DFB_SID1

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

function fail(code, message) {
  console.error(`FAIL (${code}): ${message}`)
  process.exit(code)
}

// The shell's bridge is one function, injected into the page. Stand in for it
// and keep every message so the assertions can inspect the order.
const STUB = `
  window.__dfMsgs = []
  window.__dfNativeSend = function (m) {
    window.__dfMsgs.push(m)
    if (m && m.type === 'open') {
      setTimeout(() => window.__dfTerminalReady && window.__dfTerminalReady(), 0)
    }
  }
`

async function messages(page) {
  return page.evaluate(() => window.__dfMsgs || [])
}

async function waitForMessage(page, type, timeout = 5000) {
  const deadline = Date.now() + timeout
  let seen = []
  while (Date.now() < deadline) {
    seen = await messages(page)
    if (seen.some((m) => m.type === type)) return seen
    await sleep(50)
  }
  return seen
}

async function openTopDialog(page) {
  return page.evaluate(() => {
    const dialog = document.querySelector('dialog[open]')
    if (!dialog) return null
    const box = dialog.getBoundingClientRect()
    const at = document.elementFromPoint(box.x + box.width / 2, box.y + 20)
    return {
      id: dialog.id,
      covers: Boolean(at && dialog.contains(at)),
      topmost: at ? at.tagName.toLowerCase() + (at.id ? '#' + at.id : '') : 'null'
    }
  })
}

async function signIn(page) {
  await page.goto(`${BASE}/?native=1&debug=1&panel=0`)
  await page.fill('#token', TOKEN)
  await page.click('#connect-form button[type=submit]')
  await page.waitForSelector('#app:not([hidden])', { timeout: 15000 })
  await sleep(600)
}

async function openSession(page) {
  const link = `[data-tree-session-open="${SID1}"]`
  // The tree may already be expanded, depending on what the last view left.
  if (!(await page.$(link))) await page.click(`[data-wt-toggle="${WID}"]`)
  await page.waitForSelector(link, { timeout: 15000 })
  await page.click(link)
  await sleep(800)
}

async function main() {
  const browser = await chromium.launch({ args: ['--no-sandbox'] })
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
  try {
    await page.addInitScript(STUB)
    await signIn(page)
    await openSession(page)

    // The page asks for a pane instead of the browser stream: one real
    // capability from the daemon, never a browser stream at the same time.
    const opened = await waitForMessage(page, 'open', 10000)
    if (!opened.some((m) => m.type === 'open')) {
      fail(3, `opening a session never asked the shell for a pane: ${JSON.stringify(opened)}`)
    }
    if (!opened.some((m) => m.type === 'open' && m.pane && m.sessionId === SID1)) {
      fail(3, 'the open message carried no pane capability for the session')
    }

    // New session, the control the owner reported as a hang.
    await page.click('[data-session-add]')
    const afterAdd = await waitForMessage(page, 'hide', 4000)
    const addDialog = await openTopDialog(page)
    if (!afterAdd.some((m) => m.type === 'hide')) {
      fail(3, `the New session dialog did not hide the pane: ${JSON.stringify(afterAdd)}`)
    }
    if (!addDialog || addDialog.id !== 'dlg-session') {
      fail(3, `New session did not open dlg-session: ${JSON.stringify(addDialog)}`)
    }
    if (!addDialog.covers) {
      fail(3, `the session dialog was not the topmost element at its own centre: ${addDialog.topmost}`)
    }

    // Closing it must bring the pane back, not leave a black rectangle.
    await page.click('#dlg-session [data-close]')
    const afterAddClose = await waitForMessage(page, 'show', 4000)
    if (!afterAddClose.some((m) => m.type === 'show')) {
      fail(3, `closing the session dialog did not restore the pane: ${JSON.stringify(afterAddClose)}`)
    }
    if (await openTopDialog(page)) fail(3, 'the session dialog stayed open after Cancel')

    // Stop session, the other control the owner reported as a hang: it opens a
    // confirmation before it deletes anything.
    await page.evaluate(() => { window.__dfMsgs = [] })
    await page.click('#btn-stop-session')
    const afterStop = await waitForMessage(page, 'hide', 4000)
    const confirm = await openTopDialog(page)
    if (!afterStop.some((m) => m.type === 'hide')) {
      fail(3, `the Stop confirmation did not hide the pane: ${JSON.stringify(afterStop)}`)
    }
    if (!confirm || confirm.id !== 'dlg-confirm') {
      fail(3, `Stop did not open the confirmation: ${JSON.stringify(confirm)}`)
    }
    if (!confirm.covers) {
      fail(3, `the confirm dialog was not topmost at its own centre: ${confirm.topmost}`)
    }
    // Escape is the "do not stop anything" path a keyboard user takes.
    await page.keyboard.press('Escape')
    const afterCancel = await waitForMessage(page, 'show', 4000)
    if (!afterCancel.some((m) => m.type === 'show')) {
      fail(3, `dismissing the confirmation did not restore the pane: ${JSON.stringify(afterCancel)}`)
    }

    // Leaving the session view closes the pane; it must not also leave a
    // stale visibility message behind for the next one.
    await page.evaluate(() => { window.__dfMsgs = [] })
    await page.click('#btn-home')
    const afterHome = await waitForMessage(page, 'close', 4000)
    if (!afterHome.some((m) => m.type === 'close')) {
      fail(3, `leaving the session view did not close the pane: ${JSON.stringify(afterHome)}`)
    }

    console.log(JSON.stringify({
      open: true,
      newSessionHideShow: true,
      stopConfirmHideShow: true,
      close: true
    }))
  } finally {
    await browser.close()
  }
}

main().catch((error) => fail(2, error.stack || error.message))
