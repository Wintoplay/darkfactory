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
  const trace = {outstanding:0, peak:0, writes:0, bytes:0, callbacks:0}
  const realRead = ReadableStreamDefaultReader.prototype.read
  ReadableStreamDefaultReader.prototype.read = function(...args) {
    return realRead.apply(this, args).then(result => {
      trace.streamReads = (trace.streamReads || 0) + 1
      trace.streamBytes = (trace.streamBytes || 0) + (result.value?.length || 0)
      if (result.done) trace.streamDone = true
      return result
    })
  }
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
    await pause(2000)
    if (!terminal) throw new Error('terminal global not available')
    terminal.clear()
    const originalWriteSession = writeSession
    writeSession = function(id, text) {
      trace.sessionWrites = (trace.sessionWrites || 0) + 1
      trace.sessionBytes = (trace.sessionBytes || 0) + text.length
      if (currentSession?.id !== id) trace.wrongSessionBytes = (trace.wrongSessionBytes || 0) + text.length
      return originalWriteSession(id, text)
    }
    let outstanding = 0
    let peak = 0
    let writes = 0
    let bytes = 0
    let markerTail = ''
    const originalWrite = terminal.write.bind(terminal)
    terminal.write = (data, callback) => {
      outstanding += data.length
      bytes += data.length
      writes++
      peak = Math.max(peak, outstanding)
      Object.assign(trace, {outstanding, peak, writes, bytes})
      const text = typeof data === 'string' ? data : ''
      const hasMarker = (markerTail + text).includes('DARKFLOODWEB')
      markerTail = (markerTail + text).slice(-12)
      originalWrite(data, () => {
        outstanding -= data.length; trace.outstanding = outstanding; trace.callbacks++
        if (hasMarker) trace.markerParsedMs = performance.now() - started
        if (callback) callback()
      })
    }
    const marker = 'DARKFLOODWEB'
    const started = performance.now()
    trace.startedWallMs = Date.now()
    terminal.input(`\x15python3 -c 'import sys; sys.stdout.write(("0123456789"*8+"\\n")*2000);print("DARK"+"FLOODWEB")'\r`, true)
    await waitFor(() => Array.from(screen.children, row => row.textContent.trimEnd()).join('').includes(marker), 'output sentinel', 15000)
    const end = performance.now()
    await waitFor(() => outstanding === 0, 'xterm write callbacks', 60000)
    send({ok:true, synthetic:true, dpr:devicePixelRatio, elapsedMs:end-started, peakQueuedChars:peak, bytes, writes, cols:terminal.cols, rows:terminal.rows, trace})
  })().catch(error => send({ok:false, error:String(error), samples, trace, state: {streaming, detached, session:currentSession?.status, queued:termWriteQueue.length, pendingFrame:termWriteFrame, oscCarry:oscCarry.length, selected:currentSession?.id, terminalSessionId}, screen: (document.querySelector('.xterm-rows')?.textContent || '').slice(-350)})) })
})()
