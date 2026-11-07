// UI probe for the OpenCode panel's chat switch: many chats, real clicks.
//
//   test/browser/probe.sh opencode
//
// It seeds one worktree with many chats through the daemon API, gives two of
// them a user message, then drives the real client: every row in the desktop
// list is clicked, then the mobile picker is driven through the same chats.
// Each switch must change the title, the selected row, the picker value and the
// message pane. A chat with no messages must show the empty state, not the
// previous chat's transcript: an empty message list keys to "", and the client
// used to skip the repaint for that key.
//
// The seed only needs OpenCode to store a user message; the model may fail and
// the marker still renders. That keeps the probe credential-free.
const { chromium } = require('./playwright.cjs')

const BASE = process.env.DFB_BASE
const TOKEN = process.env.DFB_TOKEN
const WID = process.env.DFB_WID
const COUNT = Number(process.env.DFB_CHATS || 16)
const MARKED = new Set([1, 7, 12])

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

function fail(code, message) {
  console.error(`FAIL (${code}): ${message}`)
  process.exit(code)
}

async function api(method, path, body) {
  const res = await fetch(`${BASE}${path}`, {
    method,
    headers: {
      Authorization: `Bearer ${TOKEN}`,
      ...(body ? { 'Content-Type': 'application/json' } : {})
    },
    ...(body ? { body: JSON.stringify(body) } : {})
  })
  const text = await res.text()
  if (!res.ok) throw new Error(`${method} ${path} -> ${res.status} ${text.slice(0, 200)}`)
  return text ? JSON.parse(text) : null
}

async function waitFor(fn, timeout, label) {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    if (await fn()) return
    await sleep(300)
  }
  fail(2, `setup never took effect: ${label}`)
}

function marker(index) {
  return `user-marker-${String(index).padStart(2, '0')}`
}

async function seed() {
  const chats = []
  for (let i = 1; i <= COUNT; i++) {
    const title = `probe-${String(i).padStart(2, '0')}`
    const created = await api('POST', '/api/v1/opencode/sessions', { worktreeId: WID, title })
    const id = created?.data?.id
    if (!id) fail(2, `OpenCode did not create ${title}: ${JSON.stringify(created).slice(0, 160)}`)
    chats.push({ id, title, marker: MARKED.has(i) ? marker(i) : '' })
  }
  for (const chat of chats.filter((c) => c.marker)) {
    await api('POST', `/api/v1/opencode/sessions/${chat.id}/prompt`, { text: chat.marker })
  }
  // A prompt stores the user message even when the model is unavailable, so the
  // pane has content to mis-render; wait for that, not for an assistant turn.
  for (const chat of chats.filter((c) => c.marker)) {
    await waitFor(async () => {
      const page = await api('GET', `/api/v1/opencode/sessions/${chat.id}/messages?limit=20&order=desc`)
      return (page?.data || []).some((m) => m.type === 'user' && m.text === chat.marker)
    }, 20000, `${chat.title} did not store its user message`)
  }
  return chats
}

async function signIn(page, chatMode = true) {
  await page.goto(`${BASE}/?debug=1&panel=0${chatMode ? '&chat=1' : ''}`)
  await page.fill('#token', TOKEN)
  await page.click('#connect-form button[type=submit]')
  await page.waitForSelector('#app:not([hidden])', { timeout: 15000 })
}

async function desktopRows(page) {
  return page.$$eval('#oc-sessions [data-oc-session]', (els) =>
    els.map((el) => ({
      id: el.dataset.ocSession,
      title: el.querySelector('.oc-session-title')?.textContent || ''
    })))
}

async function mobileOptions(page) {
  return page.$$eval('#oc-session-select option', (els) =>
    els.map((el) => ({ id: el.value, title: el.textContent || '' })).filter((o) => o.id))
}

async function panelState(page) {
  return page.evaluate(() => ({
    title: document.querySelector('#oc-chat-title')?.textContent || '',
    on: [...document.querySelectorAll('#oc-sessions .oc-session.on')].map((e) => e.dataset.ocSession),
    select: document.querySelector('#oc-session-select')?.value || '',
    text: (document.querySelector('#oc-messages')?.textContent || '').replace(/\s+/g, ' ')
  }))
}

// expectChat waits for the pane to describe `chat` and nothing else. The
// "nothing else" half is what catches a switch that only moved the highlight.
async function expectChat(page, chat, allMarkers, label) {
  const deadline = Date.now() + 8000
  let last = ''
  while (Date.now() < deadline) {
    const state = await panelState(page)
    last = JSON.stringify({ title: state.title, on: state.on, select: state.select, text: state.text.slice(-90) })
    const titleOK = state.title === chat.title
    const onOK = state.on.length === 1 && state.on[0] === chat.id
    const pickOK = state.select === chat.id
    const mine = chat.marker ? state.text.includes(chat.marker) : state.text.includes('No messages yet')
    const others = [...allMarkers].filter((m) => m !== chat.marker && state.text.includes(m))
    if (titleOK && onOK && pickOK && mine && !others.length) return
    await sleep(150)
  }
  fail(3, `${label}: ${chat.title} did not render alone after the switch: ${last}`)
}

