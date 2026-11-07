// UI gate: the served client at desktop and phone widths.
//
// This is the layout contract the browser and the Wails shell share. It does
// not diff pixels (fonts and GPUs differ); it asserts the properties that kept
// breaking: which chrome is visible, that the terminal owns the remaining
// height, that nothing overflows the viewport, that touch targets stay usable,
// and that the bytes on the wire are the bytes in web/.
//
//   test/browser/probe.sh gate
//
// Exit codes match the probes: 1 precondition, 2 setup, 3 regression.
const crypto = require('crypto')
const fs = require('fs')
const path = require('path')

const REPO = path.resolve(__dirname, '..', '..')

const { chromium } = require('./playwright.cjs')

const BASE = process.env.DFB_BASE
const TOKEN = process.env.DFB_TOKEN
const WID = process.env.DFB_WID
const SID = process.env.DFB_SID1
const ROOT = process.env.DFB_ROOT || '/tmp'

const VIEWPORTS = [
  { name: 'desktop', width: 1280, height: 900, scale: 1, phone: false },
  // The Wails shell used to open at exactly the CSS phone breakpoint.
  { name: 'narrow', width: 1200, height: 800, scale: 1, phone: false },
  { name: 'phone', width: 396, height: 719, scale: 2.72727, phone: true }
]

const SELECTORS = [
  '.topbar',
  '.sidebar',
  '.main',
  '#view-session',
  '#view-session .view-head',
  '#session-tabs',
  '#terminal-pane',
  '.xterm-screen',
  '#form-term-input',
  '#mobile-nav',
  'footer.statusbar',
  '#conn-pill',
  '#btn-new-session',
  '#btn-stop-session',
  '#session-tabs [data-session-add]'
]

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

function fail(code, message) {
  console.error(`FAIL (${code}): ${message}`)
  process.exit(code)
}

function sha256(buffer) {
  return crypto.createHash('sha256').update(buffer).digest('hex')
}

async function apiJSON(pathname, options = {}) {
  const res = await fetch(`${BASE}${pathname}`, {
    ...options,
    headers: {
      Authorization: `Bearer ${TOKEN}`,
      'Content-Type': 'application/json',
      'X-DF-Request': '1',
      ...(options.headers || {})
    }
  })
  const text = await res.text()
  let body = null
  try {
    body = JSON.parse(text)
  } catch {}
  return { status: res.status, body }
}

// The probe repo has one workspace. A fleet of one-workspace repos is the
// layout that had no movable row, so seed two more repos and two extra
// worktrees in the probe repo. This runs once per gate, before any viewport.
async function seedOrderFixtures() {
  for (const name of ['gate-order-a', 'gate-order-b']) {
    const dir = path.join(ROOT, name)
    fs.mkdirSync(dir, { recursive: true })
    const res = await apiJSON('/api/v1/repos', {
      method: 'POST',
      body: JSON.stringify({ path: dir })
    })
    if (res.status !== 201 && res.status !== 409) {
      fail(2, `could not seed repo ${name}: ${res.status} ${JSON.stringify(res.body)}`)
    }
  }
  const state = await apiJSON('/api/v1/state')
  const main = (state.body?.worktrees || []).find((row) => row.id === WID)
  if (!main) fail(2, 'could not find the probe worktree in /state')
  for (const name of ['gate-order-one', 'gate-order-two']) {
    const res = await apiJSON('/api/v1/worktrees', {
      method: 'POST',
      body: JSON.stringify({ repoId: main.repoId, name })
    })
    if (res.status !== 201 && res.status !== 409) {
      fail(2, `could not seed worktree ${name}: ${res.status} ${JSON.stringify(res.body)}`)
    }
  }
}

async function center(page, selector) {
  const box = await page.locator(selector).boundingBox()
  if (!box) fail(3, `reorder: ${selector} has no box`)
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 }
}

// The tree auto-scrolls when a drag starts within 28px of its bottom, which
// moves the row under the pointer. Center the row first so the drop coordinate
// is stable; a user can still auto-scroll for a long fleet.
async function centerWorktree(page, id) {
  await page.evaluate((wt) => {
    const grip = document.querySelector(`[data-wt-grip="${wt}"]`)
    const tree = document.getElementById('tree')
    if (!grip || !tree) return
    const row = grip.closest('li.wt-item')?.getBoundingClientRect()
    const box = tree.getBoundingClientRect()
    if (row) tree.scrollTop += row.top + row.height / 2 - (box.top + box.height / 2)
  }, id)
  await sleep(200)
  return center(page, `[data-wt-grip="${id}"]`)
}

