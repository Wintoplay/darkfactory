// Keyboard-to-visible-echo, split into segments, against a real daemon and a
// real PTY.
//
//   test/browser/probe.sh echo
//
// This is measurement, not a gate: it prints numbers and does not fail on a
// threshold, because the value depends on the host's vsync and engine. Compare
// two runs on the same host, not two hosts.
//
// It runs at a fractional device scale, which keeps xterm's DOM renderer. That
// is the path the Wails/WebKitGTK shell takes (`webglPlatformAllows` returns
// false there), so the numbers describe the desktop client rather than a GPU
// path the desktop never uses.
//
// Segments, all measured in-page with performance.now():
//   sched    keydown -> the input fetch starts
//   post     fetch start -> its promise resolves on the main thread
//   render   response -> the character is in the terminal's DOM
//   total    keydown -> the character is in the DOM
//
// A frame is ~16.7 ms at 60 Hz. `render` below one frame means the write was not
// deferred to the next animation frame.
const { chromium } = require('./playwright.cjs')

const BASE = process.env.DFB_BASE
const TOKEN = process.env.DFB_TOKEN
const WID = process.env.DFB_WID
const SID = process.env.DFB_SID1
const SCALE = Number(process.env.DFB_ECHO_SCALE || 1.5)
const CHARS = Number(process.env.DFB_ECHO_CHARS || 20)
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

function fail(code, message) {
  console.error(`FAIL (${code}): ${message}`)
  process.exit(code)
}

async function signIn(page) {
  await page.goto(`${BASE}/?debug=0&panel=0`)
  await page.fill('#token', TOKEN)
  await page.click('#connect-form button[type=submit]')
  await page.waitForSelector('#app:not([hidden])', { timeout: 15000 })
  await sleep(500)
}

async function openSession(page) {
  const row = `[data-tree-session-open="${SID}"]`
  const visible = await page.locator(row).isVisible().catch(() => false)
  if (!visible) await page.click(`[data-wt-toggle="${WID}"]`)
  await page.waitForSelector(row, { timeout: 15000, state: 'visible' })
  await page.click(row)
  await page.waitForSelector('.xterm-screen', { timeout: 15000 })
  await sleep(2000)
}

;(async () => {
  const browser = await chromium.launch({
    headless: true,
    ...(process.env.DF_CHROMIUM ? { executablePath: process.env.DF_CHROMIUM } : {})
  })
  const context = await browser.newContext({
    viewport: { width: 1280, height: 900 },
    deviceScaleFactor: SCALE
  })
  const page = await context.newPage()
  await context.addInitScript(() => {
    window.__trace = { keydown: 0, postStart: 0, postEnd: 0, mutation: 0, samples: [] }
    const original = window.fetch
    window.fetch = function (...args) {
      const url = String(args[0] && args[0].url ? args[0].url : args[0])
      if (url.includes('/input')) window.__trace.postStart = performance.now()
      const done = original.apply(this, args)
      if (url.includes('/input')) {
        done.then(() => { window.__trace.postEnd = performance.now() }).catch(() => {})
      }
      return done
    }
    document.addEventListener('keydown', () => {
      const t = window.__trace
      t.keydown = performance.now()
      t.postStart = 0
      t.postEnd = 0
      t.mutation = 0
    }, true)
    const watch = () => {
      const rows = document.querySelector('.xterm-rows')
      if (!rows) { setTimeout(watch, 100); return }
      new MutationObserver(() => {
        const t = window.__trace
        if (!t.keydown || t.mutation) return
        t.mutation = performance.now()
        t.samples.push({
          sched: t.postStart ? t.postStart - t.keydown : -1,
          post: t.postEnd ? t.postEnd - t.postStart : -1,
          render: t.mutation - (t.postEnd || t.postStart || t.keydown),
          total: t.mutation - t.keydown
        })
      }).observe(rows, { subtree: true, childList: true, characterData: true })
    }
    watch()
  })
  page.on('pageerror', (error) => console.error('pageerror:', error.message))

  try {
    await signIn(page)
    await openSession(page)
    const renderer = await page.evaluate(() => ({
      domRows: document.querySelectorAll('.xterm-rows span').length,
      canvases: document.querySelectorAll('.xterm-screen canvas').length,
      dpr: window.devicePixelRatio
    }))
    if (renderer.canvases) {
      fail(2, 'this probe expects the DOM renderer; a canvas renderer is active')
    }

    const alphabet = 'abcdefghijklmnopqrstuvwxyz'
    for (let i = 0; i < CHARS; i++) {
      await page.keyboard.type(alphabet[i % alphabet.length])
      await sleep(120)
    }
    const samples = await page.evaluate(() => window.__trace.samples)
    if (samples.length < CHARS - 2) fail(2, `only ${samples.length} of ${CHARS} keystrokes were observed`)

    const stat = (key) => {
      const values = samples.map((s) => s[key]).filter((v) => v >= 0).sort((a, b) => a - b)
      const at = (p) => Math.round(values[Math.min(values.length - 1, Math.floor(values.length * p))] * 10) / 10
      return { p50: at(0.5), p90: at(0.9) }
    }
    const report = {
      scale: SCALE,
      renderer,
      sched_ms: stat('sched'),
      post_ms: stat('post'),
      render_ms: stat('render'),
      total_ms: stat('total')
    }
    console.log(JSON.stringify(report, null, 2))
    console.log(
      `echo: total p50 ${report.total_ms.p50} ms / p90 ${report.total_ms.p90} ms ` +
        `(sched ${report.sched_ms.p50}, post ${report.post_ms.p50}, render ${report.render_ms.p50}); ` +
        `DOM renderer at dpr ${renderer.dpr}`
    )
  } finally {
    await browser.close()
  }
})().catch((error) => {
  console.error(error)
  process.exit(1)
})
