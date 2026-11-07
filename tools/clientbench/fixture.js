'use strict'

async function loadScript(src) {
  await new Promise((resolve, reject) => {
    const script = document.createElement('script')
    script.src = src
    script.async = true
    script.onload = () => resolve()
    script.onerror = () => reject(new Error(`failed to load ${src}`))
    document.head.appendChild(script)
  })
}

// The addon does not expose its context; read the canvas it created. This
// separates a real GPU from Chromium's SwiftShader or WebKit's software path.
function glRenderer(canvases) {
  for (const canvas of canvases) {
    try {
      const gl = canvas.getContext('webgl2') || canvas.getContext('webgl')
      if (!gl) continue
      const info = gl.getExtension('WEBGL_debug_renderer_info')
      const name = info ? gl.getParameter(info.UNMASKED_RENDERER_WEBGL) : gl.getParameter(gl.RENDERER)
      if (typeof name === 'string' && name) return name
    } catch (error) {
      return `unreadable: ${error.message}`
    }
  }
  return ''
}

async function run() {
  const params = new URLSearchParams(location.search)
  // The desktop shell's URL guard rejects a query, so /webgl is its path form.
  const wantWebgl = params.get('renderer') === 'webgl' || /\/webgl\/?$/.test(location.pathname)
  const term = new Terminal({ cols: 100, rows: 30, scrollback: 1000, cursorBlink: false })
  term.open(document.getElementById('terminal'))
  const readyMs = performance.now()
  const nextFrame = () => new Promise((resolve) => requestAnimationFrame(resolve))
  if (wantWebgl) {
    await loadScript('/vendor/addon-webgl.js')
    const addonGlobal = globalThis.WebglAddon
    const WebglAddonCtor = typeof addonGlobal === 'function' ? addonGlobal : addonGlobal?.WebglAddon
    if (typeof WebglAddonCtor !== 'function') throw new Error('WebglAddon is not defined')
    term.loadAddon(new WebglAddonCtor())
    await nextFrame()
  }
  const renderer = wantWebgl
    ? (glRenderer(document.querySelectorAll('#terminal canvas')) || 'webgl-unknown')
    : 'dom'
  const write = (data) => new Promise((resolve) => term.write(data, resolve))
  let renders = 0
  let lastRenderAt = 0
  const renderListener = term.onRender(() => {
    renders++
    lastRenderAt = performance.now()
  })
  const line = '0123456789'.repeat(8) + '\r\n'
  const chunk = line.repeat(100)
  const started = performance.now()
  for (let i = 0; i < 128; i++) await write(chunk)
  await write('\r\nDARKFACTORY_BENCH_DONE')
  const parsedAt = performance.now()
  const parseMs = parsedAt - started
  // The write callback proves parsing, not presentation. onRender fires after
  // the renderer draws; wait for the frame that contains the final sentinel so
  // the interval measures parse-plus-paint, not a frame from earlier output.
  const rendersBeforePaint = renders
  const paintDeadline = parsedAt + 2000
  while (renders === rendersBeforePaint && performance.now() < paintDeadline) await nextFrame()
  if (renders === rendersBeforePaint) throw new Error('renderer did not paint after parse')
  const renderMs = lastRenderAt - parsedAt
  const finalRenderMs = lastRenderAt - started
  renderListener.dispose()
  const buffer = term.buffer.active
  const finalLine = buffer.getLine(buffer.baseY + buffer.cursorY)?.translateToString(true)
  if (finalLine !== 'DARKFACTORY_BENCH_DONE') throw new Error('output sentinel missing')

  const echoWriteMs = []
  for (let i = 0; i < 30; i++) {
    await new Promise((resolve) => {
      const start = performance.now()
      const listener = term.onData((data) => {
        listener.dispose()
        term.write(data, () => {
          echoWriteMs.push(performance.now() - start)
          resolve()
        })
      })
      term.input('x', true)
    })
  }
  return {
    schema: 1, workload: 'xterm-parse-echo-v1', synthetic: true,
    userAgent: navigator.userAgent, viewport: [innerWidth, innerHeight],
    dpr: devicePixelRatio, cols: term.cols, rows: term.rows,
    renderer, webgl: wantWebgl,
    readyMs, parseMs, renderMs, finalRenderMs, renderCount: renders,
    bytes: chunk.length * 128,
    sentinel: finalLine, echoWriteMs, errors: []
  }
}

run().catch((error) => ({ schema: 1, errors: [String(error)] })).then(async (result) => {
  document.getElementById('result').textContent = JSON.stringify(result, null, 2)
  const response = await fetch('/result', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(result)
  })
  if (!response.ok) throw new Error(`result submission failed: ${response.status}`)
})