async function dragMouse(page, from, to) {
  await page.mouse.move(from.x, from.y)
  await page.mouse.down()
  await page.mouse.move(from.x, from.y - 10, { steps: 3 })
  await page.mouse.move(to.x, to.y - 8, { steps: 12 })
  await sleep(150)
  await page.mouse.up()
  await sleep(1100)
}

async function dragTouch(context, page, from, to) {
  const client = await context.newCDPSession(page)
  const points = (x, y) => [{ x, y, id: 1, radiusX: 6, radiusY: 6, force: 1 }]
  await client.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: points(from.x, from.y) })
  await sleep(80)
  for (let i = 1; i <= 12; i++) {
    const y = from.y + ((to.y - 8) - from.y) * (i / 12)
    await client.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: points(from.x, y) })
    await sleep(25)
  }
  await sleep(120)
  await client.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
  await sleep(1100)
}

async function drag(page, context, touch, from, to) {
  if (touch) await dragTouch(context, page, from, to)
  else await dragMouse(page, from, to)
}

function failOrder(label, detail) {
  fail(3, `reorder (${label}): ${detail}`)
}

async function showFleet(page) {
  await page.click('#btn-home')
  await page.waitForSelector('.sidebar', { state: 'visible', timeout: 5000 })
  await sleep(700)
}

async function fleetRows(page) {
  return page.evaluate(() =>
    Array.from(document.querySelectorAll('.repo')).map((repo) => ({
      id: repo.dataset.repo,
      name: repo.querySelector('.repo-name')?.textContent || '',
      worktrees: Array.from(repo.querySelectorAll('[data-wt]')).map((row) => row.dataset.wt)
    }))
  )
}

async function checkReorder(page, context, viewport) {
  const touch = viewport.phone
  const where = viewport.name
  await showFleet(page)

  const grips = await page.evaluate(() =>
    Array.from(document.querySelectorAll('.repo-grip, .wt-grip')).map((el) => {
      const rect = el.getBoundingClientRect()
      return { cls: el.className, w: Math.round(rect.width), h: Math.round(rect.height) }
    })
  )
  if (!grips.length) failOrder(where, 'no drag grips rendered')
  for (const grip of grips) {
    if (grip.w < 16 || grip.h < 16) {
      failOrder(where, `${grip.cls} is ${grip.w}x${grip.h}, below the 16px touch target`)
    }
  }

  let rows = await fleetRows(page)
  if (rows.length < 3) failOrder(where, `expected at least 3 repos, got ${rows.length}`)
  if (rows.some((row) => !row.worktrees.length)) failOrder(where, 'a repo has no workspace row')
  if ((await page.locator('.agent-state.live').count()) < 1) {
    failOrder(where, 'no static running glyph for the live probe session')
  }
  const firstID = rows[0].id
  const lastID = rows[rows.length - 1].id
  // The reload below aborts an in-flight mutation, so wait for the order POST
  // instead of trusting drag()'s fixed settle time.
  const orderPosted = page.waitForResponse(
    (res) => res.url().includes('/api/v1/repos/order') && res.request().method() === 'POST',
    { timeout: 8000 }
  )
  await drag(
    page,
    context,
    touch,
    await center(page, `[data-repo-grip="${lastID}"]`),
    await center(page, `[data-repo-grip="${firstID}"]`)
  )
  await orderPosted.catch(() => failOrder(where, 'the repo drag sent no order POST'))
  rows = await fleetRows(page)
  if (rows[0].id !== lastID) failOrder(where, `repo drag did not move ${lastID} to the top`)

  // The order is daemon state, not a DOM-only shuffle.
  await page.reload()
  await page.waitForSelector('#app:not([hidden])', { timeout: 15000 })
  await sleep(900)
  if (viewport.phone) await showFleet(page)
  rows = await fleetRows(page)
  if (rows[0].id !== lastID) failOrder(where, 'repo order did not survive a reload')

  if (!touch) {
    const before = rows.map((row) => row.id)
    const gripID = before[0]
    await page.locator(`[data-repo-grip="${gripID}"]`).focus()
    await page.keyboard.press('ArrowDown')
    await sleep(900)
    rows = await fleetRows(page)
    if (rows[1].id !== gripID) failOrder(where, 'ArrowDown did not move the focused repo')
  }

  // A repo with a single workspace has no sibling row to swap with; its row
  // grip must move the repo group, which is the common one-workspace layout.
  rows = await fleetRows(page)
  const lone = rows.find((row) => row.worktrees.length === 1 && row.id !== rows[0].id)
  if (lone) {
    await drag(
      page,
      context,
      touch,
      await center(page, `[data-wt-grip="${lone.worktrees[0]}"]`),
      await center(page, `[data-repo-grip="${rows[0].id}"]`)
    )
    rows = await fleetRows(page)
    if (rows[0].id !== lone.id) failOrder(where, 'a lone workspace grip did not move its repo')
  }

  // A repo with siblings keeps the per-repo row order.
  rows = await fleetRows(page)
  const multi = rows.find((row) => row.worktrees.length > 1)
  if (multi) {
    const before = multi.worktrees.slice()
    const lastWT = before[before.length - 1]
    // centerWorktree scrolls; calling it for both ends invalidates the first
    // box. Center the dragged row once, then read the target without moving
    // the tree again.
    const from = await centerWorktree(page, lastWT)
    const to = await center(page, `[data-wt-grip="${before[0]}"]`)
    const orderPosted = page.waitForResponse(
      (res) => res.url().includes('/api/v1/worktrees/order') && res.request().method() === 'POST',
      { timeout: 8000 }
    )
    await drag(page, context, touch, from, to)
    await orderPosted.catch(() => failOrder(where, 'the workspace drag sent no order POST'))
    const after = (await fleetRows(page)).find((row) => row.id === multi.id)
    if (!after || after.worktrees[0] !== lastWT) {
      failOrder(where, 'a workspace row did not move inside its repo')
    }
  }
  await page.screenshot({ path: path.join(ROOT, `ui-gate-${where}-reorder.png`) })
  await checkAttention(page, viewport)
}

