// The sidebar's workspace and session menu (UI-31/UI-32).
//
//   test/browser/probe.sh sidebar
//
// Asserts what the left bar promises: a session can be pinned to the top of its
// workspace, pinned and unpinned state survives a reload, a session grip moves a
// row one place with the arrow keys, and the menu copies the workspace path and
// offers the file-manager reveal. The reveal itself is never clicked: it would
// open a window on the machine running the probe.
//
// Exit codes match the other probes: 1 precondition, 2 setup, 3 regression.
const { chromium } = require('./playwright.cjs')

const BASE = process.env.DFB_BASE
const TOKEN = process.env.DFB_TOKEN
const WID = process.env.DFB_WID
const SID1 = process.env.DFB_SID1
const SID2 = process.env.DFB_SID2

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

function fail(code, message) {
  console.error(`FAIL (${code}): ${message}`)
  process.exit(code)
}

async function state() {
  const response = await fetch(`${BASE}/api/v1/state`, {
    headers: { Authorization: `Bearer ${TOKEN}` }
  })
  if (!response.ok) throw new Error(`state ${response.status}`)
  return response.json()
}

async function sessionsInWorktree() {
  const body = await state()
  return (body.sessions || []).filter((sess) => sess.worktreeId === WID)
}

async function rowOrder(page) {
  return page.$$eval('.tree-sessions [data-tree-session]', (nodes) => nodes.map((node) => node.dataset.treeSession))
}

async function openMenu(page, sessionId) {
  await page.click(`[data-tree-session="${sessionId}"]`, { button: 'right' })
  await page.waitForSelector('#ctx-menu:not([hidden])', { timeout: 5000 })
}

async function choose(page, action) {
  await page.click(`#ctx-menu [data-ctx="${action}"]`)
  await page.waitForSelector('#ctx-menu', { state: 'hidden', timeout: 5000 })
}

async function main() {
  const browser = await chromium.launch({ args: ['--no-sandbox'] })
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } })
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: BASE })
  const page = await context.newPage()
  try {
    await page.goto(`${BASE}/?debug=1&panel=0`)
    await page.fill('#token', TOKEN)
    await page.click('#connect-form button[type=submit]')
    await page.waitForSelector('#app:not([hidden])', { timeout: 15000 })
    await sleep(600)

    const toggle = `[data-wt-toggle="${WID}"]`
    if (await page.$(toggle)) {
      const expanded = await page.getAttribute(toggle, 'aria-expanded')
      if (expanded !== 'true') await page.click(toggle)
    }
    await page.waitForSelector(`[data-tree-session="${SID1}"]`, { timeout: 15000 })

    const before = await sessionsInWorktree()
    if (before.length < 2) fail(1, `the fixture seeded ${before.length} sessions; the probe needs two`)

    // The menu carries the new actions.
    await openMenu(page, SID1)
    const labels = await page.$$eval('#ctx-menu [data-ctx]', (nodes) => nodes.map((node) => node.textContent))
    for (const want of ['Pin session', 'Copy path', 'Reveal in file manager']) {
      if (!labels.includes(want)) fail(3, `the session menu has no ${JSON.stringify(want)}: ${JSON.stringify(labels)}`)
    }

    // Copy path copies the workspace directory, not the session id.
    await choose(page, 'copy-path')
    await sleep(300)
    const copied = await page.evaluate(() => navigator.clipboard.readText())
    const expected = (await state()).worktrees.find((wt) => wt.id === WID)?.path || ''
    if (!expected || copied !== expected) fail(3, `Copy path copied ${JSON.stringify(copied)}, want ${JSON.stringify(expected)}`)

    // Pin floats the session to the top of its workspace and survives a poll.
    await openMenu(page, SID2)
    await choose(page, 'toggle-pin')
    try {
      await page.waitForFunction(({ wid, sid }) => {
        const rows = [...document.querySelectorAll('.tree-sessions [data-tree-session]')]
          .filter((node) => node.dataset.treeSession)
        const row = rows.find((node) => node.dataset.treeSession === sid)
        if (!row) return false
        const li = row.closest('li')
        const list = row.closest('.tree-sessions')
        return li === list.firstElementChild && Boolean(row.querySelector('.tree-session-pin'))
      }, { wid: WID, sid: SID2 }, { timeout: 10000 })
    } catch {
      fail(3, 'pinning did not move the session to the top with a pin marker')
    }
    const pinned = (await sessionsInWorktree()).find((sess) => sess.id === SID2)
    if (!pinned?.pinned) fail(3, 'the pin did not reach the daemon')
    await page.screenshot({ path: `${process.env.DFB_ROOT}/sidebar-pinned.png` })

    // A reload reads the pin back from the server rather than from the page.
    await page.reload()
    await page.waitForSelector('#app:not([hidden])', { timeout: 15000 })
    if (await page.$(toggle)) {
      const expanded = await page.getAttribute(toggle, 'aria-expanded')
      if (expanded !== 'true') await page.click(toggle)
    }
    await page.waitForSelector(`[data-tree-session="${SID2}"]`, { timeout: 15000 })
    if (!(await page.locator(`[data-tree-session="${SID2}"] .tree-session-pin`).count())) {
      fail(3, 'the pin marker did not survive a reload')
    }

    // The grip is a button: ArrowDown moves the row one place inside its group.
    const orderBefore = await rowOrder(page)
    const moving = orderBefore.find((id) => id !== SID2)
    const grip = `[data-session-grip="${moving}"]`
    if (!(await page.$(grip))) fail(3, 'a session row has no reorder grip')
    const atBefore = orderBefore.indexOf(moving)
    if (atBefore + 1 >= orderBefore.length) fail(1, 'the fixture left no row below the one to move')
    await page.focus(grip)
    await page.keyboard.press('ArrowDown')
    await sleep(700)
    const orderAfter = await rowOrder(page)
    if (orderAfter.indexOf(moving) !== atBefore + 1) {
      fail(3, `ArrowDown did not move the session: ${JSON.stringify(orderBefore)} -> ${JSON.stringify(orderAfter)}`)
    }
    const stored = (await sessionsInWorktree()).map((sess) => sess.id)
    if (stored.indexOf(moving) !== atBefore + 1) {
      fail(3, `the daemon kept ${JSON.stringify(stored)} after the keyboard move`)
    }

    // Unpin puts the row back among the others and clears the marker.
    await openMenu(page, SID2)
    await choose(page, 'toggle-pin')
    await sleep(700)
    if (await page.locator(`[data-tree-session="${SID2}"] .tree-session-pin`).count()) {
      fail(3, 'unpinning left the pin marker on the row')
    }
    if ((await sessionsInWorktree()).find((sess) => sess.id === SID2)?.pinned) {
      fail(3, 'unpinning did not reach the daemon')
    }

    console.log(JSON.stringify({ menu: true, copyPath: true, pinSurvivesReload: true, keyboardReorder: true }))
  } finally {
    await browser.close()
  }
}

main().catch((error) => fail(2, error.stack || error.message))
