// Injected only into the isolated WebKitGTK probe, never into the shipped UI.
(() => {
  const cfg = JSON.parse(atob('__DF_CONFIG__'))
  const send = value => { document.title = 'DFPROBE:' + JSON.stringify(value) }
  const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
  async function waitFor(fn, name, timeout = 15000) {
    const until = performance.now() + timeout
    while (performance.now() < until) { const value = fn(); if (value) return value; await pause(50) }
    throw new Error('timeout waiting for ' + name)
  }
  const samples = []
  let active = null
  const originalFetch = window.fetch
  window.fetch = function (...args) {
    const url = String(args[0] && args[0].url ? args[0].url : args[0])
    const sample = url.includes('/input') ? active : null
    if (sample) sample.postStart = performance.now()
    const result = originalFetch.apply(this, args)
    if (sample) result.then(response => { sample.postEnd = performance.now(); sample.status = response.status }).catch(() => { sample.status = 0 })
    return result
  }
  window.addEventListener('load', () => { (async () => {
    const token = await waitFor(() => document.querySelector('#token'), 'token input')
    token.value = cfg.token
    token.dispatchEvent(new Event('input', { bubbles: true }))
    document.querySelector('#connect-form button[type=submit]').click()
    await waitFor(() => document.querySelector('#app:not([hidden])'), 'sign in')
    let row = document.querySelector(`[data-tree-session-open="${cfg.sid}"]`)
    if (!row || !row.getClientRects().length) {
      const toggle = await waitFor(() => document.querySelector(`[data-wt-toggle="${cfg.wid}"]`), 'workspace toggle')
      toggle.click()
    }
    row = await waitFor(() => { const e = document.querySelector(`[data-tree-session-open="${cfg.sid}"]`); return e && e.getClientRects().length && e }, 'session row')
    row.click()
    const screen = await waitFor(() => document.querySelector('.xterm-rows'), 'terminal rows')
    const input = await waitFor(() => document.querySelector('.xterm-helper-textarea'), 'terminal textarea')
    await pause(2000)
    const observer = new MutationObserver(() => {
      if (active && !active.visible && Array.from(screen.children, row => row.textContent.trimEnd()).join('').includes(active.expected)) active.visible = performance.now()
    })
    observer.observe(screen, {subtree:true, childList:true, characterData:true})
    const chars = 'qzxvjkmbprwytfgh'
    let expected = ''
    for (const char of chars) {
      expected += char
      const marker = expected.slice(-8)
      if (expected.length > 1 && Array.from(screen.children, row => row.textContent.trimEnd()).join('').includes(marker)) throw new Error('marker already visible before keydown')
      active = {expected: marker, keydown: performance.now(), postStart: 0, postEnd: 0, visible: 0, status: null}
      input.focus()
      const code = 'Key' + char.toUpperCase()
      input.dispatchEvent(new KeyboardEvent('keydown', {key: char, code, keyCode: char.toUpperCase().charCodeAt(0), which: char.toUpperCase().charCodeAt(0), bubbles:true, cancelable:true}))
      await waitFor(() => active.visible && active.postEnd, 'echo ' + expected, 5000)
      if (active.status < 200 || active.status >= 300 || !active.postStart) throw new Error('input request failed')
      samples.push([active.postStart-active.keydown, active.postEnd-active.postStart, active.visible-active.keydown].map(x => Math.round(x * 10) / 10))
      active = null
      await pause(150)
    }
    observer.disconnect()
    send({ok:true, synthetic:true, dpr:devicePixelRatio, ua:navigator.userAgent, samples})
  })().catch(error => send({ok:false, error:String(error), samples, screen: (document.querySelector('.xterm-rows')?.textContent || '').slice(-350)})) })
})()