// A session that exits while the page is open must raise an unread bell, not
// only a desktop notification. The bell opens an activity list that names the
// session it came from; opening an event does not clear it, because the owner
// clears deliberately. The phone Fleet tab carries the count.
async function checkAttention(page, viewport) {
  const where = viewport.name
  await showFleet(page)
  if (!(await page.locator('#btn-attention').isVisible())) {
    fail(3, `attention (${where}): the bell is not always visible`)
  }
  // The daemon keeps a ring and a fresh page asks for it, so an earlier
  // viewport's activity can already be here. Clear it before asserting the
  // empty state.
  if (await page.locator('#attention-count').isVisible()) {
    await page.click('#btn-attention')
    await page.waitForSelector('#attention-pop:not([hidden])', { timeout: 5000 })
    await page.click('#attention-clear')
    await page.waitForTimeout(250)
  }
  // The bell is a list, not a dead icon: with nothing unread it still opens and
  // says so, which is what "the bell is not clickable" was about.
  if (await page.locator('#btn-attention').isDisabled()) {
    fail(3, `attention (${where}): the bell is disabled while there is no unread activity`)
  }
  await page.click('#btn-attention')
  try {
    await page.waitForSelector('#attention-pop:not([hidden])', { timeout: 5000 })
  } catch {
    fail(3, `attention (${where}): the bell does not open the list when nothing is unread`)
  }
  if (!(await page.locator('#attention-list .attention-empty').count())) {
    fail(3, `attention (${where}): the empty list has no explanation`)
  }
  await page.keyboard.press('Escape')
  if (await page.locator('#attention-pop').isVisible()) {
    fail(3, `attention (${where}): Escape does not close the empty list`)
  }
  const created = await apiJSON('/api/v1/sessions', {
    method: 'POST',
    headers: { 'Idempotency-Key': `gate-attention-${where}-${Date.now()}` },
    body: JSON.stringify({
      worktreeId: WID,
      kind: 'shell',
      title: `gate attention ${where}`,
      cols: 40,
      rows: 10
    })
  })
  if (created.status !== 201 || !created.body?.id || !created.body?.pid) {
    fail(3, `attention (${where}): session create returned ${created.status} ${JSON.stringify(created.body)}`)
  }
  await sleep(600)
  try {
    process.kill(created.body.pid, 'SIGTERM')
  } catch (error) {
    if (error.code !== 'ESRCH') fail(3, `attention (${where}): kill failed: ${error.message}`)
  }
  try {
    await page.waitForSelector('#attention-count:not([hidden])', { timeout: 25000 })
  } catch {
    fail(3, `attention (${where}): the exited session never raised an unread bell`)
  }
  const count = (await page.locator('#attention-count').textContent())?.trim() || ''
  if (!count || count === '0') fail(3, `attention (${where}): bell count is ${JSON.stringify(count)}`)
  if (!(await page.locator('.agent-state.unread').count())) {
    fail(3, `attention (${where}): no unread session glyph on the fleet or tabs`)
  }
  if (viewport.phone && !(await page.locator('#nav-attention').isVisible())) {
    fail(3, `attention (${where}): the Fleet tab badge is not visible`)
  }
  await page.screenshot({ path: path.join(ROOT, `ui-gate-${where}-attention.png`) })

  // The bell opens the list. The newest row must name the session it came
  // from, which is what the old jump-to-newest click never showed.
  await page.click('#btn-attention')
  try {
    await page.waitForSelector('#attention-pop:not([hidden])', { timeout: 5000 })
  } catch {
    fail(3, `attention (${where}): clicking the bell did not open the activity list`)
  }
  // A probe fixture can exit while the viewport runs and land a newer row.
  // Find this session's row instead of assuming it is first; the assertion is
  // still that a row names the session it came from.
  const wanted = `gate attention ${where}`
  const row = page.locator('#attention-list .attention-row', { hasText: wanted }).first()
  try {
    await row.waitFor({ state: 'visible', timeout: 5000 })
  } catch {
    fail(3, `attention (${where}): no activity row names ${JSON.stringify(wanted)}`)
  }
  const rowText = (await row.locator('.attention-go').textContent()) || ''
  if (!rowText.includes(wanted)) {
    fail(3, `attention (${where}): the activity row does not name its session: ${JSON.stringify(rowText.slice(0, 120))}`)
  }
  const source = (await row.locator('.attention-row-source').textContent()) || ''
  if (!source.includes(wanted)) {
    fail(3, `attention (${where}): the activity row does not name its source session: ${JSON.stringify(source)}`)
  }
  await page.screenshot({ path: path.join(ROOT, `ui-gate-${where}-attention-list.png`) })

  // Following an event opens it and leaves it unread: the owner clears.
  await row.locator('.attention-go').click()
  await page.waitForSelector('#app[data-view="session"]', { timeout: 15000 })
  await page.waitForTimeout(400)
  if (!(await page.locator('#attention-count').isVisible())) {
    fail(3, `attention (${where}): opening a session silently cleared the bell`)
  }

  // Dismissing is the only way it goes away.
  for (let guard = 0; guard < 60; guard++) {
    if (!(await page.locator('#attention-count').isVisible())) break
    await page.click('#btn-attention')
    await page.waitForSelector('#attention-pop:not([hidden])', { timeout: 5000 })
    const dismiss = page.locator('#attention-list .attention-dismiss').first()
    if (!(await dismiss.count())) {
      fail(3, `attention (${where}): the activity list has no dismiss control`)
    }
    await dismiss.click()
    await page.waitForTimeout(120)
  }
  if (await page.locator('#attention-count').isVisible()) {
    fail(3, `attention (${where}): dismissing every row did not clear the bell`)
  }
  if (!(await page.locator('.agent-state.done').count())) {
    fail(3, `attention (${where}): the opened exited session has no finished glyph`)
  }

  // The alert policy is reachable from the always-visible topbar control, not
  // only from inside the bell. The defaults must load into the checkboxes:
  // a field-name typo once left every box unchecked and a save would have
  // silently muted every alert.
  if (!(await page.locator('#btn-alert-settings-top').isVisible())) {
    fail(3, `attention (${where}): the topbar alert settings button is missing`)
  }
  if (!(await page.locator('#btn-term-refresh').isVisible())) {
    fail(3, `attention (${where}): the terminal refresh control is missing`)
  }
  await page.click('#btn-alert-settings-top')
  try {
    await page.waitForSelector('#dlg-alerts[open]', { timeout: 5000 })
    await page.waitForFunction(() => {
      const box = document.getElementById('alert-prompts')
      return box instanceof HTMLInputElement && box.checked
    }, { timeout: 5000 })
  } catch {
    fail(3, `attention (${where}): alert settings did not load into the dialog`)
  }
  const alertDefaults = await page.evaluate(() => ({
    finished: document.getElementById('alert-finished').checked,
    prompts: document.getElementById('alert-prompts').checked,
    blocked: document.getElementById('alert-blocked').checked,
    replies: document.getElementById('alert-replies').checked,
    children: document.getElementById('alert-children').checked
  }))
  if (!alertDefaults.finished || !alertDefaults.prompts || !alertDefaults.blocked ||
      alertDefaults.replies || alertDefaults.children) {
    fail(3, `attention (${where}): alert defaults are wrong: ${JSON.stringify(alertDefaults)}`)
  }
  await page.click('#dlg-alerts [data-close]')
  await page.waitForTimeout(200)
  if (await page.locator('#dlg-alerts').evaluate((el) => el.open)) {
    fail(3, `attention (${where}): the alert settings dialog did not close`)
  }
}