async function probeDesktop(browser, chats, commandsFailures) {
  const page = await (await browser.newContext({ viewport: { width: 1280, height: 900 } })).newPage()
  page.on('pageerror', (error) => console.error('pageerror:', error.message))
  page.on('response', (response) => {
    if (response.url().includes('/api/v1/opencode/commands') && response.status() >= 400) {
      commandsFailures.push(`${response.status()} ${response.url()}`)
    }
  })
  await signIn(page)
  await waitFor(async () => (await desktopRows(page)).length >= COUNT, 15000, 'the panel never listed the seeded chats')

  const rows = await desktopRows(page)
  if (rows.length !== COUNT) fail(2, `the panel listed ${rows.length} chats, not ${COUNT}`)
  const byTitle = new Map(rows.map((row) => [row.title, row]))
  for (const chat of chats) {
    if (!byTitle.has(chat.title)) fail(2, `the panel did not list ${chat.title}; rows: ${rows.map((r) => r.title).join(',')}`)
  }
  const allMarkers = chats.map((c) => c.marker).filter(Boolean)

  // Forward: chat 01 (with a message) is followed by chat 02 (empty). Before
  // the fix this second step left chat 01's transcript in the pane.
  for (const chat of chats) {
    await page.click(`#oc-sessions [data-oc-session="${chat.id}"]`)
    await expectChat(page, chat, allMarkers, 'forward')
  }
  // Reverse crosses the same boundary from the other side.
  for (const chat of [...chats].reverse()) {
    await page.click(`#oc-sessions [data-oc-session="${chat.id}"]`)
    await expectChat(page, chat, allMarkers, 'reverse')
  }
  // Empty after content, twice in a row: the second empty chat must not match
  // the first one's (also empty) render.
  await page.click(`#oc-sessions [data-oc-session="${chats[0].id}"]`)
  await expectChat(page, chats[0], allMarkers, 'content then empty')
  await page.click(`#oc-sessions [data-oc-session="${chats[1].id}"]`)
  await expectChat(page, chats[1], allMarkers, 'content then empty')
  await page.click(`#oc-sessions [data-oc-session="${chats[2].id}"]`)
  await expectChat(page, chats[2], allMarkers, 'empty after empty')

  // Two clicks inside one frame: the later choice must win and must still
  // repaint the pane.
  await page.click(`#oc-sessions [data-oc-session="${chats[0].id}"]`)
  await sleep(60)
  await page.click(`#oc-sessions [data-oc-session="${chats[1].id}"]`)
  await expectChat(page, chats[1], allMarkers, 'rapid switch')

  await page.screenshot({ path: process.env.DFB_SHOT || `${process.env.DFB_ROOT || '/tmp'}/opencode-desktop.png` })
  return page
}