async function signIn(page) {
  await page.goto(`${BASE}/?debug=1&panel=0`)
  await page.fill('#token', TOKEN)
  await page.click('#connect-form button[type=submit]')
  await page.waitForSelector('#app:not([hidden])', { timeout: 15000 })
  await sleep(700)
  if (await page.$eval('#connect', (el) => !el.hidden)) {
    fail(3, 'a valid token never left the connect card')
  }
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

async function snapshot(page) {
  return page.evaluate((selectors) => {
    const rect = (el) => {
      const r = el.getBoundingClientRect()
      const cs = getComputedStyle(el)
      return {
        exists: true,
        x: Math.round(r.x),
        y: Math.round(r.y),
        w: Math.round(r.width),
        h: Math.round(r.height),
        display: cs.display,
        visibility: cs.visibility,
        opacity: cs.opacity
      }
    }
    const measure = (sel) => {
      const el = document.querySelector(sel)
      return el ? rect(el) : { exists: false }
    }
    const shown = (sel) => {
      const m = measure(sel)
      return m.exists && m.display !== 'none' && m.visibility !== 'hidden' && m.h > 0 && m.w > 0
    }
    const out = {
      inner: { w: innerWidth, h: innerHeight },
      dpr: devicePixelRatio,
      scroll: {
        w: document.documentElement.scrollWidth,
        h: document.documentElement.scrollHeight
      },
      bodyScroll: {
        w: document.body.scrollWidth,
        h: document.body.scrollHeight
      },
      uiVersion: document.getElementById('ui-version')?.textContent || '',
      bg: getComputedStyle(document.body).backgroundColor,
      tokenBg: getComputedStyle(document.documentElement).getPropertyValue('--bg').trim(),
      elements: {},
      tapTargets: []
    }
    for (const sel of selectors) out.elements[sel] = measure(sel)
    out.shown = Object.fromEntries(selectors.map((sel) => [sel, shown(sel)]))

    const controls = document.querySelectorAll('button, input, select, textarea')
    for (const el of controls) {
      if (el.closest('[hidden]')) continue
      // xterm owns a 1px helper textarea and its own screen surface; they are
      // not app controls and their size is not a touch target.
      if (el.closest('.xterm')) continue
      const r = el.getBoundingClientRect()
      if (r.width <= 0 || r.height <= 0) continue
      if (getComputedStyle(el).visibility === 'hidden') continue
      out.tapTargets.push({
        tag: el.tagName.toLowerCase(),
        id: el.id || '',
        cls: el.className || '',
        w: Math.round(r.width),
        h: Math.round(r.height)
      })
    }
    return out
  }, SELECTORS)
}

function checkCommon(label, snap) {
  const problems = []
  if (snap.scroll.w > snap.inner.w + 1) {
    problems.push(`page scrolls horizontally: ${snap.scroll.w} > ${snap.inner.w}`)
  }
  if (snap.bodyScroll.w > snap.inner.w + 1) {
    problems.push(`body scrolls horizontally: ${snap.bodyScroll.w} > ${snap.inner.w}`)
  }
  if (!snap.shown['#terminal-pane']) problems.push('the terminal pane is not visible')
  if (!snap.shown['.xterm-screen']) problems.push('the xterm screen is not visible')
  if (!snap.shown['#session-tabs']) problems.push('the session tab strip is not visible')
  if (snap.elements['.xterm-screen'].h < 80) {
    problems.push(`the xterm screen is too short: ${snap.elements['.xterm-screen'].h}px`)
  }
  if (!snap.uiVersion.startsWith('ui ')) {
    problems.push(`the footer does not name the client: ${JSON.stringify(snap.uiVersion)}`)
  }
  if (problems.length) fail(3, `${label}: ${problems.join('; ')}`)
}

function checkDesktop(snap) {
  const problems = []
  if (!snap.shown['.sidebar']) problems.push('the fleet sidebar is hidden at desktop width')
  if (snap.shown['#mobile-nav']) problems.push('the phone nav is visible at desktop width')
  if (!snap.shown['footer.statusbar']) problems.push('the status bar is hidden at desktop width')
  // The live-session title row carries the Chat and Stop actions. A wide
  // window that falls into the phone layout loses both.
  if (!snap.shown['#view-session .view-head']) {
    problems.push('the session view head is hidden at desktop width')
  }
  const term = snap.elements['#terminal-pane']
  const status = snap.elements['footer.statusbar']
  if (term.h < snap.inner.h * 0.45) {
    problems.push(`the terminal keeps only ${term.h}px of ${snap.inner.h}px`)
  }
  if (term.y + term.h > snap.inner.h - status.h + 2) {
    problems.push(`the terminal runs under the status bar: bottom ${term.y + term.h}`)
  }
  if (problems.length) fail(3, `desktop: ${problems.join('; ')}`)
}

function checkPhone(snap) {
  const problems = []
  if (snap.shown['.sidebar']) problems.push('the fleet sidebar covers the session at phone width')
  if (!snap.shown['#mobile-nav']) problems.push('the phone nav is hidden')
  if (snap.shown['footer.statusbar']) problems.push('the desktop status bar is visible on the phone')
  const term = snap.elements['#terminal-pane']
  if (term.h < snap.inner.h * 0.40) {
    problems.push(`the terminal keeps only ${term.h}px of ${snap.inner.h}px`)
  }
  if (term.y + term.h > snap.inner.h + 1) {
    problems.push(`the terminal bottom (${term.y + term.h}) leaves the viewport (${snap.inner.h})`)
  }
  const add = snap.elements['#session-tabs [data-session-add]']
  if (!add.exists || add.w === 0) {
    problems.push('the session-tab add control is not rendered')
  } else if (add.x < 0 || add.x + add.w > snap.inner.w + 0.5) {
    problems.push(`the session-tab add control is off screen: x=${add.x} w=${add.w}`)
  }
  for (const target of snap.tapTargets) {
    if (target.h < 16 || target.w < 16) {
      problems.push(`tap target ${target.tag}#${target.id || target.cls} is ${target.w}x${target.h}`)
    }
  }
  if (problems.length) fail(3, `phone: ${problems.join('; ')}`)
}

async function gateViewport(browser, viewport, sourceVersion, sourceHash) {
  const context = await browser.newContext({
    viewport: { width: viewport.width, height: viewport.height },
    deviceScaleFactor: viewport.scale,
    hasTouch: viewport.phone,
    isMobile: viewport.phone
  })
  const page = await context.newPage()
  page.on('pageerror', (error) => console.error('pageerror:', error.message))
  try {
    await signIn(page)
    await openSession(page)
    const snap = await snapshot(page)
    snap.viewport = viewport.name
    checkCommon(viewport.name, snap)
    if (viewport.phone) checkPhone(snap)
    else checkDesktop(snap)
    // The Ghostty action is capability-gated by the host, not by the viewport
    // alone: a CI runner with no graphical session and no Ghostty 1.3.1 must
    // hide it, while a developer host that can run it shows it on desktop.
    const nativeState = await page.evaluate(async () => {
      const token = sessionStorage.getItem('darkfactory-token') || ''
      const res = await fetch('/api/v1/state', { cache: 'no-store', headers: token ? { Authorization: `Bearer ${token}` } : {} })
      const body = await res.json().catch(() => ({}))
      return body.nativeTerminal || {}
    })
    const nativeButton = page.locator('#btn-term-native')
    const wantNative = !viewport.phone && nativeState.available === true
    if (wantNative) {
      try {
        await nativeButton.waitFor({ state: 'visible', timeout: 8000 })
      } catch {}
    }
    if (await nativeButton.isVisible() !== wantNative) {
      fail(3, `${viewport.name}: Ghostty action visibility is incorrect for ${JSON.stringify(nativeState)}`)
    }
    // The state poll and the session stream race the click that opened this
    // view, so the action can be disabled for one poll interval. Wait for the
    // contract (a live local session can open Ghostty) instead of sampling it.
    if (wantNative) {
      try {
        await page.waitForSelector('#btn-term-native:not([disabled])', { timeout: 8000 })
      } catch {}
    }
    if (wantNative && !(await nativeButton.isEnabled())) {
      const diag = await page.evaluate(async (sid) => {
        const token = sessionStorage.getItem('darkfactory-token') || ''
        const res = await fetch('/api/v1/state', { cache: 'no-store', headers: { Authorization: `Bearer ${token}` } })
        const body = await res.json().catch(() => ({}))
        const row = (body.sessions || []).find((item) => item.id === sid) || null
        return {
          conn: document.querySelector('#conn-text')?.textContent,
          status: res.status,
          session: row ? { live: row.live, status: row.status } : null
        }
      }, SID)
      fail(3, `${viewport.name}: Ghostty action is disabled on a live local session ${JSON.stringify(diag)}`)
    }

    const state = await page.evaluate(async () => {
      const token = sessionStorage.getItem('darkfactory-token') || ''
      const res = await fetch('/api/v1/state', {
        cache: 'no-store',
        headers: token ? { Authorization: `Bearer ${token}` } : {}
      })
      return { status: res.status, body: await res.json() }
    })
    if (state.status !== 200) fail(3, `${viewport.name}: /api/v1/state returned ${state.status}`)
    const build = state.body.build || ''
    if (!build) fail(3, `${viewport.name}: the daemon does not report a build`)
    if (!snap.uiVersion.includes(sourceVersion)) {
      fail(3, `${viewport.name}: footer ${JSON.stringify(snap.uiVersion)} does not name ${sourceVersion}`)
    }
    if (!snap.uiVersion.includes(build)) {
      fail(3, `${viewport.name}: footer ${JSON.stringify(snap.uiVersion)} does not name the daemon build ${build}`)
    }

    const served = await page.evaluate(async () => {
      const res = await fetch('/app.js', { cache: 'no-store' })
      return { status: res.status, cache: res.headers.get('cache-control'), text: await res.text() }
    })
    if (served.status !== 200) fail(3, `${viewport.name}: /app.js returned ${served.status}`)
    if (served.cache !== 'no-store') {
      fail(3, `${viewport.name}: /app.js Cache-Control is ${JSON.stringify(served.cache)}`)
    }
    if (sha256(Buffer.from(served.text, 'utf8')) !== sourceHash) {
      fail(3, `${viewport.name}: the served app.js is not web/app.js (stale daemon or browser cache)`)
    }

    await page.screenshot({ path: path.join(ROOT, `ui-gate-${viewport.name}.png`) })
    await checkReorder(page, context, viewport)
    snap.build = build
    return snap
  } finally {
    await context.close()
  }
}

;(async () => {
  if (!BASE || !TOKEN || !WID || !SID) {
    fail(1, 'run through test/browser/probe.sh gate (DFB_BASE/DFB_TOKEN/DFB_WID/DFB_SID1)')
  }
  const js = fs.readFileSync(path.join(REPO, 'web', 'app.js'))
  const sourceHash = sha256(js)
  const match = /const UI_VERSION = '([^']+)'/.exec(js.toString('utf8'))
  if (!match) fail(1, 'web/app.js has no UI_VERSION')
  const sourceVersion = match[1]
  await seedOrderFixtures()

  const browser = await chromium.launch({
    headless: true,
    ...(process.env.DF_CHROMIUM ? { executablePath: process.env.DF_CHROMIUM } : {})
  })
  try {
    const reports = []
    for (const viewport of VIEWPORTS) {
      reports.push(await gateViewport(browser, viewport, sourceVersion, sourceHash))
    }
    console.log(JSON.stringify(reports, null, 2))
    console.log(
      `PASS ui-gate: desktop 1280x900, narrow 1200x800, phone 396x719; ` +
        `ui ${sourceVersion}; daemon ${reports[0].build}; ` +
        `served app.js matches web/app.js`
    )
  } finally {
    await browser.close()
  }
})().catch((error) => {
  console.error(error)
  process.exit(1)
})