// A landscape phone is about 850x345 CSS px. The normal (not chat-mode) panel
// used to keep the desktop title/search header, which pushed the composer below
// the clipped main area; tapping Send was impossible after rotating.
async function probeLandscape(browser, chats, commandsFailures) {
  const page = await (await browser.newContext({
    viewport: { width: 850, height: 345 },
    deviceScaleFactor: 2.75,
    hasTouch: true,
    isMobile: true
  })).newPage()
  page.on('pageerror', (error) => console.error('pageerror:', error.message))
  page.on('response', (response) => {
    if (response.url().includes('/api/v1/opencode/commands') && response.status() >= 400) {
      commandsFailures.push(`${response.status()} ${response.url()}`)
    }
  })
  await signIn(page, false)
  const toggle = await page.$(`[data-wt-toggle="${WID}"]`)
  if (toggle) await toggle.click()
  await page.click(`[data-wt="${WID}"]`)
  await page.click('#btn-wt-chat')
  await page.waitForSelector('#oc-messages', { timeout: 15000 })
  await waitFor(async () => (await mobileOptions(page)).length >= COUNT, 15000, 'the landscape picker never listed the seeded chats')

  const box = await page.evaluate(() => {
    const rect = (selector) => {
      const el = document.querySelector(selector)
      if (!el) return null
      const r = el.getBoundingClientRect()
      return { top: Math.round(r.top), bottom: Math.round(r.bottom), height: Math.round(r.height), width: Math.round(r.width) }
    }
    return {
      inner: innerHeight,
      messages: rect('#oc-messages'),
      compose: rect('#form-oc-prompt'),
      select: rect('#oc-session-select')
    }
  })
  if (!box.compose || box.compose.height < 36 || box.compose.bottom > box.inner + 1) {
    fail(3, `landscape: the composer is clipped or missing: ${JSON.stringify(box)}`)
  }
  if (!box.messages || box.messages.height < 24) {
    fail(3, `landscape: the message pane has no usable height: ${JSON.stringify(box)}`)
  }
  if (!box.select || box.select.height < 16) {
    fail(3, `landscape: the chat picker is missing: ${JSON.stringify(box)}`)
  }

  // Simulate the shrink Android reports when the landscape keyboard opens:
  // the chrome rows must yield to the composer instead of leaving it below the
  // visual viewport.
  await page.evaluate(() => {
    document.documentElement.dataset.compose = '1'
  })
  await page.setViewportSize({ width: 850, height: 120 })
  await sleep(150)
  const keyboard = await page.evaluate(() => {
    const rect = (selector) => {
      const el = document.querySelector(selector)
      if (!el) return null
      const r = el.getBoundingClientRect()
      return { top: Math.round(r.top), bottom: Math.round(r.bottom), right: Math.round(r.right), width: Math.round(r.width), height: Math.round(r.height) }
    }
    return {
      inner: { w: innerWidth, h: innerHeight },
      app: Math.round(document.querySelector('.app').getBoundingClientRect().height),
      compose: rect('#form-oc-prompt'),
      send: rect('#btn-oc-send')
    }
  })
  const compose = keyboard?.compose
  const send = keyboard?.send
  if (!compose || compose.height < 30 || compose.bottom > keyboard.inner.h + 1) {
    fail(3, `landscape keyboard: the composer is outside the visual viewport: ${JSON.stringify(keyboard)}`)
  }
  if (!send || send.width < 20 || send.right > keyboard.inner.w + 1 || send.bottom > keyboard.inner.h + 1) {
    fail(3, `landscape keyboard: the send button is outside the visual viewport: ${JSON.stringify(keyboard)}`)
  }
  await page.screenshot({ path: process.env.DFB_SHOT_KEYBOARD || `${process.env.DFB_ROOT || '/tmp'}/opencode-landscape-keyboard.png` })
  await page.evaluate(() => {
    delete document.documentElement.dataset.compose
  })
  await page.setViewportSize({ width: 850, height: 345 })
  await sleep(100)

  const allMarkers = chats.map((c) => c.marker).filter(Boolean)
  const chat = chats[1]
  await page.selectOption('#oc-session-select', chat.id)
  await expectChat(page, chat, allMarkers, 'landscape')
  await page.screenshot({ path: process.env.DFB_SHOT_LANDSCAPE || `${process.env.DFB_ROOT || '/tmp'}/opencode-landscape.png` })
}

async function probeMobile(browser, chats, commandsFailures) {
  const page = await (await browser.newContext({
    viewport: { width: 396, height: 719 },
    deviceScaleFactor: 2.72727
  })).newPage()
  page.on('pageerror', (error) => console.error('pageerror:', error.message))
  page.on('response', (response) => {
    if (response.url().includes('/api/v1/opencode/commands') && response.status() >= 400) {
      commandsFailures.push(`${response.status()} ${response.url()}`)
    }
  })
  await signIn(page)
  await waitFor(async () => (await mobileOptions(page)).length >= COUNT, 15000, 'the picker never listed the seeded chats')

  const options = await mobileOptions(page)
  const byTitle = new Map(options.map((option) => [option.title, option]))
  for (const chat of chats) {
    if (!byTitle.has(chat.title)) fail(2, `the picker did not list ${chat.title}`)
  }
  const allMarkers = chats.map((c) => c.marker).filter(Boolean)
  // The phone picker is a native select; a chosen row arrives as `change`.
  for (const index of [1, 0, 6, 11, 0, 1]) {
    const chat = chats[index]
    await page.selectOption('#oc-session-select', chat.id)
    await expectChat(page, chat, allMarkers, 'mobile picker')
  }
  return page
}

;(async () => {
  if (!BASE || !TOKEN || !WID) fail(1, 'run this through test/browser/probe.sh opencode')
  const chats = await seed()

  const browser = await chromium.launch({
    headless: true,
    ...(process.env.DF_CHROMIUM ? { executablePath: process.env.DF_CHROMIUM } : {})
  })
  const commandsFailures = []
  try {
    const desktop = await probeDesktop(browser, chats, commandsFailures)
    const mobile = await probeMobile(browser, chats, commandsFailures)
    await probeLandscape(browser, chats, commandsFailures)
    // One screenshot per layout, for the artifact trail.
    await mobile.screenshot({ path: process.env.DFB_SHOT_MOBILE || `${process.env.DFB_ROOT || '/tmp'}/opencode-mobile.png` })
    if (commandsFailures.length) {
      fail(3, `the command list was rejected by the daemon: ${commandsFailures[0]}`)
    }
    console.log(`PASS opencode: ${chats.length} chats switched by click, picker, and landscape; empty chats clear the pane; composer stays visible; commands 200`)
  } finally {
    await browser.close()
  }
})().catch((error) => {
  console.error(error)
  process.exit(1)
})
